# Architecture Diagrams

## 1. Component Architecture

```mermaid
graph TB
    subgraph Client["Client Layer"]
        CC[Claude Code]
        OC[OpenClaw]
        CURL[curl / HTTP client]
    end

    subgraph OpenKiro["openkiro binary"]
        CLI[CLI Router<br/>server | start | stop | status<br/>read | refresh | export | claude]

        subgraph Server["HTTP Proxy Server :1234"]
            MW[Log Middleware]
            BL[Body Limiter<br/>MaxBytesReader 200MB]
            MSG[POST /v1/messages]
            MOD[GET /v1/models<br/>sorted deterministic]
            HP[GET /health]
        end

        subgraph Security["Security Layer"]
            BIND[Bind Guard<br/>127.0.0.1 default<br/>--listen for override]
            TIMEOUT[Timeout Config<br/>Read: 30s | Write: 60s/0<br/>Idle: 120s | Header: 10s]
            REDACT[Log Redactor<br/>tokens: first8...last4<br/>bodies: sha256 summary only]
            PANIC[Panic Handler<br/>generic 500, no stack]
        end

        TM[Token Manager<br/>read from ~/.aws/sso/cache/<br/>retry on parse failure]
        TR[Request Translator<br/>Anthropic → CodeWhisperer]

        subgraph Streaming["Streaming Engine"]
            RP[Response Parser<br/>protocol/sse_parser.go]
            INCR[Incremental Reader<br/>frame-by-frame, no buffering]
            FLUSH[http.Flusher<br/>per-event flush to client]
        end

        subgraph Transport["Upstream Transport"]
            POOL[Connection Pool<br/>single http.Client<br/>MaxIdleConnsPerHost: 10]
            CTIMEOUT[Client Timeout<br/>60s non-stream, ctx for stream]
        end

        subgraph Service["service/ package"]
            DM[Daemon Manager<br/>start/stop/status/PID]
            LD[launchd.go<br/>macOS plist gen]
            WS[windows.go<br/>Windows Service via x/sys]
        end

        subgraph Config["Configuration"]
            PORT[Port Resolution<br/>--port > OPENKIRO_PORT > 1234]
            LOG[Log Router<br/>foreground→stderr<br/>background→file]
            DBG[Debug Gate<br/>OPENKIRO_DEBUG<br/>zero alloc when off]
        end
    end

    subgraph Upstream["AWS Backend"]
        CW[CodeWhisperer API<br/>us-east-1]
    end

    subgraph FS["Filesystem"]
        TOKEN[~/.aws/sso/cache/<br/>kiro-auth-token.json]
        KIRODB[Kiro CLI SQLite DB<br/>pure-Go driver, no shell-out]
        LOGS[Platform Log Dir<br/>~/Library/Logs/openkiro/<br/>%LOCALAPPDATA%\openkiro\logs\]
        PID[PID File]
        PLIST[~/Library/LaunchAgents/<br/>com.openkiro.proxy.plist]
    end

    CC & OC & CURL -->|HTTP| BIND
    BIND --> BL --> MSG & MOD & HP
    MSG --> TM
    MSG --> TR
    TR -->|via pool| POOL --> CW
    CW -->|binary frames| INCR
    INCR --> RP --> FLUSH --> MSG
    TM --> TOKEN
    CLI -->|refresh| KIRODB
    CLI -->|start/stop| DM
    DM -->|macOS| LD
    DM -->|Windows| WS
    LD --> PLIST
    DM --> PID
    LOG --> LOGS
```

## 2. Install Flow

```mermaid
flowchart TD
    START([User runs install script]) --> DETECT_OS{Detect OS}

    DETECT_OS -->|macOS/Linux| CHECK_GO_UNIX{Go installed?}
    DETECT_OS -->|Windows| CHECK_GO_WIN{Go installed?}

    CHECK_GO_UNIX -->|Yes| GO_VER_UNIX{Go >= 1.23?}
    CHECK_GO_UNIX -->|No| CHECK_BREW{Homebrew installed?}

    CHECK_BREW -->|Yes| BREW_GO[brew install go]
    CHECK_BREW -->|No| INSTALL_BREW[Install Homebrew] --> BREW_GO

    BREW_GO --> GO_VER_UNIX
    GO_VER_UNIX -->|Yes| GO_INSTALL_UNIX[go install github.com/ryolambert/openkiro@latest]
    GO_VER_UNIX -->|No| UPGRADE_GO_UNIX[brew upgrade go] --> GO_INSTALL_UNIX

    GO_INSTALL_UNIX --> DETECT_SHELL{Detect shell}

    DETECT_SHELL -->|zsh| CHECK_ZSHRC{GOPATH/bin in ~/.zshrc?}
    DETECT_SHELL -->|bash| CHECK_BASHRC{GOPATH/bin in rc file?}

    CHECK_ZSHRC -->|No| ADD_ZSHRC[Append to ~/.zshrc]
    CHECK_ZSHRC -->|Yes| VERIFY_UNIX
    CHECK_BASHRC -->|No| ADD_BASHRC[Append to ~/.bashrc + ~/.bash_profile]
    CHECK_BASHRC -->|Yes| VERIFY_UNIX

    ADD_ZSHRC --> VERIFY_UNIX
    ADD_BASHRC --> VERIFY_UNIX

    VERIFY_UNIX[Verify: openkiro version] --> DONE_UNIX([✓ Print success + next steps])

    CHECK_GO_WIN -->|Yes| GO_VER_WIN{Go >= 1.23?}
    CHECK_GO_WIN -->|No| WINGET_GO[winget install GoLang.Go]

    WINGET_GO --> GO_VER_WIN
    GO_VER_WIN -->|Yes| GO_INSTALL_WIN[go install github.com/ryolambert/openkiro@latest]
    GO_VER_WIN -->|No| UPGRADE_GO_WIN[winget upgrade GoLang.Go] --> GO_INSTALL_WIN

    GO_INSTALL_WIN --> CHECK_PATH_WIN{GOPATH\bin in user PATH?}
    CHECK_PATH_WIN -->|No| ADD_PATH_WIN[Add to user PATH via setx]
    CHECK_PATH_WIN -->|Yes| VERIFY_WIN

    ADD_PATH_WIN --> VERIFY_WIN
    VERIFY_WIN[Verify: openkiro version] --> DONE_WIN([✓ Print success + next steps])
```

## 3. Daemon Lifecycle

```mermaid
statediagram-v2
    [*] --> Idle

    Idle --> Starting: openkiro start [--port N]
    Starting --> CheckPID: Read PID file
    CheckPID --> AlreadyRunning: PID exists + process alive
    CheckPID --> GenerateConfig: No PID / stale PID

    AlreadyRunning --> Idle: Print error + exit 1

    GenerateConfig --> LaunchdLoad: macOS
    GenerateConfig --> ServiceInstall: Windows

    LaunchdLoad --> WritePlist: Generate plist with port + log path
    WritePlist --> LaunchctlLoad: launchctl load plist
    LaunchctlLoad --> Running

    ServiceInstall --> RegisterService: sc.exe create / x/sys/windows/svc
    RegisterService --> StartService: sc.exe start / svc.Run
    StartService --> Running

    Running --> Stopping: openkiro stop
    Running --> Crashed: Unexpected exit
    Running --> Running: Serving requests on :1234

    Stopping --> LaunchctlUnload: macOS
    Stopping --> StopService: Windows

    LaunchctlUnload --> CleanPID: launchctl unload plist
    StopService --> CleanPID: Stop + delete service

    CleanPID --> Idle: Remove PID file

    Crashed --> Running: Auto-restart (KeepAlive / recovery)
    Crashed --> Idle: Max retries exceeded

    state Running {
        [*] --> Listening
        Listening --> HandleRequest: Incoming HTTP
        HandleRequest --> ReadToken: Get auth token
        ReadToken --> Translate: Anthropic → CW
        Translate --> Proxy: Send to AWS
        Proxy --> ParseResponse: Binary → SSE/JSON
        ParseResponse --> Respond: Send to client
        Respond --> Listening
    }
```

## 4. Request Flow (Detailed)

```mermaid
sequenceDiagram
    participant C as Client (Claude Code)
    participant P as openkiro Proxy :1234
    participant T as Token File
    participant CW as CodeWhisperer API

    C->>P: POST /v1/messages<br/>{model, messages, stream}
    P->>P: MaxBytesReader (1MB cap)
    P->>P: Parse AnthropicRequest
    P->>P: resolveModelID()
    P->>T: Read kiro-auth-token.json
    T-->>P: {accessToken, refreshToken}
    P->>P: buildCodeWhispererRequest()
    P->>P: ensurePayloadFits() (trim history if >250KB)
    P->>CW: POST /generateAssistantResponse<br/>+ Bearer token + SigV4-ish headers
    alt Streaming
        CW-->>P: Binary frames (chunked)
        P->>P: protocol.ParseEvents()
        P-->>C: SSE events (message_start, content_block_delta, ...)
    else Non-streaming
        CW-->>P: Binary frames (complete)
        P->>P: protocol.ParseEvents()
        P->>P: assembleAnthropicResponse()
        P-->>C: JSON {content, stop_reason, usage}
    end
    alt 403 Forbidden
        P->>T: Re-read token (may have been refreshed by IDE)
        P->>CW: Retry with new token
    end
```

## 5. Security & Hardening Layers

> Maps to [security-performance-audit.md](security-performance-audit.md) findings

```mermaid
flowchart TD
    REQ([Inbound HTTP Request]) --> BIND{Bind check}
    BIND -->|127.0.0.1 only| TIMEOUT[Server Timeouts<br/>Read: 30s, Header: 10s<br/>Idle: 120s]
    BIND -->|0.0.0.0 via --listen| WARN[⚠ Log warning:<br/>non-local bind] --> TIMEOUT

    TIMEOUT --> MAXBODY[MaxBytesReader<br/>200MB limit]
    MAXBODY -->|> 200MB| REJECT_413[413 Too Large]
    MAXBODY -->|OK| PARSE[Parse JSON]

    PARSE -->|Invalid| REJECT_400[400 Bad Request<br/>no internal details]
    PARSE -->|Valid| HANDLER[Request Handler]

    HANDLER --> TOKEN_READ[Read Token File]
    TOKEN_READ -->|Parse fail| RETRY[Wait 100ms + retry once<br/>handles IDE race condition]
    RETRY -->|Still fails| REJECT_500[500 Internal Error]
    TOKEN_READ -->|OK| UPSTREAM

    subgraph UPSTREAM[Upstream Request]
        CLIENT[Pooled http.Client<br/>MaxIdleConnsPerHost: 10]
        TIMEOUT_OUT[60s timeout<br/>or context for streaming]
        TLS[Default TLS<br/>InsecureSkipVerify: NEVER]
    end

    UPSTREAM --> RESPONSE{Response}

    RESPONSE -->|Stream| INCREMENTAL[Frame-by-frame read<br/>Flush per event<br/>WriteTimeout: 0]
    RESPONSE -->|Non-stream| BUFFER[Read + parse<br/>WriteTimeout: 60s]

    INCREMENTAL & BUFFER --> LOG_GATE{OPENKIRO_DEBUG?}
    LOG_GATE -->|Off| SILENT[Log: method, path, duration only]
    LOG_GATE -->|On| VERBOSE[Log: body sha256+size<br/>token: first8...last4<br/>SSE events summary]

    HANDLER -->|Panic| PANIC_HANDLER[Recover<br/>Log internally<br/>Return generic 500<br/>No stack trace to client]

    style REJECT_413 fill:#f66,color:#fff
    style REJECT_400 fill:#f66,color:#fff
    style REJECT_500 fill:#f66,color:#fff
    style WARN fill:#fa0,color:#fff
    style PANIC_HANDLER fill:#f66,color:#fff
```

## 6. Streaming: Before vs After

```mermaid
sequenceDiagram
    participant C as Client
    participant P as Proxy
    participant CW as CodeWhisperer

    Note over C,CW: BEFORE (current — full buffer)
    C->>P: POST /v1/messages {stream:true}
    P->>CW: POST /generateAssistantResponse
    CW-->>P: Frame 1 (100ms)
    CW-->>P: Frame 2 (200ms)
    CW-->>P: Frame 3 (300ms)
    CW-->>P: Frame N (5000ms)
    Note over P: io.ReadAll — waits for ALL frames
    P-->>C: SSE event 1 (5001ms)
    P-->>C: SSE event 2 (5001ms)
    P-->>C: SSE event N (5002ms)

    Note over C,CW: AFTER (incremental flush)
    C->>P: POST /v1/messages {stream:true}
    P->>CW: POST /generateAssistantResponse
    CW-->>P: Frame 1 (100ms)
    P-->>C: SSE event 1 (101ms) ← immediate flush
    CW-->>P: Frame 2 (200ms)
    P-->>C: SSE event 2 (201ms) ← immediate flush
    CW-->>P: Frame 3 (300ms)
    P-->>C: SSE event 3 (301ms) ← immediate flush
    CW-->>P: Frame N (5000ms)
    P-->>C: SSE event N (5001ms) ← immediate flush
```

## 7. Port Resolution

```mermaid
flowchart LR
    FLAG["--port flag"] -->|highest priority| RESOLVE
    ENV["OPENKIRO_PORT env"] -->|if no flag| RESOLVE
    DEFAULT["1234"] -->|fallback| RESOLVE
    RESOLVE[Resolved Port] --> SERVER["server / start"]
    RESOLVE --> EXPORT["export command"]
```

---

## 8. Middleware Chain Flow

> Shows the full request and response path through the new middleware pipeline introduced in the proxy expansion. See [PRD.md](PRD.md) for requirements.

```mermaid
flowchart TD
    CLIENT([Client\nClaude Code / curl]) -->|POST /v1/messages| PROXY

    subgraph PROXY["openkiro Proxy :1234"]
        subgraph REQ_CHAIN["Request Middleware Chain"]
            TO[ToolOptimizer\nReplace tools≥5 with meta-tool\ninternal/middleware/toolopt.go]
            COMP[Compression\nrtk: compress tool results\ninternal/middleware/compression.go]
            MEM[Memory Recall\nicm: inject past context\ninternal/middleware/memory.go]
            CTX[Context Budget\nheadroom: trim to fit window\ninternal/middleware/context.go]

            TO --> COMP --> MEM --> CTX
        end

        TRANSLATE[Request Translator\ninternal/proxy/request.go]

        subgraph RESP_CHAIN["Response Middleware Chain"]
            TOON[TOON Encoder\nColumnar array encoding\ninternal/middleware/toon.go]
            COMP_RESP[Response Compression\nCompress large text blocks]
            MEM_STORE[Memory Store\nicm: persist assistant turn]

            TOON --> COMP_RESP --> MEM_STORE
        end
    end

    subgraph BACKEND["Backends"]
        CW[CodeWhisperer API\nus-east-1]
        GW[Docker MCP Gateway\n:8080]
    end

    CTX --> TRANSLATE
    TRANSLATE -->|Anthropic→CW format| CW
    CW -->|Binary frames| RESP_CHAIN
    MEM_STORE -->|SSE / JSON| CLIENT

    TO -.->|Route MCP tool calls| GW
    GW -.->|Tool results| TO

    style TO fill:#4a90d9,color:#fff
    style COMP fill:#7b68ee,color:#fff
    style MEM fill:#2ecc71,color:#fff
    style CTX fill:#e67e22,color:#fff
    style TOON fill:#e74c3c,color:#fff
    style COMP_RESP fill:#9b59b6,color:#fff
    style MEM_STORE fill:#27ae60,color:#fff
```

## 9. Updated Component Architecture

> Extends [Diagram 1](#1-component-architecture) with the new internal packages.

```mermaid
graph TB
    subgraph Client["Client Layer"]
        CC[Claude Code]
        OC[OpenClaw]
        CURL[curl / HTTP client]
    end

    subgraph OpenKiro["openkiro binary"]
        CLI[CLI Router\nserver|start|stop|status\nread|refresh|export\ngateway|sandbox|memory]

        subgraph Server["HTTP Proxy Server :1234"]
            MW[Log Middleware]
            BL[Body Limiter]
            MSG[POST /v1/messages]
            MOD[GET /v1/models]
            HP[GET /health]
        end

        subgraph Middleware["internal/middleware/ — NEW"]
            CHAIN[Chain\nchain.go]
            TOOLOPT[ToolOptimizer\ntoolopt.go]
            TOON[TOON Encoder\ntoon.go]
            COMPRESS[Compression\ncompression.go]
            MEMMW[Memory\nmemory.go]
            CTXMW[Context Budget\ncontext.go]
        end

        subgraph Proxy["internal/proxy/ — existing"]
            TR[Request Translator\nrequest.go]
            RS[Response Assembler\nresponse.go]
            SRV[HTTP Server\nserver.go]
            TYPES[Types\ntypes.go]
        end

        subgraph Gateway["internal/gateway/ — NEW"]
            GWC[MCP Gateway Client\ngateway.go]
            DISC[Docker Label Discovery]
            ROUTE[Tool Router]
        end

        subgraph Sandbox["internal/sandbox/ — NEW"]
            SBC[Sandbox Controller\nsandbox.go]
            LIFE[Lifecycle Manager\ncreate/start/exec/destroy]
            ISOL[Isolation Config\nnon-root, no-net]
        end

        subgraph Existing["internal/ — existing"]
            TOKEN[token/\nAuth token management]
            DAEMON[daemon/\nBackground lifecycle]
            PROTOCOL[protocol/\nBinary frame parser]
            SERVICE[service/\nOS service integration]
        end
    end

    subgraph Upstream["Backends"]
        CW[CodeWhisperer API]
        DOCKER[Docker Engine API]
        ICM[icm MCP Server\nsidecar]
    end

    CC & OC & CURL --> MSG
    MSG --> CHAIN
    CHAIN --> TOOLOPT --> COMPRESS --> MEMMW --> CTXMW
    CTXMW --> TR --> CW
    CW --> RS --> CHAIN
    TOOLOPT -.->|MCP tool calls| GWC
    GWC --> DISC & ROUTE
    ROUTE --> DOCKER
    SBC --> LIFE --> DOCKER
    MEMMW --> ICM
    CLI --> DAEMON & TOKEN & Gateway & Sandbox
```

## 10. Memory Integration Sequence

> Shows how icm recall and store hooks integrate with the proxy request lifecycle.

```mermaid
sequenceDiagram
    participant C as Client
    participant P as Proxy :1234
    participant MW as Memory Middleware\n(internal/middleware/memory.go)
    participant ICM as icm MCP Server\n(sidecar)
    participant CW as CodeWhisperer

    C->>P: POST /v1/messages\n{messages: [...], system: [...]}
    P->>MW: Intercept(req)

    MW->>MW: Hash conversation key\nfrom last N message IDs

    MW->>ICM: tools/call recall\n{key: conv_hash, limit: 5}
    ICM-->>MW: [{snippet, relevance, ts}, ...]

    MW->>MW: Prepend recalled memories\nto system[] content blocks
    MW-->>P: req with enriched system[]

    P->>CW: Translated request\n(with injected memories)
    CW-->>P: Response

    P->>MW: PostProcess(resp)
    MW->>MW: Extract assistant turn\nfrom response content

    MW->>ICM: tools/call store\n{key: conv_hash, content: assistant_turn,\n metadata: {model, ts, tokens}}
    ICM-->>MW: {stored: true}

    MW-->>P: resp (unchanged)
    P-->>C: SSE / JSON response

    Note over MW,ICM: All icm calls are async with 50ms timeout.\nOn timeout: log warning, continue without memory.
```

## 11. ToolOptimizer Decision Tree

> Logic for when to compress tool schemas, when to use the meta-tool, and when to pass through unchanged.

```mermaid
flowchart TD
    START([Incoming request\nwith tools\[\]]) --> COUNT{Count tools}

    COUNT -->|"tools < threshold\n(default: 5)"| PASSTHROUGH[Pass through unchanged\nNo schema manipulation]

    COUNT -->|"tools ≥ threshold"| CACHE{Schema hash\nin cache?}

    CACHE -->|Cache hit| LISTING[Generate compact listing\nname + 60-char description]
    CACHE -->|Cache miss| STORE[Store full schemas\nkeyed by content hash]
    STORE --> LISTING

    LISTING --> META[Replace tools\[\] with\nsingle openkiro_tool_call\nmeta-tool]

    META --> INJECT[Inject system prompt:\n"Call openkiro_tool_call\nwith tool_name to execute.\nAvailable: {compact_listing}"]

    INJECT --> FORWARD([Forward to CodeWhisperer])

    FORWARD --> RESP{Response contains\ntool_use block?}

    RESP -->|"tool_name =\nopenkiro_tool_call"| RESOLVE[Resolve real tool\nfrom cache by name]

    RESOLVE --> MCP{Tool backed by\nDocker MCP Gateway?}

    MCP -->|Yes| GW[Route via\nGateway :8080]
    MCP -->|No| LOCAL[Execute locally\nor return error]

    GW & LOCAL --> RESULT[Inject tool_result\ninto conversation]

    RESULT --> CONTINUE([Continue conversation\nwith real result])

    RESP -->|"tool_name ≠\nopenkiro_tool_call"| DIRECT[Pass tool_use\nthrough unchanged]

    style PASSTHROUGH fill:#27ae60,color:#fff
    style META fill:#4a90d9,color:#fff
    style RESOLVE fill:#e67e22,color:#fff
    style GW fill:#9b59b6,color:#fff
```

## 12. Docker Sandbox Lifecycle

> Container lifecycle for ephemeral agent runtime environments.

```mermaid
statediagram-v2
    [*] --> Idle

    Idle --> Creating: openkiro sandbox create\n[--image IMAGE] [--cpu N] [--mem NMB]

    Creating --> Configuring: Pull image if missing\nCreate container with:\n- non-root UID (1000)\n- read-only rootfs\n- no-network flag\n- /workspace volume

    Configuring --> Starting: docker start CONTAINER_ID

    Starting --> Ready: Container healthcheck passes\nSession ID issued to caller

    Ready --> Executing: openkiro sandbox exec\nSESSION_ID COMMAND

    Executing --> Ready: Command completes\nOutput returned to caller

    Ready --> Idle_Timer: No activity for\nidle_timeout (default 30m)
    Idle_Timer --> Destroying: Timeout expired

    Ready --> Destroying: openkiro sandbox destroy SESSION_ID\nor proxy shutdown

    Destroying --> Cleanup: docker stop + docker rm\nRemove /workspace volume\nDelete session record

    Cleanup --> [*]

    state Ready {
        [*] --> Listening
        Listening --> RunCmd: exec request
        RunCmd --> CaptureOutput: docker exec
        CaptureOutput --> Listening: Return stdout+stderr
    }

    note right of Configuring
        Security constraints applied at create time:
        --user 1000:1000
        --read-only
        --network none (default)
        --cpus 1.0 --memory 512m (configurable)
        --tmpfs /tmp:rw,noexec,nosuid
    end note
```
