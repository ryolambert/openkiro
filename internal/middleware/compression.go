// Package middleware defines the Middleware interface and Chain used to
// intercept and transform requests and responses in the openkiro proxy.
package middleware

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/ryolambert/openkiro/internal/proxy"
)

// CompressionLevel controls how aggressively text content is compressed.
//
// Inspired by rtk-ai/rtk context compression strategies.
// See: https://github.com/rtk-ai/rtk
type CompressionLevel int

const (
	// CompressionNone leaves content completely untouched.
	CompressionNone CompressionLevel = iota
	// CompressionLight strips redundant whitespace and normalises line endings.
	CompressionLight
	// CompressionAggressive additionally collapses repeated instructions and
	// truncates overly long tool descriptions.
	CompressionAggressive
)

// defaultMaxToolDescLen is the maximum tool description length applied under
// aggressive compression. It matches the proxy-layer constant for consistency.
const defaultMaxToolDescLen = 200

// reMultiSpace matches two or more consecutive whitespace characters that are
// not newlines (used to collapse inline whitespace).
var reMultiSpace = regexp.MustCompile(`[^\S\n]{2,}`)

// reMultiNewline matches three or more consecutive newline characters.
var reMultiNewline = regexp.MustCompile(`\n{3,}`)

// CompressionMiddleware compresses system prompt and message content to reduce
// token usage before forwarding to CodeWhisperer.
//
// On ProcessRequest it applies the configured compression strategy to all
// system messages and the text content of every request message.
// On ProcessResponse it is a pass-through (no decompression needed).
//
// Inspired by rtk-ai/rtk context compression:
// https://github.com/rtk-ai/rtk
type CompressionMiddleware struct {
	// Level controls how aggressively content is compressed.
	Level CompressionLevel
}

// Name returns "compression".
func (c *CompressionMiddleware) Name() string { return "compression" }

// ProcessRequest compresses the request's system messages and message content
// according to the configured Level, logging before/after byte counts when
// OPENKIRO_DEBUG is enabled.
func (c *CompressionMiddleware) ProcessRequest(req *proxy.AnthropicRequest) (*proxy.AnthropicRequest, error) {
	if c.Level == CompressionNone {
		return req, nil
	}

	before := requestByteCount(req)

	out := *req

	// Compress system messages.
	if len(out.System) > 0 {
		compressed := make([]proxy.AnthropicSystemMessage, len(out.System))
		for i, s := range out.System {
			compressed[i] = proxy.AnthropicSystemMessage{
				Type: s.Type,
				Text: c.compressText(s.Text),
			}
		}
		out.System = compressed
	}

	// Compress message content (text strings only; structured content is left intact).
	if len(out.Messages) > 0 {
		msgs := make([]proxy.AnthropicRequestMessage, len(out.Messages))
		for i, m := range out.Messages {
			msgs[i] = proxy.AnthropicRequestMessage{
				Role:    m.Role,
				Content: c.compressContent(m.Content),
			}
		}
		out.Messages = msgs
	}

	// Under aggressive compression, truncate tool descriptions.
	if c.Level == CompressionAggressive && len(out.Tools) > 0 {
		tools := make([]proxy.AnthropicTool, len(out.Tools))
		for i, t := range out.Tools {
			tools[i] = proxy.AnthropicTool{
				Name:        t.Name,
				Description: proxy.TruncateString(t.Description, defaultMaxToolDescLen),
				InputSchema: t.InputSchema,
			}
		}
		out.Tools = tools
	}

	after := requestByteCount(&out)
	compressionDebugLogf("[compression] level=%d before=%d after=%d reduction=%.2f%%",
		c.Level, before, after, 100*safeDivide(float64(before-after), float64(before)))

	return &out, nil
}

// ProcessResponse is a pass-through; responses do not need decompression.
func (c *CompressionMiddleware) ProcessResponse(resp []byte) ([]byte, error) {
	return resp, nil
}

// compressText applies compression to a single string according to the Level.
func (c *CompressionMiddleware) compressText(s string) string {
	if s == "" {
		return s
	}
	switch c.Level {
	case CompressionLight:
		return lightCompress(s)
	case CompressionAggressive:
		return aggressiveCompress(s)
	default:
		return s
	}
}

// compressContent handles both string and []interface{} message content.
func (c *CompressionMiddleware) compressContent(content any) any {
	switch v := content.(type) {
	case string:
		return c.compressText(v)
	case []interface{}:
		out := make([]interface{}, len(v))
		for i, block := range v {
			if m, ok := block.(map[string]interface{}); ok {
				if blockType, _ := m["type"].(string); blockType == "text" {
					if text, ok := m["text"].(string); ok {
						cp := make(map[string]interface{}, len(m))
						for k, val := range m {
							cp[k] = val
						}
						cp["text"] = c.compressText(text)
						out[i] = cp
						continue
					}
				}
			}
			out[i] = block
		}
		return out
	default:
		return content
	}
}

// lightCompress strips redundant whitespace while preserving meaningful line
// breaks: collapses multiple spaces on the same line and reduces runs of
// three or more blank lines to a single blank line.
func lightCompress(s string) string {
	// Normalise Windows line endings.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	// Collapse multiple inline spaces to one.
	s = reMultiSpace.ReplaceAllString(s, " ")
	// Collapse excessive blank lines.
	s = reMultiNewline.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// aggressiveCompress applies lightCompress and then additionally collapses
// repeated adjacent lines (instructions that appear more than once consecutively).
func aggressiveCompress(s string) string {
	s = lightCompress(s)
	lines := strings.Split(s, "\n")
	deduped := make([]string, 0, len(lines))
	for i, line := range lines {
		if i > 0 && strings.TrimSpace(line) != "" && line == lines[i-1] {
			continue // drop exact duplicate adjacent line
		}
		deduped = append(deduped, line)
	}
	return strings.Join(deduped, "\n")
}

// requestByteCount returns a rough byte estimate for the request content
// without incurring the cost of full JSON serialisation.
func requestByteCount(req *proxy.AnthropicRequest) int {
	n := 0
	for _, s := range req.System {
		n += len(s.Text)
	}
	for _, m := range req.Messages {
		switch v := m.Content.(type) {
		case string:
			n += len(v)
		case []interface{}:
			for _, b := range v {
				if bm, ok := b.(map[string]interface{}); ok {
					if t, ok := bm["text"].(string); ok {
						n += len(t)
					}
				}
			}
		}
	}
	for _, t := range req.Tools {
		n += len(t.Description)
	}
	return n
}

// safeDivide returns numerator/denominator, or 0 if denominator is zero.
func safeDivide(numerator, denominator float64) float64 {
	if denominator == 0 {
		return 0
	}
	return numerator / denominator
}

// compressionDebugLogf mirrors the debugLogf pattern from proxy/request.go.
func compressionDebugLogf(format string, args ...any) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OPENKIRO_DEBUG"))) {
	case "1", "true", "yes", "on", "debug":
		fmt.Fprintf(os.Stderr, format+"\n", args...)
	}
}
