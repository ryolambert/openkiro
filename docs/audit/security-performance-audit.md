# Security, Performance, and Cross-Platform Packaging Audit

Date: 2026-03-23

## Scope

This report audits the `kirolink` proxy — a Go single-binary that reads Kiro auth tokens from the local filesystem, proxies Anthropic-compatible API requests, and translates them for the CodeWhisperer backend. The audit covers security, performance, Windows/macOS/Linux readiness, and the work needed to ship a production-grade package.

Commands: `read`, `refresh`, `export`, `claude`, `server`
Token source: `~/.aws/sso/cache/kiro-auth-token.json`
Core flow: local token → Anthropic-shaped request in → CodeWhisperer request out → translated response back

---

## 1) Windows Readiness

**Status: not production-ready for Kiro-IDE-only Windows users.**

The basic proxy flow works if the IDE populates the token cache, because `os.UserHomeDir` + `filepath.Join` is cross-platform (`kirolink.go:840-849`). But several paths break:

| Issue | Detail | Line refs |
|---|---|---|
| `refresh` requires external `sqlite3` binary | `exec.Command("sqlite3", ...)` — most Windows users won't have it | `kirolink.go:897-898` |
| `getKiroDBPath` missing Windows case | Falls through to Linux/XDG path `~/.local/share/kiro-cli/data.sqlite3` | `kirolink.go:876-887` |
| `claude` command assumes Unix-style config | Expects `~/.claude.json` | `kirolink.go:980-1015` |

### Edge cases to investigate

- What happens when `os.UserHomeDir` returns a path with spaces or non-ASCII characters on Windows? Go handles this, but downstream path concatenation and the `sqlite3` shell-out may not.
- Does the Kiro IDE on Windows write the token to the same relative path under `%USERPROFILE%`? No public documentation confirms this — needs empirical verification on a Windows install.
- Windows Defender / SmartScreen may block an unsigned binary from making outbound HTTPS calls. Signing is not optional for real distribution.
- WSL vs native: users running Kiro IDE natively but the proxy under WSL will have mismatched home directories. Document this or detect it.

### Research avenues

- Kiro IDE source or docs: confirm token cache path on each OS
- `os.UserConfigDir` / `os.UserCacheDir` behavior matrix across Go versions and OS editions
- Windows `LOCALAPPDATA` vs `APPDATA` conventions for credential caches
- Claude Code config file location on Windows (if `claude` command should work there)

---

## 2) Security Audit

### Critical findings

| Severity | Finding | Evidence | Risk |
|---|---|---|---|
| **High** | Listens on `0.0.0.0` by default | `http.ListenAndServe(":"+port, mux)` (`:1208`) | Any device on the LAN can hit a token-backed proxy |
| **High** | Tokens, prompts, and tool payloads logged to stdout | Full token (`:867-872`), partial token (`:932-933`), full request body (`:1106`), full upstream response (`:1724`), SSE events (`:1758-1759`) | Credential and prompt leakage in terminal scrollback, CI logs, screen shares |
| **High** | Unbounded `io.ReadAll(r.Body)` on inbound requests | `:1098` | Trivial OOM / DoS — single large POST kills the process |
| **Medium** | No HTTP server timeouts | Bare `http.ListenAndServe` (`:1208`) | Slowloris, connection exhaustion |
| **Medium** | No outbound client timeout | `&http.Client{}` (`:1574`, `:1706`) | Hung upstream stalls the goroutine forever |
| **Medium** | Panic text returned to clients | `"Internal panic: %v"` (`:1151`) | Stack traces / internal state leak |
| **Medium** | `sqlite3` shell-out for token refresh | `exec.Command("sqlite3", ...)` (`:897-898`) | Supply-chain risk, PATH injection on Windows, no structured error handling |
| **Low** | Hardcoded `us-east-1` endpoint | `:1560`, `:1583`, `:1692` | No failover, no testing against other regions |

### Positive notes

- Token file writes use `0600` permissions (`:927`).
- Zero external Go dependencies — minimal supply-chain surface.

### Edge cases to investigate

- **Token race condition**: if the IDE refreshes the token file while the proxy is mid-read, is there a partial-read risk? `os.ReadFile` is not atomic on all filesystems. Consider advisory locking or read-retry.
- **Concurrent request memory**: with no body size cap and multiple simultaneous large requests (images, long contexts), memory can spike multiplicatively. Profile under realistic multi-request load.
- **TLS certificate validation**: the outbound `http.Client` uses default TLS settings — good. But confirm no code path disables certificate verification (e.g., `InsecureSkipVerify`). A quick grep found none, but this should be a CI-enforced invariant.
- **CORS / origin validation**: if any browser-based client hits this proxy, there are no CORS headers or origin checks. This may matter if an MCP client or web UI is the consumer.
- **Signal handling**: no graceful shutdown. In-flight streaming responses will be severed on `SIGTERM`. This matters for systemd/launchd service deployment.

### Research avenues

- [OWASP Go Secure Coding Practices](https://owasp.org/www-project-go-secure-coding-practices-guide/) — timeout, body-limit, and error-handling patterns
- Go `net/http` server hardening: `ReadHeaderTimeout` specifically prevents Slowloris; many guides miss `IdleTimeout`
- AWS SigV4 request signing — confirm the proxy's auth header construction matches current AWS SDK behavior, especially around token expiry edge cases
- `http.MaxBytesReader` behavior when the limit is hit mid-stream (does it close the connection cleanly?)

---

## 3) Performance Audit

| Priority | Finding | Evidence | Impact |
|---|---|---|---|
| **High** | Streaming path buffers entire upstream response before emitting | `io.ReadAll(resp.Body)` in `handleStreamRequest` (`:1647-1656`) | Defeats the purpose of streaming; adds latency proportional to full response size |
| **Medium** | Hot-path debug logging | Request/response/event printing (`:1106`, `:1724`, `:1758-1759`) | Throughput loss, log volume, secret leakage |
| **Medium** | All request/response bodies fully buffered in memory | `io.ReadAll` on inbound (`:1098`), outbound (`:1648`, `:1717`) | Memory pressure under concurrent use |
| **Medium** | `/v1/models` output order is non-deterministic | Built from `ModelMap` iteration (`:1171-1185`) | Flaky tests, inconsistent client behavior |
| **Low** | `build.bat` is Windows-only and requires UPX | `upx --best --lzma` | Not reproducible cross-platform |

### Edge cases to investigate

- **SSE parsing under packet fragmentation**: the `protocol/sse_parser.go` reads SSE events — does it handle events split across TCP reads? Partial `data:` lines? This is the most common real-world streaming bug.
- **Upstream connection reuse**: a new `http.Client` is created per request path. Connection pooling is lost. Under sustained use, this means repeated TLS handshakes to the CodeWhisperer endpoint.
- **Large tool-use responses**: Claude tool-use responses can contain base64-encoded images or large JSON blobs. The full-buffer approach means a single 50MB tool response consumes 50MB+ of heap (original + parsed copy).
- **Timeout interaction with streaming**: if server-side `WriteTimeout` is set, long-running SSE streams will be killed mid-response. Streaming endpoints need either no write timeout or a per-flush deadline reset.

### Research avenues

- Go `net/http` streaming patterns: `http.Flusher` interface, `Transfer-Encoding: chunked`, and how to forward SSE without buffering
- Anthropic streaming API spec — confirm SSE event format, especially `event:` vs bare `data:` lines, and how `[DONE]` is signaled
- `bufio.Scanner` vs custom SSE parser tradeoffs for incremental event forwarding
- Connection pooling: `http.Transport` `MaxIdleConnsPerHost` tuning for single-upstream proxies

---

## 4) Best Practices Summary

### Local proxy security

- Bind `127.0.0.1` by default. Non-local bind requires explicit opt-in flag.
- Never log credentials or prompt content at default log level.
- Cap inbound body size. For this use case, 200MB is generous and still prevents unbounded allocation.
- Set all four server timeouts. For streaming, use `ReadHeaderTimeout` + `IdleTimeout` but leave `WriteTimeout` at 0 for SSE endpoints.

### Cross-platform filesystem

- Use `os.UserConfigDir`, `os.UserCacheDir`, `os.UserHomeDir` — not hardcoded paths.
- Windows: `LOCALAPPDATA` / `APPDATA`. macOS: `~/Library/Application Support`. Linux: XDG.
- Replace `sqlite3` shell-out with `modernc.org/sqlite` (pure Go, no CGO) or `crawshaw.io/sqlite`.

### Packaging

- GoReleaser for cross-compilation and artifact generation.
- Homebrew tap, Chocolatey package, `.deb`/`.rpm` for Linux.
- Embed version via `-ldflags` at build time. Expose via `--version`.
- Sign all release artifacts. Generate SHA256 checksums.

---

## 5) Project Plan

### Phase 0 — Stabilize (security baseline)

1. Bind to `127.0.0.1` by default.
2. Add `http.MaxBytesReader` with a generous limit (200MB) — accounts for images, media, large contexts. Add explicit `http.Server` with timeouts tuned for mixed streaming/non-streaming use.
3. Strip all default sensitive logging. Add `--debug` / `KIROLINK_DEBUG=1` for opt-in verbose output with token redaction.
4. Replace panic text in HTTP responses with generic `500 Internal Server Error` + correlation ID.
5. Add graceful shutdown on `SIGINT`/`SIGTERM`.

### Phase 1 — Cross-platform token handling

1. Replace `sqlite3` shell-out with a pure-Go SQLite driver.
2. Add Windows-correct paths in `getKiroDBPath` using `os.UserConfigDir` / `LOCALAPPDATA`.
3. Separate IDE token-cache path from CLI database path. Document both.
4. Add startup diagnostics: print which token source was found, which failed, and why.
5. Handle non-ASCII and spaced home directory paths on Windows.

### Phase 2 — UX and reliability

1. Add CLI flags: `--listen`, `--port`, `--upstream-url`, `--log-level`, `--version`.
2. Implement true incremental SSE streaming (read upstream event-by-event, flush to client immediately).
3. Reuse a single `http.Client` with connection pooling for the upstream endpoint.
4. Sort `/v1/models` output deterministically.
5. Add CORS headers if browser-based MCP clients are a target consumer.

### Phase 3 — Packaging and distribution

1. GoReleaser pipeline with cross-compilation matrix.
2. Homebrew tap (macOS), Chocolatey (Windows), `.deb`/`.rpm` (Linux).
3. SHA256 checksums + code signing for all artifacts.
4. CI: `go test ./...`, `go vet`, `govulncheck`, and cross-platform smoke tests.

### Phase 4 — Harden and maintain

1. Integration tests for streaming and non-streaming proxy paths.
2. Smoke tests for token discovery on Windows, macOS, and Linux.
3. README support matrix: which features work with Kiro IDE only / Kiro CLI only / Kiro + Claude Code.
4. Fuzz testing on SSE parser and request translation.
5. Periodic `govulncheck` in CI to catch dependency vulnerabilities if/when deps are added.

---

## Target Architecture

- **Binary**: single Go binary, no CGO (use pure-Go SQLite if needed).
- **CLI**: stdlib `flag` package unless command surface grows enough to justify `cobra`.
- **Config**: flags → env vars → optional config file (in that precedence).
- **Logging**: structured (JSON or `slog`), redacted by default, verbose behind `--debug`.
- **Transport**: single reusable `http.Client` with timeouts, configurable upstream base URL.
- **Storage**: prefer IDE token cache; if SQLite is required, access natively.

---

## Bottom Line

The proxy concept works. The blockers to production use are:

1. Sensitive data logged by default
2. Listens on all interfaces
3. No HTTP safety limits or timeouts
4. Windows token refresh is broken
5. No release/distribution pipeline

Go remains the right choice for a single-binary cross-platform proxy. The path from here to a shippable package is well-defined — the risk is in the edge cases around streaming fidelity, token lifecycle races, and Windows filesystem assumptions.
