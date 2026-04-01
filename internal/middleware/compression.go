package middleware

import (
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/ryolambert/openkiro/internal/proxy"
	"github.com/ryolambert/openkiro/internal/token"
)

// charsPerToken is the average English characters-per-token heuristic used by
// Anthropic and OpenAI tokenizers. Matches the constant in cmd/rtk/main.go.
const charsPerToken = 4

// DefaultMaxLineLen is the maximum line length before truncation.
const DefaultMaxLineLen = 500

// DefaultTokenThreshold is the minimum estimated token count a content block
// must reach before compression is applied.
const DefaultTokenThreshold = 200

// Compressor compresses a text string and returns the compressed version.
// Implementations must be safe for concurrent use.
type Compressor interface {
	Compress(text string) string
}

// DefaultCompressor applies a chain of pure-Go text filters to reduce token
// count without losing semantic content. It is safe for concurrent use because
// all operations are stateless.
//
// Filters applied in order:
//  1. Strip ANSI escape codes
//  2. Strip trailing whitespace from every line
//  3. Collapse consecutive blank lines into a single blank line
//  4. Deduplicate consecutive identical lines (e.g. "line\nline\nline" → "line (×3)")
//  5. Truncate lines exceeding MaxLineLen characters
type DefaultCompressor struct {
	// MaxLineLen is the maximum number of characters per line before
	// truncation. Zero means use DefaultMaxLineLen.
	MaxLineLen int
}

// ansiPattern matches ANSI escape sequences (CSI, OSC, and simple ESC codes).
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\][^\x1b\x07]*(?:\x07|\x1b\\)|\x1b[^[\]()]`)

// Compress applies all text filters and returns the compressed result.
func (d *DefaultCompressor) Compress(text string) string {
	if text == "" {
		return text
	}

	maxLen := d.MaxLineLen
	if maxLen <= 0 {
		maxLen = DefaultMaxLineLen
	}

	// 1. Strip ANSI escape codes.
	text = ansiPattern.ReplaceAllString(text, "")

	lines := strings.Split(text, "\n")

	// 2. Strip trailing whitespace from every line.
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t\r")
	}

	// 3. Collapse consecutive blank lines into a single blank line.
	lines = collapseBlankLines(lines)

	// 4. Deduplicate consecutive identical lines.
	lines = deduplicateLines(lines)

	// 5. Truncate long lines.
	for i, line := range lines {
		if len(line) > maxLen {
			lines[i] = line[:maxLen] + "…[truncated]"
		}
	}

	return strings.Join(lines, "\n")
}

// collapseBlankLines reduces runs of consecutive blank lines to a single blank
// line.
func collapseBlankLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	prevBlank := false
	for _, line := range lines {
		blank := strings.TrimSpace(line) == ""
		if blank && prevBlank {
			continue
		}
		out = append(out, line)
		prevBlank = blank
	}
	return out
}

// deduplicateLines replaces runs of N consecutive identical lines with a
// single line annotated with " (×N)".
func deduplicateLines(lines []string) []string {
	if len(lines) == 0 {
		return lines
	}
	out := make([]string, 0, len(lines))
	i := 0
	for i < len(lines) {
		j := i + 1
		for j < len(lines) && lines[j] == lines[i] {
			j++
		}
		count := j - i
		if count > 1 {
			out = append(out, fmt.Sprintf("%s (×%d)", lines[i], count))
		} else {
			out = append(out, lines[i])
		}
		i = j
	}
	return out
}

// estimateTokens returns a rough token count using the 4 chars/token heuristic.
// Matches cmd/rtk/main.go's EstimateTokens.
func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return int(math.Ceil(float64(len(s)) / charsPerToken))
}

// CompressionMiddleware compresses tool_result content blocks in Anthropic
// requests using pure-Go text filters. It implements the Middleware interface.
//
// Only content blocks whose estimated token count meets or exceeds the
// configured threshold are compressed. When disabled or when compression
// encounters an error, the original request is returned unchanged.
type CompressionMiddleware struct {
	compressor Compressor
	threshold  int
	enabled    bool
}

// CompressionOption configures a CompressionMiddleware.
type CompressionOption func(*CompressionMiddleware)

// WithCompressor sets a custom Compressor implementation.
func WithCompressor(c Compressor) CompressionOption {
	return func(m *CompressionMiddleware) {
		if c != nil {
			m.compressor = c
		}
	}
}

// WithThreshold sets the minimum estimated token count for a block to be
// eligible for compression.
func WithThreshold(n int) CompressionOption {
	return func(m *CompressionMiddleware) {
		m.threshold = n
	}
}

// NewCompressionMiddleware creates a CompressionMiddleware. If enabled is false
// the middleware is a no-op. Options can override the default compressor and
// token threshold.
func NewCompressionMiddleware(enabled bool, opts ...CompressionOption) *CompressionMiddleware {
	m := &CompressionMiddleware{
		compressor: &DefaultCompressor{},
		threshold:  DefaultTokenThreshold,
		enabled:    enabled,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Name returns "compression".
func (c *CompressionMiddleware) Name() string { return "compression" }

// ProcessRequest compresses tool_result content blocks in the Anthropic
// request. If the middleware is disabled or the threshold is ≤ 0, the request
// is returned unchanged. On any internal error the original request is
// returned (graceful degradation).
func (c *CompressionMiddleware) ProcessRequest(req *proxy.AnthropicRequest) (*proxy.AnthropicRequest, error) {
	if !c.enabled || c.threshold <= 0 {
		return req, nil
	}
	if len(req.Messages) == 0 {
		return req, nil
	}

	// Shallow copy to avoid mutating the caller's request.
	out := *req
	out.Messages = make([]proxy.AnthropicRequestMessage, len(req.Messages))
	copy(out.Messages, req.Messages)

	totalBefore := 0
	totalAfter := 0
	blocksCompressed := 0

	for i := range out.Messages {
		msg := &out.Messages[i]
		switch content := msg.Content.(type) {
		case []interface{}:
			compressed, before, after, n := c.compressBlocks(content)
			if n > 0 {
				msg.Content = compressed
				totalBefore += before
				totalAfter += after
				blocksCompressed += n
			}
		case string:
			if msg.Role == "user" {
				before := estimateTokens(content)
				if before >= c.threshold {
					after := c.compressor.Compress(content)
					afterTokens := estimateTokens(after)
					if afterTokens < before {
						msg.Content = after
						totalBefore += before
						totalAfter += afterTokens
						blocksCompressed++
					}
				}
			}
		}
	}

	if blocksCompressed > 0 {
		token.DebugLogf("compression: compressed %d blocks, %d→%d tokens (saved %d)",
			blocksCompressed, totalBefore, totalAfter, totalBefore-totalAfter)
	}

	return &out, nil
}

// compressBlocks processes a slice of content blocks (as []interface{}) and
// compresses eligible tool_result text fields. It returns the modified blocks,
// total tokens before/after compression, and the number of blocks compressed.
func (c *CompressionMiddleware) compressBlocks(blocks []interface{}) ([]interface{}, int, int, int) {
	// Deep-copy the blocks slice so the original is never mutated.
	out := make([]interface{}, len(blocks))
	copy(out, blocks)

	totalBefore := 0
	totalAfter := 0
	count := 0

	for i, raw := range out {
		block, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}

		blockType, _ := block["type"].(string)
		if blockType != "tool_result" {
			continue
		}

		// Copy the block map to avoid mutating the original.
		blockCopy := make(map[string]interface{}, len(block))
		for k, v := range block {
			blockCopy[k] = v
		}

		changed := false

		// Case 1: tool_result with "content" as a string.
		if content, ok := blockCopy["content"].(string); ok {
			before := estimateTokens(content)
			if before >= c.threshold {
				after := c.compressor.Compress(content)
				afterTokens := estimateTokens(after)
				if afterTokens < before {
					blockCopy["content"] = after
					totalBefore += before
					totalAfter += afterTokens
					changed = true
				}
			}
		}

		// Case 2: tool_result with "text" as a string.
		if text, ok := blockCopy["text"].(string); ok {
			before := estimateTokens(text)
			if before >= c.threshold {
				after := c.compressor.Compress(text)
				afterTokens := estimateTokens(after)
				if afterTokens < before {
					blockCopy["text"] = after
					totalBefore += before
					totalAfter += afterTokens
					changed = true
				}
			}
		}

		// Case 3: tool_result with "content" as nested []interface{} of sub-blocks.
		if nested, ok := blockCopy["content"].([]interface{}); ok {
			nestedCopy := make([]interface{}, len(nested))
			copy(nestedCopy, nested)
			for j, sub := range nestedCopy {
				subBlock, ok := sub.(map[string]interface{})
				if !ok {
					continue
				}
				if text, ok := subBlock["text"].(string); ok {
					before := estimateTokens(text)
					if before >= c.threshold {
						after := c.compressor.Compress(text)
						afterTokens := estimateTokens(after)
						if afterTokens < before {
							subCopy := make(map[string]interface{}, len(subBlock))
							for k, v := range subBlock {
								subCopy[k] = v
							}
							subCopy["text"] = after
							nestedCopy[j] = subCopy
							totalBefore += before
							totalAfter += afterTokens
							changed = true
						}
					}
				}
			}
			if changed {
				blockCopy["content"] = nestedCopy
			}
		}

		if changed {
			out[i] = blockCopy
			count++
		}
	}

	return out, totalBefore, totalAfter, count
}

// ProcessResponse is a no-op — compression only applies to inbound requests.
func (c *CompressionMiddleware) ProcessResponse(resp []byte) ([]byte, error) {
	return resp, nil
}
