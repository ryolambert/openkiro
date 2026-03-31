# Copilot Instructions

**openkiro** — a zero-dependency Go CLI and HTTP proxy that translates Anthropic API requests into AWS CodeWhisperer calls using Kiro SSO authentication.

Build: `go build -o bin/openkiro ./cmd/openkiro` · Test: `go test -race -count=1 ./...` · Lint: `go vet ./...` then `golangci-lint run`

Key packages: `internal/proxy` (HTTP server, translation), `internal/token` (SSO auth), `internal/protocol` (binary frame parser), `internal/middleware` (composable chain), `internal/headroom` (Python compression), `internal/sandbox` (Docker containers), `internal/gateway` (MCP discovery).

## Security Critical Issues

- Never log full tokens — use `token.RedactToken()`; exposes only first 8 + last 4 chars
- Server must bind `127.0.0.1` by default; warn and require explicit flag for any other address
- Request bodies must be capped via `http.MaxBytesReader` (200 MiB inbound limit)
- Panic recovery must return generic JSON — never stack traces or internal details
- Credentials must be written with `0600` permissions (`~/.openkiro/credentials.json`)
- Docker images must never contain baked-in tokens or API keys — inject via env at runtime
- Never add third-party dependencies — stdlib only (`golang.org/x/sys` is the sole exception)

## Code Quality

- Always check returned errors; never use `_` to discard errors in production code
- Wrap errors with context: `fmt.Errorf("functionName: %w", err)`
- Use `errors.Is()` / `errors.As()` for inspection — never string-match errors
- All exported types and functions must have Go doc comments
- All new proxy features must be implemented as middlewares — never modify `server.go` directly
- Use `token.DebugLogf()` for debug output (gated by `OPENKIRO_DEBUG=1`), not `log.Printf()`
- New middleware: implement `ProcessRequest`, `ProcessResponse`, `Name`; register in server setup

## Testing Standards

- Follow TDD: write failing test first, then implement, then refactor
- Use table-driven tests with `t.Run(name, ...)` and descriptive subtest names
- Always run with `-race` flag — tests that pass without it but fail with it are bugs
- Use `testutil.SetupTestServer(t)` for mocks; `testutil.LoadTestData(t, "file.json")` for fixtures
- CI minimum coverage: 45% (enforced in `.github/workflows/ci.yml`)
- Place `_test.go` files in the same package as the code under test

## Commit Style

Use Conventional Commits: `feat:`, `fix:`, `test:`, `refactor:`, `docs:`, `ci:`, `chore:`

See `AGENTS.md` for cross-agent setup, `docs/ARCHITECTURE.md` for full technical reference.
