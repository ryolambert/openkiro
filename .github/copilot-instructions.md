# Copilot Instructions

This repository is **openkiro** — a zero-dependency Go CLI and HTTP proxy that translates Anthropic API requests into AWS CodeWhisperer calls using Kiro SSO authentication.

## Technology Stack

- **Language**: Go 1.25+ (stdlib only; sole external dep is `golang.org/x/sys` for Windows service support)
- **Build**: `go build -o bin/openkiro ./cmd/openkiro` or `make build`
- **Test**: `go test -race -count=1 ./...`
- **Lint**: `go vet ./...` then `golangci-lint run`
- **CI**: GitHub Actions — Ubuntu, macOS, Windows; 45% coverage gate; govulncheck

## Project Structure

- `cmd/openkiro/` — CLI entry point (server, sandbox, version)
- `cmd/{headroom,rtk,icm,mcp-gateway}/` — companion tool binaries
- `internal/proxy/` — HTTP server, Anthropic ↔ CodeWhisperer translation, SSE streaming
- `internal/token/` — Kiro SSO token I/O, refresh, debug logging, upstream HTTP pool
- `internal/protocol/` — Binary frame parser for CW event-stream → Anthropic SSE
- `internal/middleware/` — Middleware interface + Chain for composable interception
- `internal/headroom/` — Python headroom proxy lifecycle management + HTTP client
- `internal/daemon/` — Background process utils, shell alias generation, launchd plist
- `internal/sandbox/` — Docker container lifecycle for agent sandboxes (preset configs)
- `internal/gateway/` — Docker MCP server discovery and routing
- `internal/service/` — Windows service stubs (build-tagged)
- `internal/testutil/` — Shared test helpers and `testdata/` fixtures

## Coding Conventions

- Never add external dependencies without strong justification — stdlib-first
- Always check and propagate errors; wrap with `fmt.Errorf("context: %w", err)`
- Use `token.DebugLogf()` for debug output (gated by `OPENKIRO_DEBUG` env var)
- Use `log.Printf()` only for errors and warnings, never for per-request stats
- Follow Go naming conventions: exported names are PascalCase, unexported are camelCase
- Keep functions focused and short; prefer composition over inheritance
- All public types and functions must have Go doc comments

## Testing

- Follow TDD: Red → Green → Refactor (see `docs/CONTRIBUTING.md`)
- Use table-driven tests with descriptive subtest names
- Place `_test.go` files alongside their source
- Load shared fixtures via `testutil.LoadTestData(t, "filename.json")`
- Use `testutil.SetupTestServer(t)` for HTTP mock servers
- Always run with `-race` flag
- No production code without corresponding tests

## Security

- Server binds to `127.0.0.1` only by default
- Request bodies capped at 200 MiB via `http.MaxBytesReader`
- Tokens redacted in logs (first 8 + last 4 chars)
- Panic recovery returns generic error JSON, never internal details
- Server timeouts: Read 30s, Write 60s, Idle 120s, Header 10s
- Credentials stored with 0600 permissions in `~/.openkiro/`

## Streaming & Protocol

- Primary response path is SSE (`text/event-stream`); always `Flush()` after each event
- CodeWhisperer returns a **custom binary event-stream** — not standard SSE
  - Frame: `[4B total-len][4B header-len][N header-bytes (opaque)][M payload (JSON)][4B CRC (read, not validated)]`
  - Event type is inferred from the JSON payload, not from binary header fields
- Anthropic SSE events emitted: `message_start`, `ping`, `content_block_start`, `content_block_delta`, `content_block_stop`, `message_delta`, `message_stop`
- Non-streaming: `io.ReadAll` → `protocol.ParseEvents` → `proxy.AssembleAnthropicResponse`

## Middleware System

- `middleware.Middleware` interface: `ProcessRequest`, `ProcessResponse`, `Name`
- `middleware.Chain` applies middlewares in **insertion order**; any error stops the chain
- All new proxy features must be implemented as middlewares — do not modify `server.go` directly
- Error propagation: `fmt.Errorf("middleware %q ProcessRequest: %w", m.Name(), err)`

## MCP & Agent Workflow

- `internal/gateway/` implements Docker MCP (Model Context Protocol) server discovery
- `cmd/mcp-gateway` exposes discovered MCP servers over HTTP for agent consumption
- Sandbox containers (`internal/sandbox/`) follow the **Agent-in-Sandbox** pattern:
  - Agent runs inside a container with controlled API access via the local openkiro proxy
  - Preset configs: `DefaultConfig` (isolated), `AgentConfig` (bridge), `ClaudeCodeConfig`, `KiroConfig`
- `cmd/icm` provides in-context memory as an MCP server accessible to agents

## Commit Style

Use Conventional Commits: `feat:`, `fix:`, `test:`, `refactor:`, `docs:`, `ci:`, `chore:`

## Reference Docs

| Document | What it covers |
|----------|---------------|
| `AGENTS.md` | Cross-agent setup, commands, security rules (OpenAI Codex, Google Jules compatible) |
| `docs/ARCHITECTURE.md` | Full architecture: request lifecycle, binary protocol, middleware, security, CI/CD |
| `docs/CONTRIBUTING.md` | TDD workflow, middleware development guide |
| `.github/agents/go-coder.agent.md` | Specialized Copilot agent for Go code changes |
| `.github/agents/security-auditor.agent.md` | Specialized Copilot agent for security reviews |
| `.github/instructions/proxy.instructions.md` | Path-scoped rules for `internal/proxy/` and `internal/protocol/` |
