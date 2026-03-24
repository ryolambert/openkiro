# Task Breakdown: openkiro v1.0

Ref: [PRD.md](../PRD.md) | [Architecture Diagrams](architecture-diagrams.md)

---

## Phase 1: Rename (completed)

> Prerequisite: None. This phase must complete before all others.

### Task 1.1 — Rename source file and fix import paths

**Scope**: Mechanical rename, no logic changes.

**Changes**:
- `openkiro.go` (renamed from original)
- Import path: `"github.com/ryolambert/openkiro/protocol"`
- Same import fix in `response_translation_test.go`

**Acceptance Criteria**:
- [ ] `go build ./...` succeeds
- [ ] `go test ./...` passes
- [ ] `go vet ./...` clean
- [ ] Source file renamed to `openkiro.go`
- [ ] No old import paths remain in source

---

### Task 1.2 — Rename all user-facing strings

**Scope**: CLI output, usage text, comments, config keys.

**Changes in `openkiro.go`**:
- `main()` usage strings updated to `openkiro`
- Author URL: `github.com/ryolambert/openkiro`
- Default port: `"1234"`
- `exportEnvVars()`: uses `localhost:1234`
- `setClaude()`: sets `jsonData["openkiro"]` and removes legacy keys
- Legacy key cleanup handles `kiro2cc` and prior config keys
- `OPENKIRO_DEBUG` in `debugLoggingEnabled()` with legacy fallback
- Legacy env var fallback with deprecation warning
- Comment: `// openkiro extensions`

**Acceptance Criteria**:
- [ ] No old name references in source (except legacy key migration)
- [ ] `openkiro server` defaults to port 1234
- [ ] `openkiro export` prints `localhost:1234`
- [ ] `openkiro claude` sets `"openkiro": true` and removes legacy keys
- [ ] `OPENKIRO_DEBUG=1 openkiro server` enables debug logging
- [ ] Legacy debug env var still works with deprecation warning

---

### Task 1.3 — Update test files

**Scope**: All `*_test.go` files.

**Changes**:
- `phase0_test.go`: uses `OPENKIRO_DEBUG` and port `1234` with legacy fallback tests
- `main_test.go`: asserts `openkiro` key set and legacy keys removed
- All test files: verify no old references remain

**Acceptance Criteria**:
- [ ] `go test ./...` passes
- [ ] No old references in test files (except legacy migration tests)

---

### Task 1.4 — Update build.bat

**Changes**:
- `openkiro.exe` output name
- `openkiro.go` (renamed from original)

**Acceptance Criteria**:
- [ ] `build.bat` references only `openkiro`

---

### Task 1.5 — Update documentation

**Changes**:
- `README.md`: full rewrite of name, URLs, port references, commands, badges, credits
- `CLAUDE.md`: update all references
- `docs/security-performance-audit.md`: update references (or mark as historical)

**Acceptance Criteria**:
- [ ] No old references remain in documentation
- [ ] README install instructions show `go install github.com/ryolambert/openkiro@latest`
- [ ] All example commands use `openkiro` and port `1234`

---

## Phase 2: Security Hardening

> Prerequisite: Phase 1 complete. Source: [security-performance-audit.md](security-performance-audit.md) §2
> Many of these are already partially implemented (127.0.0.1 binding, MaxBytesReader, server timeouts, panic hiding, debug gate). This phase verifies completeness and closes remaining gaps.

### Task 2.1 — Verify and harden server binding + timeouts

**Scope**: Confirm existing Phase 0 audit fixes are complete; close gaps.

**Verify existing**:
- Server already binds `127.0.0.1` via `defaultListenAddress` constant
- `newHTTPServer()` already sets `ReadTimeout`, `WriteTimeout`, `IdleTimeout`, `ReadHeaderTimeout`
- `http.MaxBytesReader` already wraps inbound body (1MB limit)
- `handlePanic()` already returns generic error

**Remaining changes**:
- Add `--listen` flag to allow explicit non-local bind (e.g., `--listen 0.0.0.0`)
- Increase `MaxBytesReader` limit from 1MB to 200MB (current 1MB is too small for image/media payloads)
- For streaming endpoints: set `WriteTimeout` to 0 or implement per-flush deadline reset so long SSE streams aren't killed
- Add outbound `http.Client` timeout: 60s default via `upstreamHTTPClient()` (verify this exists)

**Acceptance Criteria**:
- [ ] `openkiro server` binds 127.0.0.1 only (existing — verify)
- [ ] `openkiro server --listen 0.0.0.0` binds all interfaces with warning printed
- [ ] Request body up to 200MB accepted; 201MB rejected with 413
- [ ] Streaming responses not killed by WriteTimeout after 60s
- [ ] Outbound requests to CodeWhisperer have 60s timeout
- [ ] Panic in handler returns generic JSON error, no stack trace (existing — verify)
- [ ] `grep -r 'InsecureSkipVerify' *.go` returns nothing (CI check)

---

### Task 2.2 — Strip sensitive logging

**Scope**: Ensure no tokens, prompts, or request/response bodies are logged at default level.

**Audit findings**: Full token printed in `readToken()`, partial token in `refreshToken()`, full request body logged, full upstream response logged, SSE events logged.

**Changes**:
- Audit every `fmt.Printf` and `log.Printf` in `openkiro.go` for sensitive data
- `readToken()`: redact token display (show first 8 + `...` + last 4 chars)
- `refreshToken()`: same redaction
- All request/response body logging: move behind `debugLoggingEnabled()` gate
- `debugLogBodySummary()` already exists — verify it's used everywhere instead of raw body logging
- SSE event logging: move behind debug gate

**Acceptance Criteria**:
- [ ] `openkiro server` with default settings: no tokens, prompts, or bodies in log output
- [ ] `OPENKIRO_DEBUG=1 openkiro server`: verbose output with token values redacted (first 8 + last 4)
- [ ] `openkiro read`: still shows full token (user explicitly asked for it)
- [ ] No `fmt.Printf` or `log.Printf` on hot path that includes `AccessToken`, `Body`, or request content without debug gate

---

### Task 2.3 — Replace sqlite3 shell-out

**Scope**: Eliminate `exec.Command("sqlite3", ...)` in `refreshToken()`.

**Options** (pick one):
1. `modernc.org/sqlite` — pure Go, no CGO, adds ~5MB to binary
2. Direct HTTP token refresh endpoint (if Kiro auth service supports it)
3. Read SQLite file with a minimal binary parser (fragile, not recommended)

**Recommended**: Option 1 (`modernc.org/sqlite`) — it's the standard pure-Go SQLite. Build-tag it so it's only compiled when the `refresh` command is used.

**Changes**:
- Replace `exec.Command("sqlite3", dbPath, "SELECT ...")` with `database/sql` + `modernc.org/sqlite` driver
- Add Windows case to `getKiroDBPath()`: `os.UserConfigDir()` + `openkiro/data.sqlite3` or `LOCALAPPDATA\kiro-cli\data.sqlite3`
- Handle non-ASCII paths (use `filepath.Join` consistently, no string concatenation)

**Acceptance Criteria**:
- [ ] `openkiro refresh` works on macOS without external `sqlite3` binary
- [ ] `openkiro refresh` works on Windows without external `sqlite3` binary
- [ ] `getKiroDBPath()` returns correct path on macOS, Windows, and Linux
- [ ] Path with spaces (`C:\Users\John Doe\...`) works correctly
- [ ] `grep -r 'exec.Command.*sqlite3' *.go` returns nothing

---

### Task 2.4 — Token read-retry for race condition

**Scope**: Handle partial-read when IDE refreshes token file concurrently.

**Changes**:
- In `getToken()`: if JSON parse fails, wait 100ms and retry once
- Log warning on retry (behind debug gate)

**Acceptance Criteria**:
- [ ] Simulated partial-read (truncated JSON file) → retry succeeds after file is complete
- [ ] Max 1 retry, then return error as before
- [ ] No user-visible change in normal operation

---

## Phase 3: Performance Improvements

> Prerequisite: Phase 2 complete. Source: [security-performance-audit.md](security-performance-audit.md) §3

### Task 3.1 — Incremental SSE streaming

**Scope**: Stop buffering entire upstream response before emitting to client.

**Current behavior**: `handleStreamRequest()` calls `io.ReadAll(resp.Body)` then parses all frames, then writes all SSE events. This defeats streaming — client sees nothing until the full response is received.

**Changes**:
- Read upstream binary frames incrementally (frame header → payload → emit)
- Use `http.Flusher` to flush each SSE event to the client immediately
- Handle partial frames across TCP reads (frame split at arbitrary byte boundary)
- Remove `io.ReadAll` from streaming path

**Acceptance Criteria**:
- [ ] First SSE event reaches client within 200ms of upstream starting to respond (not after full response)
- [ ] `time curl -N http://localhost:1234/v1/messages` with streaming shows tokens appearing incrementally
- [ ] Large responses (>1MB) don't spike memory proportionally
- [ ] Partial frame at TCP boundary is reassembled correctly
- [ ] Existing streaming tests still pass

---

### Task 3.2 — Connection pooling for upstream

**Scope**: Reuse HTTP connections to CodeWhisperer.

**Current behavior**: `upstreamHTTPClient()` creates a new `http.Client` per call. Connection pooling is lost.

**Changes**:
- Create a package-level `var upstreamClient *http.Client` initialized once with configured `http.Transport`
- Set `MaxIdleConnsPerHost: 10` (single upstream, so this is effectively the pool size)
- Set `IdleConnTimeout: 90s`

**Acceptance Criteria**:
- [ ] Under 10 sequential requests, TCP connection count to CodeWhisperer stays at 1-2 (not 10)
- [ ] No TLS handshake per request after the first (verify via debug logging or `netstat`)
- [ ] Existing tests still pass

---

### Task 3.3 — Deterministic /v1/models output

**Scope**: Sort model list alphabetically.

**Changes**:
- Collect model IDs from `ModelMap`, sort with `sort.Strings`, then build response

**Acceptance Criteria**:
- [ ] `curl /v1/models` returns same order every time
- [ ] Order is alphabetical by model ID

---

## Phase 4: Port Configuration

> Prerequisite: Phase 1 complete.

### Task 4.1 — Add port resolution logic

**Scope**: New function `resolvePort()` implementing precedence chain.

**Changes**:
- Add `resolvePort(flagValue string) string` — checks flag → `OPENKIRO_PORT` env → `"1234"` default
- Wire into `main()` switch for `server` command
- Wire into `exportEnvVars()` so export uses resolved port
- Keep backward compat: `openkiro server 5678` positional arg still works

**Acceptance Criteria**:
- [ ] `openkiro server` → listens on :1234
- [ ] `openkiro server --port 5678` → listens on :5678
- [ ] `openkiro server 5678` → listens on :5678 (backward compat)
- [ ] `OPENKIRO_PORT=9999 openkiro server` → listens on :9999
- [ ] `OPENKIRO_PORT=9999 openkiro server --port 5678` → listens on :5678 (flag wins)
- [ ] `openkiro export --port 5678` → prints `localhost:5678`
- [ ] Unit test for `resolvePort()` covering all precedence cases

---

## Phase 5: Logging Infrastructure

> Prerequisite: Phase 1 complete.

### Task 5.1 — Platform-aware log paths

**Scope**: New functions for log directory resolution.

**Changes**:
- `logDir() string` — returns platform-appropriate log directory
- `pidFilePath() string` — returns platform-appropriate PID file path
- Auto-create log directory on first use
- When running in foreground (`server`): log to stderr (current behavior)
- When running in background (`start`): log to `openkiro.log` in log directory

**Acceptance Criteria**:
- [ ] macOS: logs to `~/Library/Logs/openkiro/openkiro.log`
- [ ] Windows: logs to `%LOCALAPPDATA%\openkiro\logs\openkiro.log`
- [ ] Linux: logs to `~/.local/state/openkiro/openkiro.log`
- [ ] Log directory created automatically if missing
- [ ] `openkiro server` still logs to stderr
- [ ] Unit tests for `logDir()` and `pidFilePath()` per platform (build-tagged)

---

## Phase 6: Background / Daemon Mode

> Prerequisite: Phase 4 + Phase 5 complete.

### Task 6.1 — PID file management

**Scope**: Shared logic for start/stop/status.

**Changes**:
- `writePID(pid int) error`
- `readPID() (int, error)`
- `removePID() error`
- `isRunning(pid int) bool` — checks if process is alive

**Acceptance Criteria**:
- [ ] PID file written on start, removed on stop
- [ ] `isRunning()` correctly detects live vs stale PID
- [ ] Stale PID file is cleaned up automatically
- [ ] Unit tests for all PID operations

---

### Task 6.2 — macOS launchd integration

**Scope**: `openkiro start/stop/status` on macOS.

**Changes**:
- Generate `com.openkiro.proxy.plist` with:
  - `ProgramArguments`: path to `openkiro` binary, `server`, `--port`, resolved port
  - `StandardOutPath` / `StandardErrorPath`: log file path
  - `KeepAlive`: true (auto-restart on crash)
  - `RunAtLoad`: false (only when explicitly loaded)
- `openkiro start`: write plist → `launchctl load`
- `openkiro stop`: `launchctl unload` → remove plist → remove PID
- `openkiro status`: check launchctl list for the service label

**Acceptance Criteria**:
- [ ] `openkiro start` → proxy running in background, no terminal needed
- [ ] `openkiro status` → shows "running" with PID and port
- [ ] `openkiro stop` → proxy stopped, plist unloaded
- [ ] `openkiro start` when already running → clear error message
- [ ] Proxy auto-restarts after crash (kill -9 the process, verify it comes back)
- [ ] Logs appear in `~/Library/Logs/openkiro/openkiro.log`
- [ ] `curl http://localhost:1234/health` returns OK after `openkiro start`

---

### Task 6.3 — Windows Service integration

**Scope**: `openkiro start/stop/status` on Windows.

**Changes**:
- Add `golang.org/x/sys/windows/svc` dependency (build-tagged `//go:build windows`)
- Detect service mode via `svc.IsWindowsService()`
- `openkiro start`: register + start Windows Service
- `openkiro stop`: stop + deregister service
- `openkiro status`: query service status via SCM
- Service logs to `%LOCALAPPDATA%\openkiro\logs\openkiro.log`

**Acceptance Criteria**:
- [ ] `openkiro start` (as admin) → service registered and running
- [ ] `openkiro status` → shows "running" with port
- [ ] `openkiro stop` → service stopped and deregistered
- [ ] Service survives user logoff (runs as background service)
- [ ] Logs appear in `%LOCALAPPDATA%\openkiro\logs\`
- [ ] `curl http://localhost:1234/health` returns OK after start
- [ ] Non-admin `openkiro start` → clear error about needing elevation

---

### Task 6.4 — Graceful shutdown

**Scope**: Signal handling for clean exit.

**Changes**:
- `SIGINT`/`SIGTERM` handler in `startServer()`
- `http.Server.Shutdown(ctx)` with 10s timeout
- Drain in-flight requests before exit
- Remove PID file on shutdown

**Acceptance Criteria**:
- [ ] `Ctrl+C` during `openkiro server` → clean shutdown, no error
- [ ] `openkiro stop` → in-flight streaming request completes before exit (up to 10s)
- [ ] PID file removed after shutdown
- [ ] Exit code 0 on clean shutdown

---

## Phase 7: Install Scripts

> Prerequisite: Phase 1 complete (scripts reference `openkiro`).

### Task 7.1 — macOS/Linux install script (`scripts/install.sh`)

**Scope**: Bash script for macOS and Linux.

**Logic**:
1. Check for Go (`go version`), install via Homebrew if missing
2. Verify Go >= 1.23
3. `go install github.com/ryolambert/openkiro@latest`
4. Detect shell (zsh/bash) from `$SHELL`
5. Check if `$(go env GOPATH)/bin` is in PATH
6. If not, append `export PATH="$PATH:$(go env GOPATH)/bin"` to appropriate rc file
7. Verify `openkiro version` works
8. Print next steps

**Acceptance Criteria**:
- [ ] Works on clean macOS with zsh (default shell)
- [ ] Works on macOS with bash
- [ ] Works on Linux with bash
- [ ] Idempotent: running twice doesn't duplicate PATH entries
- [ ] Doesn't modify rc files if GOPATH/bin already in PATH
- [ ] Prints clear error if Homebrew install fails
- [ ] `shellcheck install.sh` passes with no errors

---

### Task 7.2 — Windows install script (`scripts/install.ps1`)

**Scope**: PowerShell script for Windows.

**Logic**:
1. Check for Go (`go version`), install via `winget install GoLang.Go` if missing
2. Verify Go >= 1.23
3. `go install github.com/ryolambert/openkiro@latest`
4. Check if `$(go env GOPATH)\bin` is in user PATH
5. If not, add via `[Environment]::SetEnvironmentVariable`
6. Verify `openkiro version` works
7. Print next steps

**Acceptance Criteria**:
- [ ] Works on Windows 10/11 with PowerShell 5.1+
- [ ] Works in Windows Terminal
- [ ] Idempotent: running twice doesn't duplicate PATH entries
- [ ] Handles spaces in GOPATH gracefully
- [ ] Prints clear error if winget not available (suggests manual Go install)
- [ ] No admin required for `go install` + PATH modification (user-level PATH)

---

## Phase 8: Version Command

> Prerequisite: Phase 1 complete.

### Task 8.1 — Add `openkiro version`

**Scope**: Embed version at build time via `-ldflags`.

**Changes**:
- Add `var version = "dev"` in `openkiro.go`
- Add `version` case to `main()` switch
- Update `build.bat` to pass `-ldflags "-X main.version=..."` 
- Document in README: `go install` builds will show `dev`; tagged releases show semver

**Acceptance Criteria**:
- [ ] `openkiro version` prints version string
- [ ] Default is `"dev"` for local builds
- [ ] `-ldflags "-X main.version=v1.0.0"` overrides at build time

---

## Execution Order

```
Phase 1 (Rename) ──────────────────────────────┐
  ├── Task 1.1 (source + imports)               │
  ├── Task 1.2 (user-facing strings)            │
  ├── Task 1.3 (tests)                          │
  ├── Task 1.4 (build.bat)                      │
  └── Task 1.5 (docs)                           │
                                                │
Phase 2 (Security Hardening) ──────────────────┤ depends on 1
  ├── Task 2.1 (server binding + timeouts)      │
  ├── Task 2.2 (strip sensitive logging)        │
  ├── Task 2.3 (replace sqlite3 shell-out)      │
  └── Task 2.4 (token read-retry)               │
                                                │
Phase 3 (Performance) ─────────────────────────┤ depends on 1
  ├── Task 3.1 (incremental SSE streaming)      │
  ├── Task 3.2 (connection pooling)             │
  └── Task 3.3 (deterministic /v1/models)       │
                                                │
Phase 4 (Port Config) ─────────────────────────┤ can parallel with 2,3
                                                │
Phase 5 (Logging) ─────────────────────────────┤ can parallel with 2,3,4
                                                │
Phase 6 (Daemon) ──────────────────────────────┤ depends on 4+5
  ├── Task 6.1 (PID management)                 │
  ├── Task 6.2 (macOS launchd)                  │
  ├── Task 6.3 (Windows Service)                │
  └── Task 6.4 (Graceful shutdown)              │
                                                │
Phase 7 (Install Scripts) ─────────────────────┤ depends on 1
  ├── Task 7.1 (install.sh)                     │
  └── Task 7.2 (install.ps1)                    │
                                                │
Phase 8 (Version) ─────────────────────────────┘ can parallel with anything after 1
```

**Total**: 21 tasks across 8 phases.
**Critical path**: Phase 1 → Phase 2+3 (parallel, security/perf) → Phase 4+5 (parallel) → Phase 6 → verify.
**Independent**: Phase 7, Phase 8 can run in parallel with anything after Phase 1.

### Audit Coverage Matrix

Maps every finding from [security-performance-audit.md](security-performance-audit.md) to a task:

| Audit Finding | Severity | Task | Status |
|---|---|---|---|
| Listens on 0.0.0.0 | High | 2.1 | Already fixed (verify) |
| Tokens/prompts logged | High | 2.2 | Partially fixed (close gaps) |
| Unbounded io.ReadAll | High | 2.1 | Already fixed at 1MB (increase to 200MB) |
| No server timeouts | Medium | 2.1 | Already fixed (verify) |
| No outbound client timeout | Medium | 2.1 | Already fixed (verify) |
| Panic text to clients | Medium | 2.1 | Already fixed (verify) |
| sqlite3 shell-out | Medium | 2.3 | New work |
| Hardcoded us-east-1 | Low | Backlog | Not in scope for v1.0 |
| Streaming buffers all | High | 3.1 | New work |
| Hot-path debug logging | Medium | 2.2 + P1.4 | Partially fixed |
| Bodies fully buffered | Medium | 3.1 | New work |
| Non-deterministic /v1/models | Medium | 3.3 | New work |
| build.bat Windows-only | Low | 1.4 | Addressed by rename |
| Windows getKiroDBPath | — | 2.3 | New work |
| No graceful shutdown | — | 6.4 | New work |
| Token race condition | — | 2.4 | New work |
