---
applyTo: "{internal/proxy/**,internal/protocol/**}"
---
# Proxy & Protocol Guidelines

## Anthropic ↔ CodeWhisperer Translation

### Request Translation (`proxy.BuildCodeWhispererRequest`)
- Converts `AnthropicRequest.Messages` into CW `conversationState` format.
- The **last** message becomes the current `userInputMessage`; all prior messages become `history`.
- Multi-content messages (tool results, image blocks) require iterating `Content.([]ContentBlock)`.
- Tool schemas must be stripped of unsupported fields before forwarding; see `EnsurePayloadFits`.

### Response Assembly (`proxy.AssembleAnthropicResponse`)
- Collects SSE events from `protocol.ParseEvents(cwBody)`.
- Assembles `content_block_delta` events into a final `TranslatedAnthropicResponse`.
- Non-streaming path: `io.ReadAll` then `ParseEvents` then `BuildAnthropicResponsePayload`.

## Model Resolution (`proxy.ResolveModelID`)

Resolution is tried in this exact order — **do not reorder**:

1. **Exact match** — case-insensitive lookup in `ModelMap` (defined in `types.go`)
   - The literal string `"default"` maps to `ModelSonnet45`.
2. **Prefix passthrough** — if the requested ID starts with `CLAUDE_` (uppercase) it is
   returned as-is without mapping.
3. **Fuzzy match** — keyword heuristics:
   - Contains "sonnet" + ("4-5" or "4.5") → `ModelSonnet45`
   - Contains "sonnet" → `ModelSonnet46`
   - Contains "opus" → `ModelOpus46`
   - Contains "haiku" → `ModelHaiku45`
4. **Default** — empty string or no match → `ModelSonnet46`.

When adding a new model:
1. Add a `const Model<Name>` in `types.go`.
2. Add it to `ModelMap` with all expected alias strings as keys.
3. Add a fuzzy rule if needed.
4. Add test cases to `TestResolveModelID` in `request_test.go`.

## CodeWhisperer Binary Frame Parser (`protocol.ParseEventStream`)

The parser processes CodeWhisperer's custom binary event-stream format:

```
[4B total-length][4B header-length][N header-bytes][M payload-bytes][4B CRC]
```

Implementation rules:
- **Header bytes are opaque** — the parser does NOT interpret individual header
  key-value pairs. Event type is inferred from the JSON payload content.
- **CRC is read but not validated** — the 4 trailing bytes are consumed to advance
  the reader; no checksum is performed.
- The callback receives each `SSEEvent` as it is parsed; do not buffer all events.
- `ParseEvents` (non-streaming variant) collects all events into a slice.

## SSE Streaming Output

The proxy outputs standard `text/event-stream` format:
```
event: <type>
data: <json>

```
(Two newlines end each event — an empty line separates events.)

### Event sequence for a complete streaming response:
```
message_start        → {type, message:{id, role, model, usage}}
ping                 → {type:"ping"}
content_block_start  → {type, index, content_block:{type:"text",text:""}}
content_block_delta* → {type, index, delta:{type:"text_delta",text:"..."}}
content_block_stop   → {type, index}
message_delta        → {type, delta:{stop_reason}, usage:{output_tokens}}
message_stop         → {type:"message_stop"}
```

## Retry Logic

The proxy retries upstream requests up to **3 times**:

| Condition | Action |
|-----------|--------|
| HTTP 400 "Improperly formed" | Trim message history + simplify tool schemas → retry |
| HTTP 403 | `token.RefreshToken()` → re-read token → retry |
| Other errors | Emit error SSE event → return (no retry) |

- Never retry indefinitely; the cap is hardcoded at 3.
- Token refresh on 403 must not loop: if refresh fails, propagate the error.

## UUID Generation

`proxy.newConversationID()` generates conversation IDs using `uuidEntropySource`
(package-level var, injectable for deterministic tests). Fallback uses an atomic
serial counter appended to a constant prefix.

```go
// In tests, replace the entropy source:
uuidEntropySource = func(b []byte) (int, error) {
    copy(b, []byte("deterministic-seed-bytes"))
    return len(b), nil
}
t.Cleanup(func() { uuidEntropySource = rand.Read })
```

## Payload Size Management (`proxy.EnsurePayloadFits`)

There are **two distinct size limits** — do not confuse them:

| Limit | Constant | Value | Direction |
|-------|----------|-------|-----------|
| `MaxRequestBodyBytes` | `proxy.MaxRequestBodyBytes` | 200 MiB (`200 << 20`) | Inbound from client; enforced via `http.MaxBytesReader` |
| `MaxPayloadBytes` | `proxy.MaxPayloadBytes` | ~250 MB (`250_000_000`) | Outgoing to CodeWhisperer; enforced by `EnsurePayloadFits` |

`EnsurePayloadFits` behavior:
- Marshals the CW request to JSON and checks against `MaxPayloadBytes`.
- If the payload exceeds the limit, trims the oldest history entries until it fits.
- Tool schemas are simplified (InputSchema stripped to the bare minimum) if trimming alone is not enough.
- This runs **before** the HTTP call; no recovery needed after.

## HTTP Handler Patterns

```go
// All handlers must:
// 1. Use http.MaxBytesReader (already set in server.go; don't remove it)
// 2. Set Content-Type before writing the body
// 3. Flush after each SSE event when streaming
w.Header().Set("Content-Type", "text/event-stream")
w.Header().Set("Cache-Control", "no-cache")
w.Header().Set("Connection", "keep-alive")
fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, jsonData)
if f, ok := w.(http.Flusher); ok {
    f.Flush()
}
```

## Anti-Patterns

- **Do not** use `json.Decoder.Buffered()` and then `io.ReadAll` on the same reader —
  use one or the other per request body.
- **Do not** write to `w` after a `http.Error()` call — the headers are already sent.
- **Do not** hold locks while making upstream HTTP calls — deadlock risk.
- **Do not** log upstream URLs or response headers that may contain token fragments.
