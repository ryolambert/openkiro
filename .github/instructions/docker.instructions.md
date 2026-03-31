---
applyTo: "Dockerfile*"
---
# Docker & Sandbox Guidelines

## Dockerfile Conventions
- Use multi-stage builds: builder stage (Go compilation) → minimal runtime stage
- Prefer `gcr.io/distroless/static-debian12:nonroot` or Alpine 3.20 for runtime
- Always use `CGO_ENABLED=0` for static binaries
- Set `--trimpath` in go build for reproducible builds
- Run as non-root user (UID 1000) — never as root
- Include `HEALTHCHECK` instructions where applicable

## Sandbox Architecture
This project includes Docker sandbox templates for isolated agent environments:
- `Dockerfile.sandbox` — standalone Alpine-based sandbox with all companion tools
- `Dockerfile.sandbox-claude` — extends Docker's Claude Code sandbox template
- `Dockerfile.sandbox-kiro` — extends Docker's Kiro sandbox template

## Sandbox Presets (`internal/sandbox/agent.go`)
- `DefaultConfig()` — strict isolation: network=none, read-only root FS
- `AgentConfig()` — agent workloads: bridge networking, read-only root FS
- `ClaudeCodeConfig()` — Claude Code: bridge + ANTHROPIC_BASE_URL/API_KEY env vars
- `KiroConfig()` — Kiro agent: bridge + ANTHROPIC_BASE_URL/KIRO_PROXY env vars

## Security Constraints
- API keys injected via environment variables at runtime, never baked into images
- Network whitelisting for trusted endpoints only
- Workspace directories bind-mounted; sensitive host files never exposed
- Ephemeral containers: create → use → destroy lifecycle

## Build Commands
```bash
make sandbox-claude    # Build Claude Code sandbox template
make sandbox-kiro      # Build Kiro sandbox template
make sandbox-all       # Build all sandbox templates
```
