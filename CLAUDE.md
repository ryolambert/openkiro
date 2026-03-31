# CLAUDE.md

This file provides guidance to Claude Code when working with code in this repository.

## Project Overview

openkiro is a zero-dependency Go CLI and HTTP proxy that translates Anthropic API requests into AWS CodeWhisperer calls, using Kiro SSO tokens for authentication. It enables any Anthropic-compatible client (Claude Code, Cursor, Continue, etc.) to use Kiro's backend transparently.

## Build, Test, and Lint Commands

```bash
# Build (produces bin/openkiro)
go build -o bin/openkiro ./cmd/openkiro

# Run all tests with race detection
go test -race -count=1 ./...

# Run a single package's tests
go test -race -v ./internal/proxy/...

# Vet + lint (requires golangci-lint)
go vet ./...
golangci-lint run

# All quality gates
make check

# Build Docker sandbox templates
make sandbox-all
```

## CLI Commands

```
openkiro server [port]          Start proxy (default :1234, override via $OPENKIRO_PORT)
openkiro sandbox create|start|stop|destroy|list   Manage Docker agent sandboxes
openkiro version                Print version, commit, build date
```

## Architecture

### Package Layout

| Package | Responsibility |
|---------|---------------|
| `cmd/openkiro` | CLI entry point — `server`, `sandbox`, `version` commands |
| `cmd/headroom` | Standalone headroom compression proxy launcher |
| `cmd/rtk`, `cmd/icm`, `cmd/mcp-gateway` | Companion tool entry points |
| `internal/proxy` | HTTP server, request/response translation, model mapping, SSE streaming |
| `internal/token` | Token I/O from `~/.aws/sso/cache/kiro-auth-token.json`, refresh, debug logging, upstream HTTP client pool |
| `internal/protocol` | Binary frame parser — CodeWhisperer event-stream → Anthropic SSE events |
| `internal/middleware` | `Middleware` interface and `Chain` — composable request/response interceptors |
| `internal/headroom` | Python headroom proxy lifecycle (install, start, stop, health) + HTTP client |
| `internal/daemon` | Background process utilities, shell alias generation, launchd/plist support |
| `internal/sandbox` | Docker container lifecycle for isolated agent environments with preset configs |
| `internal/gateway` | Docker MCP (Model Context Protocol) server discovery and routing |
| `internal/service` | Windows service stubs (build-tagged) |
| `internal/testutil` | Shared test helpers: mock server, JSON comparison, fixture loading from `testutil/testdata/` |

### Request Flow

```
Client (Anthropic API) → POST /v1/messages
  → token.GetToken() reads ~/.aws/sso/cache/kiro-auth-token.json
  → proxy.BuildCodeWhispererRequest() translates Anthropic → CW format
  → HTTP POST to codewhisperer.us-east-1.amazonaws.com/generateAssistantResponse
  → protocol.ParseEventStream() parses binary CW frames → SSE events
  → Streamed back as text/event-stream (or assembled as JSON for non-stream)
```

### HTTP Endpoints

- `POST /v1/messages` — Anthropic Messages API proxy (streaming and non-streaming)
- `GET  /v1/models`   — Lists available model aliases (deterministic sort order)
- `GET  /health`      — Returns `OK` (200)

### Key Types

- `proxy.AnthropicRequest` / `proxy.CodeWhispererRequest` — request translation pair
- `proxy.ModelMap` — alias → CodeWhisperer model ID mapping (in `internal/proxy/types.go`)
- `protocol.SSEEvent` — parsed streaming event
- `middleware.Middleware` — interface: `ProcessRequest`, `ProcessResponse`, `Name`
- `middleware.Chain` — ordered middleware composition
- `token.Data` — `{ accessToken, refreshToken, expiresAt }`

### Design Patterns

- **Chain of Responsibility** — middleware chain for request/response interception
- **Adapter** — translates between Anthropic and CodeWhisperer API formats
- **Strategy** — model resolution with fuzzy fallback matching
- **Factory** — sandbox preset configs (`DefaultConfig`, `AgentConfig`, `ClaudeCodeConfig`, `KiroConfig`)

## Conventions

- **Zero external deps** — only `golang.org/x/sys` (for Windows service support); stdlib for everything else
- **Debug logging** — gated by `OPENKIRO_DEBUG` env var (fallback: `KIROLINK_DEBUG`); use `token.DebugLogf()` not `log.Printf` for per-request diagnostics
- **Error handling** — always wrap with `fmt.Errorf("context: %w", err)`; never ignore returned errors in production code
- **Test style** — table-driven tests, `_test.go` files alongside source, fixtures via `testutil.LoadTestData(t, "file.json")`
- **TDD workflow** — Red → Green → Refactor; see `docs/CONTRIBUTING.md`
- **Conventional Commits** — `test:`, `feat:`, `refactor:`, `fix:`, `docs:`, `ci:`
- **CI coverage threshold** — 45% minimum (enforced in `.github/workflows/ci.yml`)
- **Token redaction** — first 8 + last 4 chars only in logs
- **Server security** — binds `127.0.0.1` by default; 200 MiB max request body; read/write/idle/header timeouts configured

## Verification

After making changes, always run:
```bash
go vet ./...
go test -race -count=1 ./...
```

For Docker changes, build and smoke-test:
```bash
make sandbox-claude
docker run --rm openkiro-sandbox-claude:latest openkiro version
```
