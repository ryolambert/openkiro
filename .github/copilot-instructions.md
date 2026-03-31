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

## Commit Style

Use Conventional Commits: `feat:`, `fix:`, `test:`, `refactor:`, `docs:`, `ci:`, `chore:`
