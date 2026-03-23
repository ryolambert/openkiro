# PRD: openkiro v1.0 — Rename, Packaging, and Daemon Mode

Date: 2026-03-23
Status: Draft
Author: Architecture Review

---

## 1. Problem Statement

The project was forked from `alexandephilia/kiro-claude-proxy` (aka `kirolink`). The codebase still references the old name, old author, old import paths, and uses port 8080 which conflicts with common dev tools. Users must manually build from source, keep a terminal open to run the proxy, and configure shell paths themselves.

**Goal**: Ship `openkiro` as a single `go install`-able binary with cross-platform install scripts, a non-conflicting default port, and the ability to run as a background service without a terminal window.

---

## 2. User Personas

| Persona | Description | Primary Need |
|---|---|---|
| Developer (macOS) | Uses Claude Code + Kiro IDE daily, zsh or bash | `go install` + one-liner setup, runs in background |
| Developer (Windows) | Uses Claude Code + Kiro IDE, PowerShell | Install script handles Go + PATH, runs as service |
| Power User | Wants custom port, debug logs, manual control | CLI flags, env var overrides, log access |

---

## 3. Requirements

### 3.1 Rename (kirolink → openkiro)

| ID | Requirement | Priority |
|---|---|---|
| R1.1 | Rename main source file: `kirolink.go` → `openkiro.go` | P0 |
| R1.2 | Fix import path: `github.com/alexandeism/kirolink/protocol` → `github.com/ryolambert/openkiro/protocol` | P0 |
| R1.3 | Replace all `kirolink` references in CLI output, usage strings, comments | P0 |
| R1.4 | Rename env var: `KIROLINK_DEBUG` → `OPENKIRO_DEBUG` | P0 |
| R1.5 | Rename claude.json config key: `"kirolink"` → `"openkiro"` | P0 |
| R1.6 | Add `"kirolink"` as a legacy key to clean up (alongside existing `kiro2cc`) | P0 |
| R1.7 | Update `build.bat` to produce `openkiro.exe` | P0 |
| R1.8 | Update all test files to reference new names/env vars | P0 |
| R1.9 | Update README.md, CLAUDE.md, docs/ with new name, new repo URL, new author | P0 |
| R1.10 | Remove/replace all `alexandeism`, `alexandephilia`, `kiro-claude-proxy` references | P0 |

### 3.2 `go install` Support

| ID | Requirement | Priority |
|---|---|---|
| R2.1 | `go install github.com/ryolambert/openkiro@latest` produces working `openkiro` binary | P0 |
| R2.2 | Module path in `go.mod` matches import paths in all `.go` files | P0 |
| R2.3 | Binary name is `openkiro` (derived from module path last segment) | P0 |
| R2.4 | Zero external Go dependencies (maintain current zero-dep stance) | P1 |

### 3.3 Install Scripts

| ID | Requirement | Priority |
|---|---|---|
| R3.1 | `install.sh` for macOS/Linux: detect Go, install via Homebrew if missing, run `go install`, add `GOPATH/bin` to PATH | P0 |
| R3.2 | Shell support: bash (`~/.bashrc`, `~/.bash_profile`) and zsh (`~/.zshrc`) | P0 |
| R3.3 | `install.ps1` for Windows: detect Go, install via `winget` if missing, run `go install`, add `GOPATH\bin` to user PATH | P0 |
| R3.4 | Both scripts are idempotent — safe to run multiple times | P0 |
| R3.5 | Scripts print clear success/failure messages with next steps | P1 |
| R3.6 | Scripts detect if `openkiro` is already installed and report version | P1 |

### 3.4 Port Configuration

| ID | Requirement | Priority |
|---|---|---|
| R4.1 | Default port changes from `8080` to `1234` | P0 |
| R4.2 | Port configurable via CLI: `openkiro server --port 5678` | P0 |
| R4.3 | Port configurable via env var: `OPENKIRO_PORT=5678` | P0 |
| R4.4 | CLI flag takes precedence over env var, env var over default | P0 |
| R4.5 | `export` command uses the configured port (not hardcoded) | P0 |
| R4.6 | Positional arg `openkiro server 5678` still works for backward compat | P1 |

### 3.5 Background / Daemon Mode

| ID | Requirement | Priority |
|---|---|---|
| R5.1 | `openkiro start` — starts the proxy in the background (no terminal needed) | P0 |
| R5.2 | `openkiro stop` — stops the background proxy | P0 |
| R5.3 | `openkiro status` — reports whether the proxy is running, PID, port | P0 |
| R5.4 | macOS: uses launchd plist in `~/Library/LaunchAgents/com.openkiro.proxy.plist` | P0 |
| R5.5 | Windows: registers as a Windows Service via `sc.exe` or `golang.org/x/sys/windows/svc` | P0 |
| R5.6 | Graceful shutdown on SIGINT/SIGTERM (macOS) and service stop (Windows) | P0 |
| R5.7 | PID file at platform-appropriate location (see §4.2) | P0 |
| R5.8 | Auto-restart on crash (launchd `KeepAlive`, Windows service recovery) | P1 |
| R5.9 | `openkiro start` fails clearly if already running | P1 |

### 3.6 Logging

| ID | Requirement | Priority |
|---|---|---|
| R6.1 | Default log location — macOS: `~/Library/Logs/openkiro/openkiro.log` | P0 |
| R6.2 | Default log location — Windows: `%LOCALAPPDATA%\openkiro\logs\openkiro.log` | P0 |
| R6.3 | Default log location — Linux: `~/.local/state/openkiro/openkiro.log` (XDG_STATE_HOME) | P1 |
| R6.4 | Log directory auto-created on first run | P0 |
| R6.5 | Foreground mode (`openkiro server`) logs to stderr as today | P0 |
| R6.6 | Background mode (`openkiro start`) logs to file | P0 |
| R6.7 | `OPENKIRO_DEBUG=1` enables verbose logging (replaces `KIROLINK_DEBUG`) | P0 |
| R6.8 | Log rotation: max 10MB per file, keep 3 rotated files | P1 |

---

## 4. Architecture Decisions

### 4.1 Project Structure

```
openkiro/
├── openkiro.go          # main package (renamed from kirolink.go)
├── *_test.go            # all test files
├── protocol/
│   ├── sse_parser.go
│   └── sse_parser_test.go
├── scripts/
│   ├── install.sh       # macOS/Linux installer
│   └── install.ps1      # Windows installer
├── service/
│   ├── launchd.go       # macOS launchd plist generation + management
│   ├── windows.go       # Windows service registration
│   └── daemon.go        # shared start/stop/status logic
├── go.mod
├── go.sum               # if deps added for windows/svc
├── README.md
├── CLAUDE.md
├── PRD.md
└── docs/
```

**Decision**: Keep `package main` in root (no `cmd/` directory). This is the simplest structure for `go install` and matches the current layout. The binary name `openkiro` is derived from the module path's last segment.

**Decision**: The `service/` package is internal — it handles platform-specific daemon lifecycle. If `golang.org/x/sys/windows/svc` is needed, it's the only new dependency and it's build-tagged to `windows` only.

### 4.2 Platform Paths

| Purpose | macOS | Windows | Linux |
|---|---|---|---|
| Logs | `~/Library/Logs/openkiro/` | `%LOCALAPPDATA%\openkiro\logs\` | `$XDG_STATE_HOME/openkiro/` or `~/.local/state/openkiro/` |
| PID file | `~/Library/Application Support/openkiro/openkiro.pid` | `%LOCALAPPDATA%\openkiro\openkiro.pid` | `$XDG_RUNTIME_DIR/openkiro.pid` or `/tmp/openkiro.pid` |
| launchd plist | `~/Library/LaunchAgents/com.openkiro.proxy.plist` | N/A | N/A |
| Config (future) | `~/Library/Application Support/openkiro/` | `%LOCALAPPDATA%\openkiro\` | `$XDG_CONFIG_HOME/openkiro/` |

**Rationale**: These follow OS conventions. macOS `~/Library/Logs/` is where Console.app looks. Windows `LOCALAPPDATA` is per-user, non-roaming. Linux follows XDG Base Directory spec.

### 4.3 Port Precedence

```
CLI flag (--port) > ENV (OPENKIRO_PORT) > Default (1234)
```

The `export` command must read the same precedence chain so `ANTHROPIC_BASE_URL` matches the actual server port.

### 4.4 Command Surface (Post-Rename)

| Command | Description |
|---|---|
| `openkiro server [--port N]` | Start proxy in foreground (logs to stderr) |
| `openkiro start [--port N]` | Start proxy as background service |
| `openkiro stop` | Stop background service |
| `openkiro status` | Show running state, PID, port |
| `openkiro read` | Print token info |
| `openkiro refresh` | Sync token from Kiro CLI DB |
| `openkiro export [--port N]` | Print env var setup commands |
| `openkiro claude` | Update Claude Code config |
| `openkiro version` | Print version string |

### 4.5 Backward Compatibility

| Old | New | Migration |
|---|---|---|
| `kirolink` binary | `openkiro` binary | Install script replaces; old binary is orphaned |
| Port 8080 | Port 1234 | Breaking change — documented in README |
| `KIROLINK_DEBUG` | `OPENKIRO_DEBUG` | Code checks both, prefers new, warns on old |
| `"kirolink"` in claude.json | `"openkiro"` in claude.json | `claude` command migrates: sets new key, deletes old |
| `"kiro2cc"` in claude.json | deleted | Already handled by `legacyClaudeConfigKey()` |

### 4.6 Windows Service Approach

**Decision**: Use `golang.org/x/sys/windows/svc` behind a `//go:build windows` tag. This is the standard Go approach and avoids shelling out to `sc.exe` with string interpolation (security risk). The service binary is the same `openkiro.exe` — it detects whether it's running as a service or CLI via `svc.IsWindowsService()`.

**Alternative considered**: `sc.exe create` — simpler but fragile, requires admin, and doesn't integrate with Go's signal handling.

### 4.7 Zero-Dep Stance

Current codebase has zero external Go dependencies. Adding `golang.org/x/sys` is acceptable because:
- It's maintained by the Go team
- It's only compiled on Windows builds
- It's required for proper Windows service integration
- No alternative exists in stdlib

---

## 5. Security & Performance Requirements

> Source: [docs/security-performance-audit.md](docs/security-performance-audit.md)

### 5.1 Security Hardening

| ID | Requirement | Audit Ref | Priority |
|---|---|---|---|
| S1.1 | Bind to `127.0.0.1` by default; non-local bind requires explicit `--listen 0.0.0.0` flag | High: listens on 0.0.0.0 | P0 |
| S1.2 | Strip all sensitive logging (tokens, prompts, request/response bodies) at default log level. Only emit behind `OPENKIRO_DEBUG=1` with token values redacted (first 8 + last 4 chars only) | High: tokens/prompts logged to stdout | P0 |
| S1.3 | Cap inbound request body with `http.MaxBytesReader` — 200MB limit (accounts for images, large contexts) | High: unbounded io.ReadAll | P0 |
| S1.4 | Set all four HTTP server timeouts: `ReadTimeout`, `WriteTimeout`, `IdleTimeout`, `ReadHeaderTimeout`. For SSE streaming endpoints, `WriteTimeout` must be 0 or use per-flush deadline reset | Medium: no server timeouts (Slowloris) | P0 |
| S1.5 | Set outbound `http.Client` timeout (60s for non-streaming, 0 for streaming with context cancellation) | Medium: no outbound client timeout | P0 |
| S1.6 | Replace panic text in HTTP responses with generic `{"error":{"type":"server_error","message":"Internal server error"}}` — no stack traces, no recovered values | Medium: panic text returned to clients | P0 |
| S1.7 | Replace `sqlite3` shell-out with pure-Go SQLite (`modernc.org/sqlite`) or eliminate the dependency entirely. Shell-out is a PATH injection risk on Windows | Medium: sqlite3 shell-out | P1 |
| S1.8 | Add `--listen` flag for explicit non-local bind opt-in (required for LAN access) | Defense in depth | P1 |
| S1.9 | Ensure no code path sets `InsecureSkipVerify: true` on outbound TLS. Add CI check (`grep -r InsecureSkipVerify`) | TLS validation | P1 |
| S1.10 | Token file reads: implement read-retry (re-read on JSON parse failure) to handle partial-read race with IDE token refresh | Edge case: token race condition | P2 |
| S1.11 | Add CORS headers / origin validation if browser-based MCP clients are a target consumer | Edge case: CORS | P2 |

### 5.2 Performance Improvements

| ID | Requirement | Audit Ref | Priority |
|---|---|---|---|
| P1.1 | Implement true incremental SSE streaming: read upstream binary frames event-by-event, flush to client immediately via `http.Flusher`. Do not buffer entire upstream response | High: streaming buffers entire response | P0 |
| P1.2 | Reuse a single `http.Client` with connection pooling (`http.Transport` with `MaxIdleConnsPerHost`) for the upstream CodeWhisperer endpoint. Eliminate per-request client creation | Medium: no connection reuse | P1 |
| P1.3 | Sort `/v1/models` output deterministically (alphabetical by model ID) | Medium: non-deterministic map iteration | P1 |
| P1.4 | Move all debug logging behind `OPENKIRO_DEBUG` gate — zero allocation on hot path when disabled | Medium: hot-path debug logging | P0 |
| P1.5 | Handle SSE events split across TCP reads in `protocol/sse_parser.go` — partial frame reassembly | Edge case: packet fragmentation | P1 |
| P1.6 | Profile memory under concurrent multi-request load (large contexts + images). Set `GOGC` tuning guidance in docs | Edge case: concurrent memory pressure | P2 |

### 5.3 Cross-Platform Token Handling

| ID | Requirement | Audit Ref | Priority |
|---|---|---|---|
| T1.1 | Add Windows case to `getKiroDBPath()` using `os.UserConfigDir()` / `LOCALAPPDATA` | Windows: missing path case | P0 |
| T1.2 | Replace `sqlite3` shell-out in `refreshToken()` with pure-Go SQLite or direct HTTP refresh | Windows: sqlite3 not available | P1 |
| T1.3 | Handle non-ASCII and spaced home directory paths on Windows (test with `C:\Users\José García\`) | Windows: path edge cases | P1 |
| T1.4 | Add startup diagnostics: print which token source was found, which failed, and why | UX: silent failures | P1 |
| T1.5 | Document/detect WSL vs native mismatch (Kiro IDE native + proxy in WSL = different home dirs) | Windows: WSL edge case | P2 |

---

## 6. Non-Goals (Explicit)

- No Homebrew tap/cask (use `go install` for now)
- No Chocolatey package (use `go install` for now)
- No config file support (flags + env vars are sufficient)
- No auto-update mechanism
- No GUI or tray icon
- No Linux systemd unit (Linux users can write their own; launchd and Windows Service are the priorities)

---

## 7. Risks

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Windows Service requires admin to install | High | Users can't self-serve | Document; `start` command detects and prompts for elevation |
| Port 1234 conflicts with something obscure | Low | User confusion | `--port` flag; document in README |
| `golang.org/x/sys` version drift | Low | Build breaks | Pin version in go.sum; dependabot |
| Existing kirolink users lose config | Medium | Broken setup | `claude` command migrates keys; README migration section |
| launchd plist survives across upgrades | Medium | Stale binary path | `start` command regenerates plist each time |

---

## 8. Success Criteria

1. `go install github.com/ryolambert/openkiro@latest` works on macOS and Windows
2. `openkiro server` starts on port 1234 by default, bound to 127.0.0.1
3. `openkiro start` runs the proxy in the background without a terminal
4. `openkiro stop` cleanly shuts it down
5. Install scripts work on a clean macOS (bash+zsh) and Windows (PowerShell) machine
6. All existing tests pass after rename
7. Zero references to `kirolink`, `alexandeism`, or `8080` remain in code
