# Headroom Integration

[Headroom](https://github.com/chopratejas/headroom) is an intelligent context
compression library that reduces token usage by 47–92 % while preserving answer
accuracy. openkiro integrates headroom as an optional sidecar proxy — the
headroom Python process runs alongside the Go proxy and compresses every request
before it reaches AWS CodeWhisperer.

## How It Works

```
Client (Claude Code, Cursor, …)
  │
  │  Anthropic API request
  ▼
openkiro Go proxy  (:1234)
  │
  │  /v1/compress  ──►  headroom Python proxy  (:8787)
  │                          │
  │  compressed messages  ◄──┘
  │
  ▼
AWS CodeWhisperer
```

1. The client sends a standard Anthropic request to openkiro.
2. The **HeadroomMiddleware** extracts the messages, forwards them to
   headroom's `/v1/compress` endpoint for compression.
3. Compressed messages replace the originals in the request.
4. The request is translated and forwarded to CodeWhisperer as usual.
5. If headroom is unavailable the middleware silently passes through — requests
   are never blocked.

## Quick Start

### 1. Install headroom

**macOS / Linux:**

```bash
scripts/headroom-install.sh
```

**Windows (PowerShell):**

```powershell
.\scripts\headroom-install.ps1
```

**Manual:**

```bash
pip install "headroom-ai[proxy]"
```

> Requires Python 3.10+.

### 2. Start headroom

```bash
headroom proxy --port 8787
```

### 3. Start openkiro

```bash
openkiro server
```

To use headroom with openkiro you must enable and configure the
`HeadroomMiddleware` in your server setup (for example, in your Go proxy
configuration). The openkiro proxy does **not** currently read any
`HEADROOM_*` environment variables by itself; setting these variables alone
will not cause headroom to be enabled.

## Configuration

The following environment variables are **suggested conventions only**. They
are not consumed directly by the openkiro proxy, but you can use them in your
own configuration code to control how `HeadroomMiddleware` is initialized.

| Environment Variable | Default | Description |
|---------------------|---------|-------------|
| `HEADROOM_ENABLED` | `false` | Suggested flag you can read to enable headroom compression middleware |
| `HEADROOM_HOST` | `127.0.0.1` | Suggested host variable for where your headroom proxy is running |
| `HEADROOM_PORT` | `8787` | Suggested port variable for where your headroom proxy is running |

## Architecture

### Go Package: `internal/headroom/`

| File | Purpose |
|------|---------|
| `config.go` | `Config` struct and `DefaultConfig()` |
| `client.go` | HTTP client for `/v1/compress` and `/health` |
| `manager.go` | Process lifecycle: `Install()`, `Start()`, `Stop()`, `Installed()`, `PythonAvailable()` |

### Middleware: `internal/middleware/headroom.go`

`HeadroomMiddleware` implements the standard `Middleware` interface:

- **`ProcessRequest`** — converts Anthropic messages → headroom format,
  calls `/v1/compress`, maps compressed messages back.
- **`ProcessResponse`** — no-op pass-through (headroom only compresses inputs).
- **Graceful fallback** — if headroom is down, logs a warning and returns the
  original request unchanged.

### Install Scripts

| Script | Platform |
|--------|----------|
| `scripts/headroom-install.sh` | macOS / Linux |
| `scripts/headroom-install.ps1` | Windows |

Both scripts:
- Detect Python 3.10+ and pip
- Install `headroom-ai[proxy]` (or `headroom-ai[all]` with `--full`)
- Verify the `headroom` CLI is on PATH
- Support `--check` to test if headroom is already installed

## Usage Examples

### Using the Manager from Go

```go
import "github.com/ryolambert/openkiro/internal/headroom"

cfg := headroom.DefaultConfig()
mgr := headroom.NewManager(cfg)

// Check prerequisites
if !mgr.PythonAvailable() {
    log.Fatal("Python 3.10+ is required for headroom")
}

// Install (if needed)
if !mgr.Installed() {
    if err := mgr.Install(ctx); err != nil {
        log.Fatal(err)
    }
}

// Start the sidecar proxy
if err := mgr.Start(ctx); err != nil {
    log.Fatal(err)
}
defer mgr.Stop()
```

### Using the Middleware in a Chain

```go
import (
    "github.com/ryolambert/openkiro/internal/headroom"
    "github.com/ryolambert/openkiro/internal/middleware"
)

cfg := headroom.DefaultConfig()
client := headroom.NewClient(cfg)

var chain middleware.Chain
chain.Add(middleware.NewHeadroomMiddleware(client, true))

// In your request handler:
compressed, err := chain.ProcessRequest(anthropicReq)
```

### Calling the Client Directly

```go
client := headroom.NewClient(headroom.DefaultConfig())

msgs := []headroom.Message{
    {Role: "user", Content: "Analyze this log output"},
    {Role: "assistant", Content: "Let me check..."},
    {Role: "user", Content: hugeLogOutput},
}

result, err := client.Compress(ctx, msgs, "claude-sonnet-4-5")
// result.TokensSaved, result.CompressionRatio, result.Messages
```

## Headroom Proxy Options

The headroom proxy supports many useful options:

```bash
headroom proxy --port 8787                     # Default
headroom proxy --no-intelligent-context        # Simpler context management
headroom proxy --llmlingua                     # ML-based compression (needs GPU)
headroom proxy --budget 100.0                  # Daily budget limit
headroom proxy --log-file /var/log/headroom.jsonl  # Request logging
```

See the [headroom proxy documentation](https://github.com/chopratejas/headroom/blob/main/docs/proxy.md)
for all available options.

## Limitations

- **Python dependency** — headroom requires Python 3.10+ and pip.
- **Code is mostly passed through** — headroom intentionally does not compress
  code in recent messages (it's usually there because the user needs it).
- **Latency** — adds 15–200 ms per request for compression. This is usually
  offset by the reduced token processing time on the LLM side.
- **No response compression** — headroom only compresses inputs; responses are
  returned unchanged.

See the [headroom limitations](https://github.com/chopratejas/headroom/blob/main/docs/LIMITATIONS.md)
document for the full list.
