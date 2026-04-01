package middleware

import (
	"bytes"
	"context"
	"math"
	"os/exec"
	"strings"
	"time"

	"github.com/ryolambert/openkiro/internal/proxy"
	"github.com/ryolambert/openkiro/internal/token"
)

// charsPerToken is the average English characters-per-token heuristic used by
// Anthropic and OpenAI tokenizers. Matches the constant in cmd/rtk/main.go.
const charsPerToken = 4

// DefaultTokenThreshold is the minimum estimated token count a content block
// must reach before compression is applied.
const DefaultTokenThreshold = 200

// defaultRtkTimeout is the maximum time allowed for a single rtk subprocess
// invocation before the context is cancelled.
const defaultRtkTimeout = 10 * time.Second

// Compressor compresses a text string and returns the compressed version.
// Implementations must be safe for concurrent use.
type Compressor interface {
	Compress(text string) string
}

// RtkCompressor delegates text compression to the rtk binary (rtk-ai/rtk).
// If the binary is not found on $PATH, Compress returns text unchanged.
type RtkCompressor struct {
	binaryPath string // resolved via exec.LookPath; empty means unavailable
	timeout    time.Duration
}

// resolveRtkBinary locates the rtk binary on $PATH and verifies it is the
// Rust rtk-ai/rtk binary (not the Go cmd/rtk shim). It returns the resolved
// path, or empty string if the binary is not found or is the wrong one.
func resolveRtkBinary() string {
	path, err := exec.LookPath("rtk")
	if err != nil {
		return ""
	}

	// Verify this is the Rust rtk-ai/rtk binary by checking --version output.
	// The Go cmd/rtk tool prints "openkiro token compression toolkit" while
	// the Rust binary prints something like "rtk 0.34.2".
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	//nolint:gosec // path is resolved via LookPath, not user input.
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return ""
	}

	version := strings.ToLower(strings.TrimSpace(string(out)))
	// The Rust binary outputs "rtk <version>"; the Go shim mentions "openkiro".
	if strings.Contains(version, "openkiro") {
		return ""
	}
	if !strings.Contains(version, "rtk") {
		return ""
	}

	return path
}

// NewRtkCompressor creates an RtkCompressor that delegates to the rtk binary.
// If the rtk binary is not found or is the wrong variant, the compressor
// operates in passthrough mode (Compress returns text unchanged).
func NewRtkCompressor() *RtkCompressor {
	return &RtkCompressor{
		binaryPath: resolveRtkBinary(),
		timeout:    defaultRtkTimeout,
	}
}

// Available reports whether the rtk binary was found and verified at
// construction time.
func (r *RtkCompressor) Available() bool {
	return r.binaryPath != ""
}

// Compress runs the text through `rtk read --level minimal` via subprocess and
// returns the compressed output. On ANY error (binary not found, timeout,
// non-zero exit, etc.) the original text is returned unchanged (graceful
// degradation).
func (r *RtkCompressor) Compress(text string) string {
	if text == "" {
		return text
	}
	if r.binaryPath == "" {
		return text
	}

	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()

	//nolint:gosec // binaryPath is resolved via LookPath at construction time.
	cmd := exec.CommandContext(ctx, r.binaryPath, "read", "--level", "minimal")
	cmd.Stdin = strings.NewReader(text)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		token.DebugLogf("rtk compress error: %v (stderr: %s)", err, stderr.String())
		return text
	}

	result := stdout.String()
	if result == "" {
		// If rtk returned empty output, keep the original to avoid data loss.
		return text
	}

	return result
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
// requests using the rtk binary subprocess. It implements the Middleware interface.
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
		compressor: NewRtkCompressor(),
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

// copyStringMap creates a shallow copy of a map[string]interface{}. This is
// sufficient for content block maps where values are primitives (strings,
// numbers, bools) or are replaced entirely (e.g. the "content" or "text"
// field). Nested reference types beyond "content" are shared with the
// original; this is acceptable because only string fields are modified.
func copyStringMap(src map[string]interface{}) map[string]interface{} {
	dst := make(map[string]interface{}, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
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
		blockCopy := copyStringMap(block)

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
							subCopy := copyStringMap(subBlock)
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

// Ensure RtkCompressor satisfies the Compressor interface at compile time.
var _ Compressor = (*RtkCompressor)(nil)

// Ensure CompressionMiddleware satisfies the Middleware interface at compile time.
var _ Middleware = (*CompressionMiddleware)(nil)