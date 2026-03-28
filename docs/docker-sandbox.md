# Docker Sandbox

This document describes the Docker sandbox microVM infrastructure for openkiro:
how the images are structured, the container lifecycle, security hardening,
and how to use the Go packages to manage sandbox containers programmatically.

---

## Overview

openkiro provides two Docker images and a Go lifecycle manager for running
isolated agent workloads as ephemeral Docker containers:

| Image | Purpose |
|-------|---------|
| `openkiro:latest` | Minimal distroless image containing only the openkiro binary |
| `openkiro-sandbox:latest` | Agent sandbox image — non-root Alpine with openkiro pre-installed |

Sandbox containers are **not long-lived services**; they are created per-session
(or per-task) and automatically destroyed when idle or when the proxy shuts down.

---

## Security hardening

Every sandbox container is created with the following constraints enforced by
the `internal/sandbox` package:

| Constraint | Docker flag | Value |
|------------|-------------|-------|
| Non-root user | `--user` | `1000:1000` |
| Read-only root FS | `--read-only` | enabled |
| No network | `--network` | `none` |
| Memory cap | `--memory` | 512 MB (default) |
| CPU cap | `--cpus` | 0.50 (default) |
| Drop all capabilities | `--cap-drop` | `ALL` |
| No privilege escalation | `--security-opt` | `no-new-privileges` |

These defaults can be overridden via `sandbox.Config` when creating a sandbox
programmatically.

---

## Images

### `Dockerfile` — openkiro main image

A two-stage build that compiles the openkiro binary with `CGO_ENABLED=0` and
packages it in a [distroless/static](https://github.com/GoogleContainerTools/distroless)
base image. The final image contains only the binary and CA certificates —
no shell, no package manager, no libc.

```sh
# Build
docker build -t openkiro:latest .

# Run the proxy
docker run --rm \
  -p 127.0.0.1:1234:1234 \
  -v ~/.aws:/home/nonroot/.aws:ro \
  openkiro:latest server
```

### `Dockerfile.sandbox` — sandbox agent image

An Alpine-based image with:
- `tini` as PID 1 (proper zombie reaping and signal forwarding)
- `sandbox` user (UID/GID 1000)
- `/workspace` directory for host workspace bind-mounts
- The `openkiro` binary
- Placeholder stubs for `rtk`, `icm`, and `headroom` (uncomment when published)

```sh
# Build
docker build -f Dockerfile.sandbox -t openkiro-sandbox:latest .
```

The sandbox manager creates containers from this image automatically; you
rarely need to run it directly.

---

## Container lifecycle

The lifecycle is managed by `internal/sandbox.Manager`. Containers move
through the following states:

```
creating → running → stopped → destroyed
              ↓
           failed  ──(auto-heal)──→ running
```

### Idle timeout and auto-heal

The Manager's `StartAutoHeal` goroutine runs every 30 seconds:

- **Failed** containers are restarted automatically.
- **Running** containers that have had no activity for longer than
  `Config.IdleTimeout` (default 30 minutes) are destroyed.

---

## Programmatic usage

### Creating and starting a sandbox

```go
import "github.com/ryolambert/openkiro/internal/sandbox"

mgr := sandbox.NewManager()

cfg := sandbox.DefaultConfig()
cfg.WorkspaceDir = "/home/alice/project"  // bind-mounted at /workspace

sb, err := mgr.Create(ctx, "session-abc", cfg)
if err != nil {
    log.Fatal(err)
}

if err := mgr.Start(ctx, sb.ID); err != nil {
    log.Fatal(err)
}
```

### Stopping and destroying a sandbox

```go
// Graceful stop (container kept but not running).
if err := mgr.Stop(ctx, sb.ID); err != nil {
    log.Printf("stop: %v", err)
}

// Remove the container entirely.
if err := mgr.Destroy(ctx, sb.ID); err != nil {
    log.Printf("destroy: %v", err)
}

// Destroy all tracked sandboxes (e.g. on proxy shutdown).
if err := mgr.DestroyAll(ctx); err != nil {
    log.Printf("destroy all: %v", err)
}
```

### Auto-heal

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

// Runs until ctx is cancelled.
go mgr.StartAutoHeal(ctx)
```

### Custom configuration

```go
cfg := sandbox.Config{
    Image:        "openkiro-sandbox:latest",
    WorkspaceDir: "/data/workspaces/session-xyz",
    ReadOnlyRoot: true,
    NetworkMode:  "none",
    UID:          "1000:1000",
    MemoryMB:     1024,
    CPUPercent:   75.0,
    IdleTimeout:  10 * time.Minute,
    Env:          []string{"OPENKIRO_PORT=1234"},
    Labels:       map[string]string{"project": "my-agent"},
}
```

---

## Docker MCP Gateway

The `internal/gateway` package discovers MCP (Model Context Protocol) tool
servers running as Docker containers and routes tool-listing and tool-call
requests to them.

### Advertising an MCP server via labels

Add these labels to any Docker container to make it discoverable:

```yaml
# docker-compose.yml
services:
  my-tool-server:
    image: my-mcp-server:latest
    labels:
      mcp.enable: "true"
      mcp.name: "file-tools"
      mcp.transport: "http"
      mcp.port: "9090"
      mcp.path: "/mcp"
```

| Label | Required | Default | Description |
|-------|----------|---------|-------------|
| `mcp.enable` | yes | — | Set to `"true"` to opt in |
| `mcp.name` | no | first 12 chars of container ID | Human-readable server name |
| `mcp.transport` | no | `http` | `http` or `stdio` |
| `mcp.port` | no | `8080` | Container port for HTTP transport |
| `mcp.path` | no | `/mcp` | HTTP path prefix |

### Using the gateway

```go
import "github.com/ryolambert/openkiro/internal/gateway"

gw := gateway.NewGateway()

// One-shot discovery.
servers, err := gw.Discover(ctx)

// Continuous background discovery (updates every 30s).
go gw.StartDiscovery(ctx)

// Look up a server by name.
srv := gw.ServerByName("file-tools")

// Get the HTTP endpoint URL.
endpoint, err := gw.ToolEndpoint("file-tools")
// → "http://127.0.0.1:9090/mcp"

// Check Docker daemon connectivity.
if err := gw.Health(ctx); err != nil {
    log.Printf("docker unavailable: %v", err)
}
```

---

## CI integration

The `.github/workflows/docker.yml` workflow:

1. **Lint** — runs `hadolint` on both Dockerfiles.
2. **Build** — builds both images and runs smoke tests.
3. **Security scan** — runs `trivy` (CRITICAL + HIGH only) on both images.
4. **Push** — on version tags (`v*`), pushes both images to the GitHub
   Container Registry (`ghcr.io`):
   - `ghcr.io/<owner>/openkiro:<version>`
   - `ghcr.io/<owner>/openkiro-sandbox:<version>`

---

## Agent tools (planned)

The sandbox image includes stubs for the following tools that will be
pre-installed once their packages are published:

| Tool | Description | Status |
|------|-------------|--------|
| `rtk` | Token compression toolkit (60–90% reduction) | Planned |
| `icm` | In-context memory MCP server | Planned |
| `headroom` | Context budget manager | Planned |

See `Dockerfile.sandbox` for the commented installation blocks.

---

## References

- [Docker Engine security](https://docs.docker.com/engine/security/)
- [Docker sandbox hardening](https://docs.docker.com/engine/security/seccomp/)
- [distroless base images](https://github.com/GoogleContainerTools/distroless)
- [tini — a tiny init for containers](https://github.com/krallin/tini)
- [`internal/sandbox` package](../internal/sandbox/sandbox.go)
- [`internal/gateway` package](../internal/gateway/gateway.go)
