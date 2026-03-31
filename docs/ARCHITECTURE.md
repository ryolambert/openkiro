# Architecture & Expertise Reference

> Comprehensive technical reference for developing and improving the openkiro project.
> This document is optimized for AI coding agents and human developers alike.

## Table of Contents

- [System Overview](#system-overview)
- [Component Architecture](#component-architecture)
- [Request Lifecycle](#request-lifecycle)
- [Streaming Protocol](#streaming-protocol)
- [Middleware System](#middleware-system)
- [Token & Authentication](#token--authentication)
- [Model Resolution](#model-resolution)
- [Docker Sandbox Infrastructure](#docker-sandbox-infrastructure)
- [Headroom Compression](#headroom-compression)
- [Daemon & Service Integration](#daemon--service-integration)
- [Security Architecture](#security-architecture)
- [Testing Strategy](#testing-strategy)
- [CI/CD Pipeline](#cicd-pipeline)
- [Design Patterns Reference](#design-patterns-reference)
- [API Surface Reference](#api-surface-reference)
- [Error Handling Patterns](#error-handling-patterns)
- [Performance Considerations](#performance-considerations)
- [Cross-Platform Support](#cross-platform-support)
- [Development Workflow](#development-workflow)
- [Glossary](#glossary)

---

## System Overview

openkiro is a **local-first API proxy** that enables Anthropic-compatible AI clients to use AWS CodeWhisperer through Kiro SSO authentication. It is designed as an orchestration backbone for AI agent development workflows.

```
┌─────────────────┐     ┌──────────────────┐     ┌──────────────────────────┐
│  AI Client       │────▶│  openkiro proxy   │────▶│  AWS CodeWhisperer API   │
│  (Claude Code,   │◀────│  :1234            │◀────│  us-east-1               │
│   Cursor, etc.)  │     │                    │     │                          │
└─────────────────┘     └──────────────────┘     └──────────────────────────┘
                         ▲                  │
                         │  Token from       │
                         │  ~/.aws/sso/      │
                         │  cache/           │
                         │  kiro-auth-       │
                         │  token.json       │
```

### Key Design Decisions

1. **Zero external dependencies** — Only `golang.org/x/sys` for Windows. Everything else is stdlib.
2. **Local-only by default** — Binds to `127.0.0.1` to prevent network exposure.
3. **Stateless proxy** — No database, no persistent state beyond token files and credentials.
4. **Streaming-first** — SSE streaming is the primary path; non-streaming is a convenience wrapper.
5. **Composable middleware** — All request/response transformations are pluggable via the middleware chain.

---

## Component Architecture

### Package Dependency Graph

```
cmd/openkiro
  ├── internal/proxy     (HTTP server, request/response translation)
  │     ├── internal/protocol   (binary frame → SSE event parser)
  │     └── internal/token      (auth token I/O, HTTP client pool)
  ├── internal/sandbox   (Docker container lifecycle)
  └── internal/middleware (composable interceptors)
        └── internal/headroom   (compression proxy integration)

cmd/headroom → internal/headroom  (standalone proxy launcher)
cmd/mcp-gateway → internal/gateway (MCP server discovery)
```

### File Ownership Map

| File | Owner | Purpose |
|------|-------|---------|
| `internal/proxy/types.go` | proxy | All type definitions, constants, model map |
| `internal/proxy/request.go` | proxy | Anthropic → CW request translation, model resolution, UUID generation |
| `internal/proxy/response.go` | proxy | CW → Anthropic response assembly, JSON payload building |
| `internal/proxy/server.go` | proxy | HTTP server setup, route handlers, SSE streaming, retry logic |
| `internal/token/token.go` | token | Token file I/O, refresh, debug logging, upstream HTTP client |
| `internal/token/credentials.go` | token | ~/.openkiro/ credential storage (0600 perms) |
| `internal/protocol/sse_parser.go` | protocol | Binary frame parser: length-prefixed headers → payload extraction |
| `internal/middleware/middleware.go` | middleware | Interface definition, Chain, NoopMiddleware |
| `internal/middleware/headroom.go` | middleware | Headroom compression middleware adapter |
| `internal/headroom/manager.go` | headroom | Python process lifecycle (install, start, stop, health) |
| `internal/headroom/client.go` | headroom | HTTP client for headroom proxy communication |
| `internal/headroom/config.go` | headroom | Configuration defaults and validation |
| `internal/daemon/daemon.go` | daemon | PID files, launchd plist, Claude config |
| `internal/daemon/alias.go` | daemon | Shell alias/function generation (bash, zsh, PowerShell, cmd) |
| `internal/sandbox/sandbox.go` | sandbox | Docker container create/start/stop/destroy/list |
| `internal/sandbox/agent.go` | sandbox | Preset configs: Default, Agent, ClaudeCode, Kiro |
| `internal/gateway/gateway.go` | gateway | Docker MCP server discovery and routing |

---

## Request Lifecycle

### Streaming Request (primary path)

```
1. Client POST /v1/messages {stream: true}
   │
2. ├── token.GetToken() → reads ~/.aws/sso/cache/kiro-auth-token.json
   │
3. ├── json.Unmarshal → proxy.AnthropicRequest
   │
4. ├── Validate: model ≠ "", messages ≠ []
   │
5. ├── proxy.ResolveModelID(req.Model) → CW model ID
   │
6. ├── proxy.BuildCodeWhispererRequest(req) → CW request struct
   │
7. ├── proxy.EnsurePayloadFits(&cwReq) → JSON body (trim if >250MB)
   │
8. ├── HTTP POST to codewhisperer.us-east-1.amazonaws.com/generateAssistantResponse
   │     Headers: Authorization: Bearer <token>, Accept: text/event-stream
   │
9. ├── Retry logic (up to 3 attempts):
   │     • 400 "Improperly formed" → trim history + simplify tool schemas → retry
   │     • 403 → token.RefreshToken() → retry with new token
   │     • Other errors → send error SSE event → return
   │
10.├── Send message_start SSE event (with conversation_id, model, usage)
   │
11.├── Send ping event
   │
12.├── protocol.ParseEventStream(resp.Body, callback)
   │     For each binary frame:
   │     │ Parse length-prefixed header
   │     │ Extract event type + payload
   │     │ Emit as SSE event via callback
   │
13.└── Send message_stop event
```

### Non-Streaming Request

Same as steps 1-8, then:
```
9.  ├── io.ReadAll(resp.Body) → raw CW response bytes
10. ├── protocol.ParseEvents(cwRespBody) → []SSEEvent
11. ├── proxy.AssembleAnthropicResponse(events) → TranslatedAnthropicResponse
12. └── proxy.BuildAnthropicResponsePayload(...) → JSON response
```

---

## Streaming Protocol

### CodeWhisperer Binary Frame Format

CodeWhisperer returns a custom binary event-stream (not standard SSE). Each frame consists of:

```
┌─────────────────────────────────────────────────────┐
│  Total byte length (4 bytes, big-endian)            │
├─────────────────────────────────────────────────────┤
│  Header byte length (4 bytes, big-endian)           │
├─────────────────────────────────────────────────────┤
│  Headers (variable length, opaque to the parser)    │
├─────────────────────────────────────────────────────┤
│  Payload (variable length, JSON)                    │
├─────────────────────────────────────────────────────┤
│  Trailing CRC (4 bytes, read but not validated)     │
└─────────────────────────────────────────────────────┘
```

> **Note:** The current `protocol.ParseEventStream` implementation reads the header bytes but does not interpret individual header key-value pairs (e.g., `:event-type`). Instead, it infers the event type from the JSON payload content. CRC fields are read to advance the reader but are not validated.

### Anthropic SSE Output Format

The proxy converts to standard SSE format:
```
event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_stop
data: {"type":"message_stop"}
```

### SSE Event Types Emitted

| Event | When | Data Shape |
|-------|------|-----------|
| `message_start` | Beginning of response | `{type, message: {id, role, model, usage, ...}}` |
| `ping` | After message_start | `{type: "ping"}` |
| `content_block_start` | New text or tool_use block | `{type, index, content_block}` |
| `content_block_delta` | Incremental content | `{type, index, delta}` |
| `content_block_stop` | Block complete | `{type, index}` |
| `message_delta` | Usage/stop updates | `{type, delta, usage}` |
| `message_stop` | Response complete | `{type: "message_stop"}` |
| `error` | Any error | `{type: "error", error: {type, message}}` |

---

## Middleware System

### Interface

```go
type Middleware interface {
    ProcessRequest(req *proxy.AnthropicRequest) (*proxy.AnthropicRequest, error)
    ProcessResponse(resp []byte) ([]byte, error)
    Name() string
}
```

### Chain Execution

```
Request:  Client → M1.ProcessRequest → M2.ProcessRequest → ... → CodeWhisperer
Response: CodeWhisperer → M1.ProcessResponse → M2.ProcessResponse → ... → Client
```

Middlewares execute in **insertion order**. If any middleware returns an error, the chain stops immediately with a wrapped error: `middleware "name" ProcessRequest: <cause>`.

### Implementing a New Middleware

1. Create `internal/middleware/<name>.go` implementing the `Middleware` interface
2. Create `internal/middleware/<name>_test.go` with table-driven tests
3. Register in the appropriate server setup code via `chain.Add(myMiddleware)`
4. Follow TDD: write failing test first, then implement

### Existing Middlewares

| Name | File | Purpose |
|------|------|---------|
| `NoopMiddleware` | `middleware.go` | Pass-through; useful as default/test placeholder |
| `HeadroomMiddleware` | `headroom.go` | Routes requests through headroom compression proxy |

---

## Token & Authentication

### Token Source

```
~/.aws/sso/cache/kiro-auth-token.json
```

Structure:
```json
{
  "accessToken": "eyJ...",
  "refreshToken": "...",
  "expiresAt": "2025-01-01T00:00:00Z"
}
```

### Token Refresh Flow

```
1. Request fails with HTTP 403
2. token.RefreshToken() invoked
   └── Attempts Kiro CLI database sync
3. token.GetToken() re-reads the file
4. Retry original request with new token
```

### Credential Storage

`token.WriteCredentials(baseURL, accessToken)` writes to `~/.openkiro/credentials.json` with `0600` permissions. This allows companion tools to discover the proxy URL and token.

### Debug Logging

Controlled by `OPENKIRO_DEBUG=1` (fallback: `KIROLINK_DEBUG=1`):
```go
token.DebugLogf("format %s", args...)     // conditional debug log
token.DebugLoggingEnabled() bool           // check if debug mode is on
token.DebugLogBodySummary(label, body)     // log body size + first N chars
```

### Upstream HTTP Client

`token.GetUpstreamClient()` returns a shared `*http.Client` with:
- 60-second timeout (`UpstreamHTTPTimeout`)
- Connection pooling (`MaxIdleConnsPerHost: 10`)
- Idle connection timeout: 90s (`IdleConnTimeout`)

---

## Model Resolution

`proxy.ResolveModelID(requested)` maps client-facing model names to CodeWhisperer model IDs.

### Resolution Strategy

1. **Exact match** — look up in `ModelMap` (case-insensitive, including the literal alias `"default"` → `ModelSonnet45`)
2. **Passthrough** — if prefixed with `claude_` (uppercase), pass as-is
3. **Fuzzy match** — keyword-based fallback:
   - Contains "sonnet" + "4-5" or "4.5" → `ModelSonnet45`
   - Contains "sonnet" → `ModelSonnet46`
   - Contains "opus" → `ModelOpus46`
   - Contains "haiku" → `ModelHaiku45`
4. **Empty/unknown** — when `requested` is `""` or no rule above matches, `ResolveModelID` falls back to `ModelSonnet46`

### Current Model Constants

| Constant | CodeWhisperer ID |
|----------|-----------------|
| `ModelSonnet46` | `CLAUDE_SONNET_4_6_V1_0` |
| `ModelSonnet45` | `CLAUDE_SONNET_4_5_20250929_V1_0` |
| `ModelOpus46` | `CLAUDE_OPUS_4_6_V1_0` |
| `ModelHaiku45` | `CLAUDE_HAIKU_4_5_20251001_V1_0` |

---

## Docker Sandbox Infrastructure

### Overview

openkiro includes Docker-based sandbox management for running AI agents in isolated environments. This follows the **Agent-in-Sandbox** pattern where the entire agent runs inside a container with controlled API access.

### Container Lifecycle

```
Create (docker create) → Start (docker start) → [Use] → Stop (docker stop) → Destroy (docker rm)
```

### Preset Configurations

| Preset | Network | Root FS | Special Env Vars |
|--------|---------|---------|-----------------|
| `DefaultConfig()` | none | read-only | — |
| `AgentConfig()` | bridge | read-only | — |
| `ClaudeCodeConfig()` | bridge | read-only | `ANTHROPIC_BASE_URL`, `ANTHROPIC_API_KEY` |
| `KiroConfig()` | bridge | read-only | `ANTHROPIC_BASE_URL`, `ANTHROPIC_API_KEY`, `KIRO_PROXY` |

### Sandbox Images

| Dockerfile | Base | Purpose |
|-----------|------|---------|
| `Dockerfile` | `gcr.io/distroless/static-debian12:nonroot` | Minimal openkiro binary image |
| `Dockerfile.sandbox` | Alpine 3.20 | Standalone sandbox with all tools |
| `Dockerfile.sandbox-claude` | `docker/sandbox-templates:claude-code` | Claude Code sandbox extension |
| `Dockerfile.sandbox-kiro` | `docker/sandbox-templates:kiro` | Kiro sandbox extension |

---

## Headroom Compression

### Purpose

The headroom subsystem integrates a Python-based compression proxy for reducing context size before sending to CodeWhisperer, improving token efficiency.

### Architecture

```
Request → HeadroomMiddleware → headroom Python proxy → compressed request → CodeWhisperer
```

### Manager Lifecycle

```go
mgr := headroom.NewManager(cfg)
mgr.Install()    // pip install headroom-ai
mgr.Start()      // launch Python process
mgr.Health()     // HTTP health check
mgr.Stop()       // graceful shutdown via done channel
```

### Key Implementation Details

- Uses `pythonBin()` to resolve the Python interpreter (not `pipCommand`)
- Install runs `python -m pip install` as a subprocess
- `Stop()` uses a done channel with mutex unlock/relock to avoid deadlock
- Health checks via HTTP GET to the headroom proxy's health endpoint

---

## Daemon & Service Integration

### macOS (launchd)

- Generates `.plist` files for `LaunchAgents`
- Label: `com.openkiro.proxy`
- PID file management for process lifecycle tracking

### Windows (Service Manager)

- Build-tagged via `internal/service/windows.go` (only compiled on Windows)
- Stub in `internal/service/stub.go` for non-Windows platforms

### Shell Alias Generation

`internal/daemon/alias.go` generates shell aliases/functions:
- **bash/zsh**: function that sets `ANTHROPIC_BASE_URL` and `ANTHROPIC_API_KEY`
- **PowerShell**: `$env:` variable assignments
- **cmd**: `set` commands

---

## Security Architecture

### Defense-in-Depth Layers

1. **Network isolation** — `127.0.0.1` binding by default; warning logged if overridden
2. **Request size limits** — 200 MiB `MaxBytesReader` on all request bodies
3. **Server timeouts** — Read: 30s, Write: 60s, Idle: 120s, Header: 10s
4. **Panic recovery** — Generic `{"error":{"type":"server_error","message":"Internal server error"}}` response; recovered value logged (no stack trace)
5. **Token redaction** — Only first 8 + last 4 characters shown in any log output
6. **Credential file permissions** — `0600` on `~/.openkiro/credentials.json`
7. **No credential baking** — Docker images never contain tokens; injected at runtime
8. **Read-only root FS** — Sandbox containers use read-only root filesystem by default

### Threat Model Assumptions

- The local machine is trusted (local-only proxy)
- Kiro SSO tokens may expire and must be refreshable
- Upstream responses may be malformed and must be parsed defensively
- Docker sandbox escape is mitigated by network mode and FS restrictions

---

## Testing Strategy

### Test Pyramid

```
Unit Tests (internal/*_test.go)        ← Primary: fast, isolated, table-driven
Integration Tests (manager_internal_test.go) ← Secondary: compiled binary as fake proxy
Smoke Tests (Makefile, CI)             ← Docker builds, health checks
```

### Test Utilities

| Utility | Location | Purpose |
|---------|----------|---------|
| `SetupTestServer(t)` | `testutil/helpers.go` | Mock HTTP server returning canned responses |
| `AssertJSONEqual(t, a, b)` | `testutil/helpers.go` | Deep JSON comparison (order-independent) |
| `LoadTestData(t, name)` | `testutil/helpers.go` | Load fixture from `testutil/testdata/` |
| `MustMarshal(t, v)` | `testutil/helpers.go` | JSON marshal or `t.Fatal` |

### Test Seams

Package-level variables allow deterministic testing:
- `uuidEntropySource` — inject controlled entropy for UUID generation
- `uuidFallbackSerial` — atomic counter for fallback UUID uniqueness

### Coverage Requirements

- **CI threshold**: 45% minimum (enforced by `COVERAGE_THRESHOLD` env var)
- **CONTRIBUTING.md target**: 50% minimum for new packages
- Coverage ratchet: threshold only increases, never decreases

---

## CI/CD Pipeline

### Workflow Summary

| Workflow | Trigger | Jobs |
|----------|---------|------|
| `ci.yml` | push/PR to main | Build (3 OS), Test (-race), Vet, Coverage gate, Lint, Security scan |
| `docker.yml` | push/PR/tags | hadolint, Docker build, smoke test, Trivy scan, GHCR push (tags only) |
| `release.yml` | version tags | Test → Lint → GoReleaser |
| `snapshot.yml` | weekly schedule | GoReleaser snapshot build |
| `tag-release.yml` | manual dispatch | Auto-bump semver, create git tag |

### Quality Gates

1. `go build ./...` succeeds on Ubuntu, macOS, Windows
2. `go test -race -count=1 -coverprofile=coverage.out ./...` passes
3. `go vet ./...` clean
4. `golangci-lint run` clean (errcheck, govet, staticcheck, unused)
5. `staticcheck ./...` clean
6. `govulncheck ./...` no known vulnerabilities
7. Coverage ≥ 45%
8. GoReleaser config valid (`goreleaser check`)

---

## Design Patterns Reference

### Adapter Pattern
**Where**: `internal/proxy/request.go` — `BuildCodeWhispererRequest()`
**Purpose**: Translates Anthropic API message format to CodeWhisperer's conversationState format, including history, tools, and system messages.

### Chain of Responsibility
**Where**: `internal/middleware/middleware.go` — `Chain`
**Purpose**: Ordered sequence of middleware instances that process requests and responses. Each can modify, augment, or reject the payload.

### Strategy Pattern
**Where**: `internal/proxy/request.go` — `ResolveModelID()`
**Purpose**: Multiple resolution strategies (exact match → prefix match → fuzzy match → default) tried in order.

### Factory Pattern
**Where**: `internal/sandbox/agent.go` — `DefaultConfig()`, `AgentConfig()`, etc.
**Purpose**: Pre-configured sandbox settings for different use cases without exposing implementation details.

### Observer/Callback Pattern
**Where**: `internal/protocol/sse_parser.go` — `ParseEventStream(body, callback)`
**Purpose**: Binary frame parser invokes a callback function for each parsed SSE event, decoupling parsing from delivery.

### Template Method (Docker)
**Where**: `Dockerfile.sandbox-claude`, `Dockerfile.sandbox-kiro`
**Purpose**: Extend base sandbox templates with openkiro-specific layers and configuration.

---

## API Surface Reference

### POST /v1/messages

**Request** (Anthropic Messages API format):
```json
{
  "model": "claude-sonnet-4-6",
  "max_tokens": 4096,
  "messages": [{"role": "user", "content": "Hello"}],
  "system": [{"type": "text", "text": "You are helpful."}],
  "tools": [{"name": "bash", "description": "Run commands", "input_schema": {...}}],
  "stream": true,
  "temperature": 0.7,
  "conversation_id": "optional-uuid"
}
```

**Response (streaming)**: SSE event stream (see [Streaming Protocol](#streaming-protocol))

**Response (non-streaming)**:
```json
{
  "id": "msg_20250101120000",
  "type": "message",
  "role": "assistant",
  "content": [{"type": "text", "text": "Hello!"}],
  "model": "claude-sonnet-4-6",
  "stop_reason": "end_turn",
  "usage": {"input_tokens": 10, "output_tokens": 5}
}
```

### GET /v1/models

Returns all model aliases from `ModelMap` in deterministic sorted order:
```json
{
  "object": "list",
  "data": [
    {"id": "claude-3-5-haiku-20241022", "object": "model", "created": 1686960000, "owned_by": "anthropic"},
    ...
  ]
}
```

### GET /health

Returns `200 OK` with body `OK`.

---

## Error Handling Patterns

### HTTP Error Responses

| Status | Condition | Response |
|--------|-----------|----------|
| 400 | Missing model or messages | `{"message":"Missing required field: model"}` |
| 405 | Non-POST to /v1/messages | `"Only POST requests are supported"` |
| 413 | Body > 200 MiB | `"Request body exceeds N bytes"` |
| 500 | Token read failure | `"Failed to get token: <detail>"` |
| 500 | Panic recovery | `{"error":{"type":"server_error","message":"Internal server error"}}` |

### SSE Error Events

Errors during streaming are sent as SSE events rather than HTTP error codes:
```
event: error
data: {"type":"error","error":{"type":"overloaded_error","message":"<description>"}}
```

### Retry Strategy

The streaming handler retries up to 3 times:
1. **400 "Improperly formed"**: Trim history to 2 most recent pairs, simplify tool schemas, retry
2. **403 Unauthorized**: Refresh token from Kiro CLI, retry with new token
3. **Other errors**: Fail immediately with error SSE event

---

## Performance Considerations

### Connection Pooling

`token.GetUpstreamClient()` returns a singleton `*http.Client` configured with:
- `MaxIdleConnsPerHost: 10`
- `IdleConnTimeout: 90s`
- a request-level timeout of 60s via `http.Client.Timeout`

### Streaming Efficiency

- `http.Flusher.Flush()` called after every SSE event for low latency
- `http.ResponseController.SetWriteDeadline()` extended per-event to prevent timeout on long streams
- No buffering of the full response — events are forwarded as they arrive

### Payload Management

- `EnsurePayloadFits()` checks against `MaxPayloadBytes` (~250MB) and trims if necessary
- `KeepMostRecentHistory()` retains only the N most recent conversation pairs
- `TruncateString()` limits tool descriptions to prevent payload bloat
- `SimplifyToolSchemas()` reduces complex JSON Schema to `{"type": "object"}` for retries

---

## Cross-Platform Support

### OS-Specific Behaviour

| Feature | macOS/Linux | Windows |
|---------|-------------|---------|
| Token path | `~/.aws/sso/cache/kiro-auth-token.json` | `%USERPROFILE%\.aws\sso\cache\kiro-auth-token.json` |
| Daemon | launchd plist | Windows Service Manager |
| Shell aliases | bash/zsh functions | PowerShell/cmd set commands |
| Credential perms | `os.WriteFile(..., 0600)` | `os.WriteFile(..., 0600)` (no explicit ACLs) |
| Build | `CGO_ENABLED=0` | `CGO_ENABLED=0` |

### Build Tags

- `internal/service/windows.go` — compiled only on Windows (`//go:build windows`)
- `internal/service/stub.go` — compiled on all other platforms (`//go:build !windows`)

---

## Development Workflow

### Adding a New Feature

1. **Write failing test** — `test: add failing tests for <feature>`
2. **Implement minimum code** — `feat: implement <feature>`
3. **Refactor** — `refactor: clean up <feature>`
4. **Verify**: `go vet ./... && go test -race -count=1 ./...`

### Adding a New Middleware

1. Create `internal/middleware/<name>.go` implementing `Middleware` interface
2. Create `internal/middleware/<name>_test.go` with table-driven tests
3. Optionally add an upstream adapter package in `internal/<name>/`
4. Register via `chain.Add()` in server setup
5. Update docs if the middleware changes observable behaviour

### Adding a New Model Alias

1. Add constant in `internal/proxy/types.go` if it's a new CW model ID
2. Add entry to `ModelMap` in `internal/proxy/types.go`
3. Update fuzzy matching in `ResolveModelID()` if needed
4. Add test case in `internal/proxy/request_test.go`

### Adding a New CLI Command

1. Add case in `cmd/openkiro/main.go` switch statement
2. Create handler function in the same file or a new internal package
3. Update `printUsage()` with the new command
4. Add tests

### Releasing a New Version

```bash
make check              # Run all quality gates
make tag-patch          # or tag-minor, tag-major
# CI handles the rest: test → lint → GoReleaser → GHCR
```

---

## Glossary

| Term | Definition |
|------|-----------|
| **CodeWhisperer (CW)** | AWS's AI coding assistant backend that openkiro proxies to |
| **Kiro SSO** | Amazon's single sign-on authentication used to obtain access tokens |
| **SSE** | Server-Sent Events — HTTP streaming protocol for real-time event delivery |
| **Headroom** | Python-based context compression proxy for reducing token usage |
| **MCP** | Model Context Protocol — Docker's standard for tool server discovery |
| **Sandbox** | Isolated Docker container for running AI agent workloads |
| **Middleware** | Pluggable interceptor in the request/response chain |
| **Model alias** | Human-friendly name (e.g., `claude-sonnet-4-6`) mapped to CW internal ID |
| **Conversation ID** | UUID tracking a multi-turn conversation session |
| **Profile ARN** | AWS resource identifier for IAM Identity Center access |
| **Builder ID** | AWS free-tier authentication identity |
| **launchd** | macOS system for managing background processes (daemons) |
