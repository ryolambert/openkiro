# Docker Sandbox

This document describes the Docker sandbox microVM infrastructure for openkiro:
the installed agent tools, how containers are configured for Claude Code and
Kiro workloads, the lifecycle API, and CI integration.

---

## Tool inventory

The `openkiro-sandbox:latest` image ships with all agent tools pre-installed:

| Binary | Version | Source | Purpose |
|--------|---------|--------|---------|
| `openkiro` | repo | `cmd/openkiro` | Anthropic API proxy for Kiro/AWS CodeWhisperer |
| `rtk` | repo | `cmd/rtk` | Token estimation and compression toolkit |
| `icm` | repo | `cmd/icm` | In-context memory MCP server |
| `headroom` | repo | `cmd/headroom` | Context budget manager |
| `mcp-gateway` | repo | `cmd/mcp-gateway` | Docker MCP Gateway HTTP server |
| `claude` | npm | `@anthropic-ai/claude-code` | Claude Code CLI agent |

All Go binaries are compiled with `CGO_ENABLED=0` (static, no libc dependency).
`claude` is installed globally via `npm install -g @anthropic-ai/claude-code`.

---

## Images

### `Dockerfile` — openkiro main image

Two-stage build targeting `gcr.io/distroless/static-debian12:nonroot`.
Contains **only** the `openkiro` binary and CA certificates. No shell, no
package manager, minimal attack surface.

```sh
# Build
docker build -t openkiro:latest .

# Run the proxy (host bind-mount for Kiro auth token)
docker run --rm \
  -p 127.0.0.1:1234:1234 \
  -v ~/.aws:/home/nonroot/.aws:ro \
  openkiro:latest server
```

### `Dockerfile.sandbox` — agent sandbox image

Alpine 3.20 base with:
- `tini` as PID 1 (zombie reaping + signal forwarding)
- `sandbox` user (UID/GID 1000) — non-root enforced at the OS level
- Node.js + npm — required for the Claude Code CLI
- All 5 Go binaries (`openkiro`, `rtk`, `icm`, `headroom`, `mcp-gateway`)
- `claude` (Claude Code CLI) installed globally via npm
- `/workspace` directory for host bind-mounts

```sh
# Build
docker build -f Dockerfile.sandbox -t openkiro-sandbox:latest .

# Verify all tools are available
docker run --rm openkiro-sandbox:latest openkiro version
docker run --rm openkiro-sandbox:latest rtk version
docker run --rm openkiro-sandbox:latest icm version
docker run --rm openkiro-sandbox:latest headroom version
docker run --rm openkiro-sandbox:latest mcp-gateway version
docker run --rm openkiro-sandbox:latest claude --version
```

---

## Security hardening

Every sandbox container is created with the following constraints enforced by
the `internal/sandbox` package:

| Constraint | Docker flag | Value |
|------------|-------------|-------|
| Non-root user | `--user` | `1000:1000` |
| Read-only root FS | `--read-only` | enabled |
| Drop all capabilities | `--cap-drop` | `ALL` |
| No privilege escalation | `--security-opt` | `no-new-privileges` |
| Memory cap | `--memory` | 512 MB (default) |
| CPU cap | `--cpus` | 0.50 (default) |
| Network | `--network` | `none` (default) / `bridge` (agent presets) |

The network mode is the key difference between use cases:
- **`none`** — fully air-gapped; no outbound traffic (default strict isolation)
- **`bridge`** — standard Docker networking; required for Claude Code / Kiro
  workloads so the container can reach AWS CodeWhisperer via the openkiro proxy

---

## Configuration presets

Four named presets are available in `internal/sandbox/agent.go`:

| Preset | Network | Extra env vars | Use case |
|--------|---------|----------------|----------|
| `DefaultConfig()` | `none` | — | Fully isolated scripting |
| `AgentConfig()` | `bridge` | — | General agent tooling |
| `ClaudeCodeConfig()` | `bridge` | `ANTHROPIC_BASE_URL`, `ANTHROPIC_API_KEY`, `NODE_NO_WARNINGS`, `DISABLE_AUTOUPDATER` | Claude Code CLI |
| `KiroConfig()` | `bridge` | `ANTHROPIC_BASE_URL`, `ANTHROPIC_API_KEY`, `KIRO_PROXY` | Kiro-based agent flows |

---

## Running Claude Code in a sandbox

Claude Code connects to the openkiro proxy at `ANTHROPIC_BASE_URL` and uses
AWS CodeWhisperer for inference. The `ClaudeCodeConfig()` preset sets up all
required environment variables:

```go
import "github.com/ryolambert/openkiro/internal/sandbox"

mgr := sandbox.NewManager()
cfg := sandbox.ClaudeCodeConfig()
cfg.WorkspaceDir = "/home/alice/my-project"

sb, err := mgr.Create(ctx, "claude-session-1", cfg)
if err != nil { log.Fatal(err) }
if err := mgr.Start(ctx, sb.ID); err != nil { log.Fatal(err) }
```

Or via the CLI:

```sh
# Create and start a Claude Code sandbox
openkiro sandbox create \
  --id claude-session-1 \
  --preset claude \
  --workspace /home/alice/my-project

# Check running sandboxes
openkiro sandbox list

# Destroy when done
openkiro sandbox destroy claude-session-1
```

Inside the running container, Claude Code is fully configured:

```sh
# Claude Code will use openkiro proxy at http://127.0.0.1:1234
claude "write a Go HTTP server"
```

---

## Running Kiro agent workloads

Kiro uses AWS CodeWhisperer for inference via the openkiro proxy. The `KiroConfig()` preset configures the environment for Kiro-based agent flows:

```go
cfg := sandbox.KiroConfig()
cfg.WorkspaceDir = "/data/workspaces/kiro-session"
sb, _ := mgr.Create(ctx, "kiro-session-1", cfg)
mgr.Start(ctx, "kiro-session-1")
```

Or via the CLI:

```sh
openkiro sandbox create --id kiro-1 --preset kiro --workspace /my/project
```

---

## Agent tools reference

### openkiro — Anthropic API proxy

Routes Claude Code / Kiro API requests to AWS CodeWhisperer using Kiro SSO tokens from `~/.aws/sso/cache/kiro-auth-token.json`.

```sh
openkiro server          # start proxy on 127.0.0.1:1234
openkiro server 8080     # custom port
openkiro version
```

### rtk — token compression toolkit

Estimates token counts and compresses message history.

```sh
rtk count "Hello, world!"
echo "My prompt" | rtk count
cat messages.json | rtk estimate
cat messages.json | rtk compress --target 4000 > messages-compressed.json
```

### icm — in-context memory MCP server

Key-value memory store, persisted to `/workspace/.icm-store.json`.

```sh
icm serve --port 8082          # start HTTP memory server
icm store my-key "my value"    # one-shot store
icm recall my-key              # one-shot recall
icm list                       # show all memories

# HTTP API
curl -X POST http://127.0.0.1:8082/remember \
  -H 'Content-Type: application/json' \
  -d '{"key":"project","value":"openkiro"}'
curl 'http://127.0.0.1:8082/recall?key=project'
```

### headroom — context budget manager

Reports available token budget and trims conversations to fit.

```sh
headroom status --max 8000 --used 3200
cat chat.json | headroom check  --max 8000
cat chat.json | headroom trim   --max 6000 > chat-trimmed.json
```

Exit code `2` means over budget (useful for scripting).

### mcp-gateway — Docker MCP Gateway

Discovers MCP tool servers in Docker containers and exposes them as HTTP endpoints.

```sh
mcp-gateway serve --port 8081   # start gateway
mcp-gateway list                # one-shot discovery

curl http://127.0.0.1:8081/health
curl http://127.0.0.1:8081/servers
curl 'http://127.0.0.1:8081/tools?server=file-tools'
```

#### Advertising an MCP server via Docker labels

```yaml
# docker-compose.yml
services:
  file-tools:
    image: my-mcp-server:latest
    labels:
      mcp.enable: "true"
      mcp.name: "file-tools"
      mcp.transport: "http"
      mcp.port: "9090"
      mcp.path: "/mcp"
```

| Label | Default | Description |
|-------|---------|-------------|
| `mcp.enable` | — | Set `"true"` to opt in (required) |
| `mcp.name` | first 12 chars of container ID | Human-readable name |
| `mcp.transport` | `http` | `http` or `stdio` |
| `mcp.port` | `8080` | Container port |
| `mcp.path` | `/mcp` | HTTP path prefix |

### claude — Claude Code CLI

Pre-installed from npm. Inside a `ClaudeCodeConfig` sandbox, `ANTHROPIC_BASE_URL` already points to the openkiro proxy — no additional configuration needed.

```sh
claude --version
claude "write a Go HTTP handler"
claude --help
```

---

## Container lifecycle

Managed by `internal/sandbox.Manager`:

```
creating → running → stopped → destroyed
              ↓
           failed ──(auto-heal)──→ running
```

### Programmatic usage

```go
import "github.com/ryolambert/openkiro/internal/sandbox"

mgr := sandbox.NewManager()

// Create a Claude Code sandbox
cfg := sandbox.ClaudeCodeConfig()
cfg.WorkspaceDir = "/home/alice/project"
sb, _ := mgr.Create(ctx, "session-abc", cfg)
mgr.Start(ctx, sb.ID)

// Auto-heal: restart failed, destroy idle (runs every 30s)
go mgr.StartAutoHeal(ctx)

// Clean up
mgr.Destroy(ctx, "session-abc")
mgr.DestroyAll(ctx)
```

### CLI usage

```sh
# Create + start (claude preset)
openkiro sandbox create --id my-session --preset claude --workspace /my/project

# List sandboxes
openkiro sandbox list

# Stop (keep container)
openkiro sandbox stop my-session

# Restart
openkiro sandbox start my-session

# Remove container
openkiro sandbox destroy my-session
```

---

## CI integration

`.github/workflows/docker.yml` runs on every push/PR:

1. **Lint** — `hadolint` on both Dockerfiles
2. **Build** — both images (`openkiro:ci`, `openkiro-sandbox:ci`)
3. **Smoke tests** — verifies each binary responds to `version`:
   - `openkiro version`, `rtk version`, `icm version`, `headroom version`,
     `mcp-gateway version`, `claude --version`
4. **Security scan** — Trivy (CRITICAL + HIGH, exit 1 on findings)
5. **Push to GHCR** — on version tags `v*`:
   - `ghcr.io/<owner>/openkiro:<version>`
   - `ghcr.io/<owner>/openkiro-sandbox:<version>`

---

## References

- [Docker Engine security](https://docs.docker.com/engine/security/)
- [distroless base images](https://github.com/GoogleContainerTools/distroless)
- [tini — init for containers](https://github.com/krallin/tini)
- [Claude Code CLI](https://www.npmjs.com/package/@anthropic-ai/claude-code)
- [`internal/sandbox` package](../internal/sandbox/sandbox.go)
- [`internal/sandbox/agent.go`](../internal/sandbox/agent.go) — config presets
- [`internal/gateway` package](../internal/gateway/gateway.go)

