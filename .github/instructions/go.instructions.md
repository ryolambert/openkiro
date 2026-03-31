---
applyTo: "**/*.go"
---
# Go Code Guidelines

## Style
- Format with `gofmt`; no manual formatting overrides
- Use `context.Context` as the first parameter for cancellable or timeout-aware functions
- Prefer named return values only when they improve doc clarity; avoid naked returns
- Keep line length reasonable (~100 chars); break long function signatures across lines

## Error Handling
- Always check returned errors; never use `_` to discard them in production code
- Wrap errors with context: `fmt.Errorf("functionName: %w", err)`
- Use `errors.Is()` and `errors.As()` for error inspection, not string matching
- Return early on error; keep the happy path un-indented

## Naming
- Package names: short, lowercase, singular (`proxy`, `token`, `middleware`)
- Interfaces: verb or -er suffix (`Middleware`, `Flusher`), not `IMiddleware`
- Constants: `PascalCase` for exported, `camelCase` for unexported
- Test functions: `TestFunctionName_scenario` (e.g., `TestResolveModelID_unknownAlias`)

## Patterns Used in This Project
- **Middleware interface**: implement `ProcessRequest`, `ProcessResponse`, `Name` (see `internal/middleware/middleware.go`)
- **Chain of Responsibility**: `middleware.Chain` composes middlewares in order
- **Adapter**: `proxy.BuildCodeWhispererRequest()` translates between API formats
- **Factory functions**: `sandbox.AgentConfig()`, `sandbox.ClaudeCodeConfig()`, etc.
- **Test seams**: package-level vars like `uuidEntropySource` for deterministic testing

## Logging
- Debug output: `token.DebugLogf()` — gated by `OPENKIRO_DEBUG` env var
- Errors/warnings only: `log.Printf()`
- Never log full tokens or credentials; use `token.RedactToken()` for partial display

## Dependencies
- Stdlib only — no third-party packages unless absolutely unavoidable
- Sole exception: `golang.org/x/sys` for Windows service support
