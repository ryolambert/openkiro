# docs/

Documentation index for the openkiro proxy.

---

## Files

| File | Description |
|------|-------------|
| [PRD.md](PRD.md) | Full Product Requirements Document covering all subsystems (Proxy Core, Compression, Memory, Context Optimization, ToolOptimizer, TOON Encoding, Docker MCP Gateway, Docker Sandbox) |
| [architecture-diagrams.md](architecture-diagrams.md) | Mermaid architecture diagrams: component layout, request flow, middleware chain, memory integration, ToolOptimizer decision tree, Docker sandbox lifecycle |
| [task-breakdown.md](task-breakdown.md) | v1.0 execution plan: 21 tasks across 8 phases |
| [env-alias-plan.md](env-alias-plan.md) | Implementation plan for `openkiro env` and `openkiro alias` commands |
| [audit/security-performance-audit.md](audit/security-performance-audit.md) | Security, performance, and cross-platform audit findings |

---

## Dependency Map

The table below shows which upstream open-source projects map to which openkiro internal packages.

| Upstream Repository | Technique / Role | openkiro Package |
|---------------------|-----------------|------------------|
| [rtk-ai/rtk](https://github.com/rtk-ai/rtk) | CLI output compression (60–90% token reduction) | `internal/middleware/compression.go` |
| [rtk-ai/icm](https://github.com/rtk-ai/icm) | Persistent agent memory via MCP recall/store | `internal/middleware/memory.go` |
| [chopratejas/headroom](https://github.com/chopratejas/headroom) | Context budget management (trim to fit window) | `internal/middleware/context.go` |
| [knowsuchagency/mcp2cli](https://github.com/knowsuchagency/mcp2cli) | ToolOptimizer: schema elimination meta-tool pattern (Go port) | `internal/middleware/toolopt.go` |
| *(native Go)* | TOON columnar array encoding | `internal/middleware/toon.go` |
| Docker Engine API | MCP Gateway: dynamic tool discovery via Docker labels | `internal/gateway/gateway.go` |
| Docker Engine API | Sandbox: ephemeral isolated agent containers | `internal/sandbox/sandbox.go` |

---

## Architecture Overview

```
internal/
  middleware/     ← NEW: Chain, ToolOptimizer, Compression, Memory, Context, TOON
  proxy/          ← existing: HTTP server, request/response translation, types
  gateway/        ← NEW: Docker MCP Gateway client and tool router
  sandbox/        ← NEW: Docker sandbox lifecycle management
  daemon/         ← existing: background process lifecycle (start/stop/status)
  token/          ← existing: auth token management, upstream HTTP client
  protocol/       ← existing: CodeWhisperer binary frame → SSE event parser
  service/        ← existing: OS service integration (launchd, Windows Service)
```

For the full architecture with Mermaid diagrams, see [architecture-diagrams.md](architecture-diagrams.md).

For requirements and phased delivery plan, see [PRD.md](PRD.md).
