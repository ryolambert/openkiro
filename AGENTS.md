# AGENTS.md

> Universal agent instructions for openkiro. Read by GitHub Copilot coding agent,
> OpenAI Codex, Google Jules, and any other AI coding agent that supports the
> [AGENTS.md standard](https://agents.md/).

## Project Overview

**openkiro** is a zero-dependency Go CLI and HTTP proxy that translates Anthropic
API requests into AWS CodeWhisperer calls using Kiro SSO authentication. It is the
orchestration backbone for local AI agent development workflows.

```
AI Client (Claude Code, Cursor, etc.)
  └─▶ openkiro proxy :1234
        └─▶ AWS CodeWhisperer  codewhisperer.us-east-1.amazonaws.com
```

Token source: `~/.aws/sso/cache/kiro-auth-token.json`

---

## Setup Commands

```bash
# Build the main binary
go build -o bin/openkiro ./cmd/openkiro

# Build all binaries (headroom, rtk, icm, mcp-gateway written to working dir)
go build ./...

# Run all tests with race detection (required before every PR)
go test -race -count=1 ./...

# Vet
go vet ./...

# Lint (requires golangci-lint installed)
golangci-lint run

# All quality gates
make check

# Start proxy (default port 1234)
./bin/openkiro server

# Build Docker sandbox templates
make sandbox-claude
make sandbox-kiro
make sandbox-all
```

---

## Package Layout

| Package | Purpose |
|---------|---------|
| `cmd/openkiro` | CLI entry point — `server`, `sandbox`, `version` subcommands |
| `cmd/{headroom,rtk,icm,mcp-gateway}` | Companion tool binaries |
| `internal/proxy` | HTTP server, Anthropic ↔ CodeWhisperer translation, SSE streaming |
| `internal/token` | Kiro SSO token I/O, refresh, redaction, upstream HTTP client pool |
| `internal/protocol` | Binary frame parser: CW event-stream → Anthropic SSE events |
| `internal/middleware` | `Middleware` interface + `Chain` for composable interception |
| `internal/headroom` | Python headroom proxy lifecycle (install/start/stop) + HTTP client |
| `internal/daemon` | PID file management, launchd plist generation, shell alias output |
| `internal/sandbox` | Docker container create/start/stop/destroy/list + preset configs |
| `internal/gateway` | Docker MCP server discovery and routing |
| `internal/service` | Windows service stubs (build-tagged, non-Windows is a no-op stub) |
| `internal/testutil` | Shared test helpers: mock server, JSON compare, fixture loader |

---

## Coding Guidelines

### Errors
- Always check and propagate errors. Never discard with `_` in production code.
- Wrap with context: `fmt.Errorf("functionName: %w", err)`
- Use `errors.Is()` / `errors.As()` for inspection — never string matching.
- Return early on error; keep the happy path un-indented.

### Dependencies
- **stdlib only.** The sole external dependency is `golang.org/x/sys` (Windows
  service support). Do not add third-party packages without strong justification.

### Logging
- Debug output: `token.DebugLogf(...)` — gated by `OPENKIRO_DEBUG=1` env var.
- Errors/warnings only: `log.Printf(...)`.
- **Never** log full tokens or credentials. Use `token.RedactToken()` for partial display.

### Naming
- Package names: short, lowercase, singular (`proxy`, `token`, `middleware`).
- Interfaces: verb or `-er` suffix (`Middleware`, `Flusher`) — never `IFoo`.
- Test functions: `TestFunctionName_scenario` (e.g., `TestResolveModelID_unknownAlias`).

### Format
- `gofmt` always. No manual overrides.
- Line length ~100 chars; break long signatures across lines.
- `context.Context` is the first parameter for cancellable/timeout-aware functions.

---

## Testing Workflow

```bash
# Run all tests with race detection
go test -race -count=1 ./...

# Run a single package
go test -race -v ./internal/proxy/...

# Generate coverage report
go test -race -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### Rules
- **TDD cycle**: write a failing test first, then implement, then refactor.
- Use table-driven tests with `t.Run(name, ...)` and descriptive subtest names.
- Use `t.Fatalf` when a failure prevents further execution; `t.Errorf` otherwise.
- Place `_test.go` files in the same package as the source.
- Use `testutil.SetupTestServer(t)` for HTTP mock servers.
- Use `testutil.LoadTestData(t, "fixture.json")` for JSON fixtures (`internal/testutil/testdata/`).
- Use `testutil.AssertJSONEqual(t, expected, actual)` for deep JSON comparison.
- CI gate: **45% minimum coverage** (enforced in `.github/workflows/ci.yml`).
- Always run with `-race`. Tests that pass without `-race` but fail with it are bugs.

---

## Middleware Development

To add a new middleware:

1. Implement `middleware.Middleware` in `internal/middleware/<name>.go`:
   ```go
   type MyMiddleware struct { ... }
   func (m *MyMiddleware) ProcessRequest(req *proxy.AnthropicRequest) (*proxy.AnthropicRequest, error)
   func (m *MyMiddleware) ProcessResponse(resp []byte) ([]byte, error)
   func (m *MyMiddleware) Name() string { return "my-middleware" }
   ```
2. Write `internal/middleware/<name>_test.go` **before** the implementation.
3. Register via `chain.Add(myMiddleware)` in server setup.
4. Follow TDD: commit the test first (`test:` prefix), then implement (`feat:` prefix).

---

## Security Rules

- **NEVER** bake tokens or credentials into Docker images.
- **NEVER** log full token values. Use `token.RedactToken(t)` which exposes only the first 8 and last 4 characters.
- **NEVER** bind to `0.0.0.0` without an explicit override flag; default is `127.0.0.1`.
- **NEVER** return internal error details in HTTP responses; the panic recovery responds with `{"error":{"type":"server_error","message":"Internal server error"}}`.
- Credential files must be written with `0600` permissions (both Unix and Windows).
- Request bodies are capped at 200 MiB via `http.MaxBytesReader`.
- Server timeouts: Read 30s, Write 60s, Idle 120s, Header 10s.

---

## PR Guidelines

- Use Conventional Commits: `feat:`, `fix:`, `test:`, `refactor:`, `docs:`, `ci:`, `chore:`
- Every PR must pass all CI quality gates (build on 3 OSes, race test, vet, lint, coverage, vuln scan).
- Documentation changes live in `docs/` or `.github/`; update `docs/ARCHITECTURE.md` when changing public interfaces.
- Keep PRs focused: one logical change per PR.

---

## Key Reference Documents

| Document | Purpose |
|----------|---------|
| `docs/ARCHITECTURE.md` | Comprehensive technical reference (request lifecycle, streaming protocol, security, CI/CD) |
| `docs/CONTRIBUTING.md` | TDD workflow, middleware development guide |
| `docs/PRD.md` | Product requirements and phased delivery plan |
| `docs/docker-sandbox.md` | Docker sandbox setup and tool inventory |
| `docs/headroom.md` | Headroom compression proxy integration |
| `CLAUDE.md` | Claude Code-specific instructions (mirrors this file) |
| `.github/copilot-instructions.md` | Copilot repo-wide instructions |
| `.github/instructions/*.instructions.md` | Path-scoped Copilot instructions |
| `.github/agents/*.agent.md` | Specialized Copilot custom agent profiles |
