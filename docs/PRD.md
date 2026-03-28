# PRD: openkiro Proxy Expansion

Date: 2026-03-28
Status: Draft
Author: Architecture Review
Parent Epic: https://github.com/ryolambert/openkiro/issues/1

---

## 1. Vision

Evolve openkiro from a single-purpose Anthropic→CodeWhisperer proxy into an **orchestration backbone for agent LLM development**. The expanded system provides:

- **Compression** — 60–90% token reduction on CLI tool output via rtk integration
- **Memory** — persistent agent memory across sessions via icm MCP integration
- **Context Optimization** — intelligent context budget management via headroom integration
- **Tool Routing** — schema elimination for large tool sets via mcp2cli-inspired ToolOptimizer
- **TOON Encoding** — native Go encoder for token-efficient array serialization
- **Docker MCP Gateway** — dynamic tool discovery and execution via MCP protocol
- **Docker Sandbox microVMs** — ephemeral, isolated agent runtime environments

The proxy remains a single-binary, zero-runtime-dependency Go tool. All new subsystems are either embedded as Go middleware or launched as optional sidecar containers.

---

## 2. User Personas

| Persona | Description | Primary Need |
|---------|-------------|--------------|
| Developer (macOS/Linux) | Runs Claude Code + Kiro IDE, iterates on agent tools | Fast proxy, compressed tool output, persistent memory |
| Developer (Windows) | Same stack on Windows, PowerShell | Install script handles Go + PATH, single binary |
| Agent Workflow Designer | Builds multi-step agent pipelines | ToolOptimizer reduces schema overhead, sandbox isolates exec |
| Platform Engineer | Manages shared infra for LLM agent teams | Docker MCP Gateway for central tool discovery |

---

## 3. Functional Requirements

### 3.1 Proxy Core (existing)

> Existing Anthropic→CodeWhisperer translation layer. All new subsystems hook in as middleware.

| ID | Requirement | Priority |
|----|-------------|----------|
| PC-1 | Accept POST `/v1/messages` in Anthropic API format | P0 |
| PC-2 | Translate to CodeWhisperer binary frame format and proxy upstream | P0 |
| PC-3 | Parse binary frame responses and emit Anthropic-compatible SSE or JSON | P0 |
| PC-4 | Retry on 403 with refreshed token | P0 |
| PC-5 | Support both streaming and non-streaming modes | P0 |
| PC-6 | Expose `/v1/models` and `/health` endpoints | P0 |
| PC-7 | Bind to `127.0.0.1` by default; accept `--listen` for non-local | P0 |
| PC-8 | All middleware must be chainable — each stage receives and returns `AnthropicRequest` | P0 |

### 3.2 Compression Middleware

> Integrates [rtk-ai/rtk](https://github.com/rtk-ai/rtk) compression to reduce token count for CLI and tool output. Target: 60–90% reduction on structured text.

| ID | Requirement | Priority |
|----|-------------|----------|
| CM-1 | Intercept tool result content blocks in requests and apply rtk compression | P0 |
| CM-2 | Detect content type (shell output, JSON array, plain text) and select encoder | P0 |
| CM-3 | Fall through without compression when rtk is unavailable | P0 |
| CM-4 | Expose per-request compression ratio in `X-OpenKiro-Compression-Ratio` response header | P1 |
| CM-5 | Configurable threshold: only compress blocks over N tokens (default 200) | P1 |
| CM-6 | Compression must be reversible for downstream display / tool result parsing | P0 |
| CM-7 | Benchmark suite: measure token count before/after for representative payloads | P1 |

### 3.3 Memory Layer

> Integrates [rtk-ai/icm](https://github.com/rtk-ai/icm) for persistent agent memory. icm is exposed as an MCP server; the proxy intercepts conversation context to trigger recall and store hooks.

| ID | Requirement | Priority |
|----|-------------|----------|
| ML-1 | On every inbound request, invoke icm recall hook with the current conversation hash | P0 |
| ML-2 | Inject recalled memories as a `system` content block prepended to the request | P0 |
| ML-3 | On every completed response, invoke icm store hook with assistant message and metadata | P0 |
| ML-4 | Memory operations must be async and non-blocking — max 50ms added latency | P0 |
| ML-5 | Configurable memory TTL (default 7 days); evict expired entries | P1 |
| ML-6 | `openkiro memory list` subcommand shows stored memories for current session | P1 |
| ML-7 | `openkiro memory clear` subcommand deletes all memories | P1 |
| ML-8 | icm MCP server started automatically as a sidecar when the proxy starts | P1 |

### 3.4 Context Optimization

> Integrates [chopratejas/headroom](https://github.com/chopratejas/headroom) for intelligent context budget management. headroom calculates safe message window sizes to stay within model context limits.

| ID | Requirement | Priority |
|----|-------------|----------|
| CO-1 | Before forwarding, compute token budget headroom for the target model's context window | P0 |
| CO-2 | If messages exceed available headroom, trim oldest non-system messages | P0 |
| CO-3 | Preserve system messages and the most recent N turns during trimming | P0 |
| CO-4 | Log trimmed token count at debug level | P1 |
| CO-5 | Expose per-model context window sizes in `/v1/models` response | P1 |
| CO-6 | headroom integration must not add more than 5ms latency per request | P0 |

### 3.5 ToolOptimizer

> mcp2cli-inspired meta-tool pattern. Replaces large `tools[]` arrays with a compact on-demand discovery interface, reducing token cost from `O(tools × turns)` to `O(turns)`.

| ID | Requirement | Priority |
|----|-------------|----------|
| TO-1 | Intercept requests with `tools[]` containing ≥ 5 schemas | P0 |
| TO-2 | Cache full tool schemas keyed by content hash | P0 |
| TO-3 | Replace `tools[]` with a single `openkiro_tool_call` meta-tool and compact listing | P0 |
| TO-4 | Inject system prompt explaining on-demand tool discovery | P0 |
| TO-5 | Intercept `tool_use` for `openkiro_tool_call`; resolve real schema from cache; execute | P0 |
| TO-6 | Route MCP-backed tools through Docker MCP Gateway when available | P1 |
| TO-7 | Configurable tool count threshold (default 5, env `OPENKIRO_TOOL_THRESHOLD`) | P1 |
| TO-8 | Pass-through mode for requests with fewer tools than threshold | P0 |
| TO-9 | Token savings metric logged at debug level per request | P1 |

### 3.6 TOON Encoding

> Native Go implementation of TOON (Token-Oriented Object Notation). Encodes uniform JSON arrays as a compact columnar format, achieving 30–60% token reduction on tabular data.

| ID | Requirement | Priority |
|----|-------------|----------|
| TN-1 | Detect JSON arrays of uniform objects in tool results and response content | P0 |
| TN-2 | Encode detected arrays to TOON columnar format | P0 |
| TN-3 | Minimum array length threshold before encoding (default 10 items) | P1 |
| TN-4 | TOON encoder is a pure Go implementation — no npm/Node.js dependency | P0 |
| TN-5 | Include TOON format description in injected system prompt | P1 |
| TN-6 | Provide `DecodeTOON(string) ([]map[string]any, error)` for downstream consumers | P1 |
| TN-7 | Benchmark suite: compare raw JSON token count vs TOON for representative datasets | P1 |

### 3.7 Docker MCP Gateway

> Provides dynamic tool discovery and execution via MCP protocol. Acts as a centralized gateway to Docker-hosted MCP servers.

| ID | Requirement | Priority |
|----|-------------|----------|
| GW-1 | Start MCP Gateway as a local HTTP/SSE server on a configurable port (default 8080) | P0 |
| GW-2 | Discover MCP servers from Docker labels (`mcp.enable=true`, `mcp.name`, `mcp.transport`) | P0 |
| GW-3 | Route `tools/list` and `tools/call` MCP requests to the correct container | P0 |
| GW-4 | Support stdio and HTTP/SSE MCP transports | P0 |
| GW-5 | `openkiro gateway start/stop/status` subcommands | P0 |
| GW-6 | Health check: verify Docker daemon is accessible on startup | P1 |
| GW-7 | Reconnect on container restart without proxy restart | P1 |
| GW-8 | Gateway must not require admin/root on macOS or Linux | P0 |

### 3.8 Docker Sandbox microVMs

> Ephemeral, isolated agent runtime environments. Each session spawns a fresh container; cleanup is automatic on session end.

| ID | Requirement | Priority |
|----|-------------|----------|
| SB-1 | `openkiro sandbox create [--image IMAGE]` spawns an ephemeral container | P0 |
| SB-2 | `openkiro sandbox exec SESSION_ID COMMAND` runs a command in the sandbox | P0 |
| SB-3 | `openkiro sandbox destroy SESSION_ID` removes the container and all resources | P0 |
| SB-4 | Sandbox containers run as non-root, with read-only root filesystem | P0 |
| SB-5 | Network isolation: sandboxes have no outbound internet by default | P0 |
| SB-6 | Mount a per-session workspace volume at `/workspace` | P1 |
| SB-7 | Auto-destroy on proxy shutdown or configurable idle timeout (default 30 min) | P1 |
| SB-8 | `openkiro sandbox list` shows active sessions with container IDs | P1 |
| SB-9 | Resource limits: configurable CPU and memory caps per sandbox | P1 |

---

## 4. Non-Functional Requirements

### 4.1 Performance

| ID | Requirement | Target |
|----|-------------|--------|
| NFP-1 | Middleware chain overhead per request | < 10ms p99 |
| NFP-2 | Memory recall latency (icm hook) | < 50ms p99 |
| NFP-3 | TOON encoding for 100-row array | < 1ms |
| NFP-4 | ToolOptimizer schema caching (cache hit) | < 0.5ms |
| NFP-5 | Compression ratio for CLI output | ≥ 60% token reduction |
| NFP-6 | Compression ratio for JSON arrays | ≥ 30% token reduction |

### 4.2 Security

| ID | Requirement |
|----|-------------|
| NFS-1 | All sandbox containers run as non-root (UID ≥ 1000) |
| NFS-2 | Sandbox network isolation by default; opt-in for internet access |
| NFS-3 | Tool schema cache uses content-addressable storage (hash keys only — no user data in keys) |
| NFS-4 | Memory store encrypts sensitive conversation snippets at rest |
| NFS-5 | MCP Gateway authenticates only from localhost by default |
| NFS-6 | Docker socket access limited to dedicated Gateway process, not proxy process |

### 4.3 Cross-Platform

| ID | Requirement |
|----|-------------|
| NFX-1 | Proxy Core + Middleware run on macOS, Linux, Windows without external dependencies |
| NFX-2 | Docker-dependent features (Gateway, Sandbox) degrade gracefully on systems without Docker |
| NFX-3 | TOON encoder and ToolOptimizer are pure Go — no CGo, no shell-outs |
| NFX-4 | Memory layer (icm) falls back to in-process ephemeral store when icm sidecar is unavailable |

### 4.4 Observability

| ID | Requirement |
|----|-------------|
| NFO-1 | Structured log entries for each middleware stage (compression ratio, tokens saved, latency) |
| NFO-2 | `X-OpenKiro-*` response headers expose per-request metrics |
| NFO-3 | `/metrics` endpoint (optional, gated by `OPENKIRO_METRICS=1`) exposes Prometheus-compatible counters |

---

## 5. Upstream Dependencies

| Upstream Repo | Role in openkiro | openkiro Package |
|---------------|-----------------|------------------|
| [rtk-ai/rtk](https://github.com/rtk-ai/rtk) | CLI output compression | `internal/middleware/compression.go` |
| [rtk-ai/icm](https://github.com/rtk-ai/icm) | Persistent agent memory via MCP | `internal/middleware/memory.go` |
| [chopratejas/headroom](https://github.com/chopratejas/headroom) | Context budget management | `internal/middleware/context.go` |
| [knowsuchagency/mcp2cli](https://github.com/knowsuchagency/mcp2cli) | ToolOptimizer pattern (Go port of core technique) | `internal/middleware/toolopt.go` |

All upstream projects are MIT-licensed. The Go port of mcp2cli's core algorithm (~400 lines) does not pull in the Python runtime — only the token-saving technique is adapted.

---

## 6. Dependency Map

```
upstream repo                     openkiro package
─────────────────────────────────────────────────────────────
rtk-ai/rtk                    →   internal/middleware/compression.go
rtk-ai/icm                    →   internal/middleware/memory.go
chopratejas/headroom           →   internal/middleware/context.go
knowsuchagency/mcp2cli (port)  →   internal/middleware/toolopt.go
(native Go)                    →   internal/middleware/toon.go
Docker Engine API              →   internal/gateway/gateway.go
Docker Engine API              →   internal/sandbox/sandbox.go
```

---

## 7. Phased Delivery Plan

### Phase 1 — TDD Foundation + Middleware Scaffold (Weeks 1–2)

**Goal**: Establish test-driven workflow and scaffold the middleware pipeline without changing proxy behavior.

**Deliverables**:
- `internal/middleware/chain.go` — chainable `Middleware` interface with `Intercept(req) req` and `PostProcess(resp) resp` hooks
- GitHub Actions CI with `go test ./... -race -coverprofile` gate (80% coverage floor)
- Test harness: `internal/middleware/testutil/` with request/response fixtures
- `docs/tdd.md` — Red-Green-Refactor guide and test conventions for the project

**TDD Workflow**:
```
RED   → Write failing test for Middleware.Intercept contract
GREEN → Implement minimal Chain struct to pass test
REFACTOR → Extract shared helpers, ensure <10ms benchmark
```

**Tests to write first** (before implementation):
- `TestChain_Empty` — empty chain passes request unchanged
- `TestChain_SingleMiddleware` — single middleware transforms request
- `TestChain_Order` — multiple middlewares execute in registration order
- `TestChain_ShortCircuit` — middleware returning error halts chain

### Phase 2 — Compression + Memory + ToolOptimizer (Weeks 3–6)

**Goal**: Deliver the three highest-value middleware components with full test coverage.

**Deliverables**:
- `internal/middleware/toolopt.go` + `toolopt_test.go`
- `internal/middleware/toon.go` + `toon_test.go`
- `internal/middleware/compression.go` + `compression_test.go`
- `internal/middleware/memory.go` + `memory_test.go`
- `internal/middleware/context.go` + `context_test.go`

**TDD workflow per component**:
```
RED   → Write benchmark test asserting ≥60% compression
GREEN → Implement encoder, run benchmark
REFACTOR → Profile hot path, optimize allocations
```

**Key tests**:
- `TestToolOptimizer_BelowThreshold` — pass-through when tools < 5
- `TestToolOptimizer_AboveThreshold` — replaces tools[] with meta-tool
- `TestToolOptimizer_CacheHit` — schema lookup < 0.5ms
- `TestTOON_Encode_UniformArray` — columnar output matches spec
- `TestTOON_Encode_NonUniform` — graceful fallback to raw JSON
- `TestCompression_CLIOutput` — ≥60% token reduction on shell output fixture
- `TestMemory_RecallInject` — recalled snippets prepended to system block
- `TestContext_HeadroomTrim` — messages trimmed when over budget

### Phase 3 — Docker MCP Gateway + Sandbox (Weeks 7–10)

**Goal**: Deliver Docker-based tool infrastructure with integration tests using Docker-in-Docker or testcontainers-go.

**Deliverables**:
- `internal/gateway/gateway.go` + `gateway_test.go`
- `internal/sandbox/sandbox.go` + `sandbox_test.go`
- `openkiro gateway` and `openkiro sandbox` CLI subcommands
- Integration test suite using `testcontainers-go`

**TDD workflow**:
```
RED   → Write test asserting tool discovery from mock MCP container
GREEN → Implement Docker label-based discovery
REFACTOR → Handle reconnect, add health checks
```

**Key tests**:
- `TestGateway_Discovery` — discovers MCP servers from Docker labels
- `TestGateway_RouteToolCall` — routes tool execution to correct container
- `TestSandbox_CreateDestroy` — lifecycle completes in < 5s
- `TestSandbox_NonRoot` — container UID != 0
- `TestSandbox_NetworkIsolation` — no outbound connectivity by default

---

## 8. Success Criteria

1. All middleware components have ≥ 80% test coverage
2. ToolOptimizer achieves ≥ 95% token reduction on a 30-tool request (matches mcp2cli benchmark)
3. TOON encoding achieves ≥ 30% token reduction on a 100-row JSON array
4. Compression middleware achieves ≥ 60% reduction on representative CLI output
5. Memory recall adds < 50ms latency at p99
6. Middleware chain adds < 10ms overhead at p99 for a no-op chain
7. `go build ./...` succeeds with zero external dependencies added beyond the existing `golang.org/x/sys`
8. All existing tests continue to pass after middleware scaffold is wired in

---

## 9. Non-Goals (Explicit)

- No OpenAPI or GraphQL client (Docker MCP Gateway abstracts tool sources)
- No Homebrew tap or Chocolatey package (use `go install`)
- No GUI or tray icon
- No auto-update mechanism
- No Python or Node.js runtime required (TOON is native Go; mcp2cli is a Go port of the core technique only)
- No Linux systemd unit (Linux daemon users write their own)
