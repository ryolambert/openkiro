package middleware

import (
	"context"
	"encoding/json"
	"log"

	"github.com/ryolambert/openkiro/internal/headroom"
	"github.com/ryolambert/openkiro/internal/proxy"
	"github.com/ryolambert/openkiro/internal/token"
)

// HeadroomMiddleware compresses request messages via the headroom Python proxy
// before they are forwarded to CodeWhisperer. It implements the Middleware
// interface.
//
// When the headroom proxy is unreachable the middleware falls back to a
// passthrough so requests are never blocked.
type HeadroomMiddleware struct {
	client  *headroom.Client
	enabled bool
}

// NewHeadroomMiddleware creates a HeadroomMiddleware backed by the given
// headroom client. If enabled is false the middleware is a no-op.
func NewHeadroomMiddleware(client *headroom.Client, enabled bool) *HeadroomMiddleware {
	return &HeadroomMiddleware{
		client:  client,
		enabled: enabled,
	}
}

// Name returns "headroom".
func (h *HeadroomMiddleware) Name() string { return "headroom" }

// ProcessRequest compresses the messages in the Anthropic request via
// headroom's /v1/compress endpoint. If the request fails, the original
// request is returned unchanged.
func (h *HeadroomMiddleware) ProcessRequest(req *proxy.AnthropicRequest) (*proxy.AnthropicRequest, error) {
	if !h.enabled || h.client == nil {
		return req, nil
	}

	msgs := anthropicToHeadroomMessages(req)
	if len(msgs) == 0 {
		return req, nil
	}

	result, err := h.client.Compress(context.Background(), msgs, req.Model)
	if err != nil {
		// Graceful degradation: log and proceed without compression.
		log.Printf("headroom: compression failed, falling back to passthrough: %v", err)
		return req, nil
	}

	token.DebugLogf("headroom: compressed %d→%d tokens (saved %d, ratio %.0f%%)",
		result.TokensBefore, result.TokensAfter, result.TokensSaved,
		result.CompressionRatio*100)

	compressed := applyCompressedMessages(req, result.Messages)
	return compressed, nil
}

// ProcessResponse is a no-op — headroom only compresses requests.
func (h *HeadroomMiddleware) ProcessResponse(resp []byte) ([]byte, error) {
	return resp, nil
}

// anthropicToHeadroomMessages converts Anthropic messages into the flat
// OpenAI-style message list that headroom's /v1/compress endpoint expects.
func anthropicToHeadroomMessages(req *proxy.AnthropicRequest) []headroom.Message {
	var msgs []headroom.Message

	// Include system messages as a single "system" role message.
	if len(req.System) > 0 {
		var systemText string
		for _, s := range req.System {
			if systemText != "" {
				systemText += "\n"
			}
			systemText += s.Text
		}
		msgs = append(msgs, headroom.Message{Role: "system", Content: systemText})
	}

	for _, m := range req.Messages {
		msgs = append(msgs, headroom.Message{
			Role:    m.Role,
			Content: messageContent(m),
		})
	}

	return msgs
}

// messageContent extracts the content from an AnthropicRequestMessage.
// The Content field is either a plain string or a structured value (e.g.
// []ContentBlock for tool_use / tool_result). Structured content is
// preserved as-is so downstream code can still inspect typed blocks.
func messageContent(m proxy.AnthropicRequestMessage) any {
	return m.Content
}

// applyCompressedMessages rebuilds the AnthropicRequest using the compressed
// messages from headroom. System messages are extracted back out, and user/
// assistant messages replace the originals.
func applyCompressedMessages(original *proxy.AnthropicRequest, compressed []headroom.Message) *proxy.AnthropicRequest {
	// Preserve originals before mutation for the fallback path.
	origMessages := original.Messages
	origSystem := original.System

	out := *original // shallow copy
	out.Messages = nil
	out.System = nil

	for _, m := range compressed {
		if m.Role == "system" {
			var text string
			switch v := m.Content.(type) {
			case string:
				text = v
			default:
				// Non-string system content: marshal to JSON string so the
				// system prompt is never silently dropped.
				b, err := json.Marshal(v)
				if err == nil {
					text = string(b)
				}
			}
			out.System = append(out.System, proxy.AnthropicSystemMessage{
				Type: "text",
				Text: text,
			})
			continue
		}
		out.Messages = append(out.Messages, proxy.AnthropicRequestMessage{
			Role:    m.Role,
			Content: m.Content,
		})
	}

	// If compression removed all messages, fall back to originals.
	if len(out.Messages) == 0 {
		out.Messages = origMessages
		out.System = origSystem
	}

	return &out
}
