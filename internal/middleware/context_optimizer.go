// Package middleware defines the Middleware interface and Chain used to
// intercept and transform requests and responses in the openkiro proxy.
package middleware

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ryolambert/openkiro/internal/proxy"
)

const (
	// charsPerToken is the simple heuristic used to estimate token count:
	// approximately 4 characters per token.
	//
	// Inspired by chopratejas/headroom context optimization:
	// https://github.com/chopratejas/headroom
	charsPerToken = 4

	// defaultContextBudget is the default maximum token budget (128k tokens).
	defaultContextBudget = 128_000

	// minHistoryKeep is the minimum number of history messages to retain even
	// when trimming aggressively.
	minHistoryKeep = 2
)

// ContextOptimizerMiddleware optimises overall context window usage by
// progressively trimming the request when its estimated token count exceeds a
// configurable budget.
//
// Trimming strategy (applied in order until the budget is met):
//  1. Drop oldest history messages using KeepMostRecentHistory.
//  2. Compress every system message with aggressiveCompress.
//  3. Truncate tool descriptions to 100 characters.
//
// Inspired by chopratejas/headroom context optimization:
// https://github.com/chopratejas/headroom
type ContextOptimizerMiddleware struct {
	// TokenBudget is the maximum allowed estimated token count.
	// Defaults to defaultContextBudget (128k) when zero.
	TokenBudget int
}

// Name returns "context-optimizer".
func (c *ContextOptimizerMiddleware) Name() string { return "context-optimizer" }

// ProcessRequest estimates the token count of req and, if it exceeds the
// configured budget, progressively trims the request until it fits.
func (c *ContextOptimizerMiddleware) ProcessRequest(req *proxy.AnthropicRequest) (*proxy.AnthropicRequest, error) {
	budget := c.TokenBudget
	if budget <= 0 {
		budget = defaultContextBudget
	}

	estimated := estimateTokens(req)
	if estimated <= budget {
		contextDebugLogf("[context-optimizer] estimated=%d budget=%d — no trimming needed", estimated, budget)
		return req, nil
	}

	contextDebugLogf("[context-optimizer] estimated=%d budget=%d — trimming", estimated, budget)

	out := *req

	// Step 1: Trim oldest history by progressively halving the kept count.
	// Operate directly on []AnthropicRequestMessage to avoid repeated []any conversions.
	if estimated > budget && len(out.Messages) > minHistoryKeep+1 {
		last := out.Messages[len(out.Messages)-1]
		history := out.Messages[:len(out.Messages)-1]
		keep := len(history)
		for estimated > budget && keep > minHistoryKeep {
			keep = keep / 2
			if keep < minHistoryKeep {
				keep = minHistoryKeep
			}
			if keep < len(history) {
				history = history[len(history)-keep:]
			}
			trimmed := make([]proxy.AnthropicRequestMessage, len(history)+1)
			copy(trimmed, history)
			trimmed[len(trimmed)-1] = last
			out.Messages = trimmed
			estimated = estimateTokens(&out)
			contextDebugLogf("[context-optimizer] after history trim keep=%d estimated=%d", keep, estimated)
		}
	}

	// Step 2: Compress system messages aggressively.
	if estimated > budget && len(out.System) > 0 {
		compressed := make([]proxy.AnthropicSystemMessage, len(out.System))
		for i, s := range out.System {
			compressed[i] = proxy.AnthropicSystemMessage{
				Type: s.Type,
				Text: aggressiveCompress(s.Text),
			}
		}
		out.System = compressed
		estimated = estimateTokens(&out)
		contextDebugLogf("[context-optimizer] after system compress estimated=%d", estimated)
	}

	// Step 3: Truncate tool descriptions.
	if estimated > budget && len(out.Tools) > 0 {
		tools := make([]proxy.AnthropicTool, len(out.Tools))
		for i, t := range out.Tools {
			tools[i] = proxy.AnthropicTool{
				Name:        t.Name,
				Description: proxy.TruncateString(t.Description, 100),
				InputSchema: t.InputSchema,
			}
		}
		out.Tools = tools
		estimated = estimateTokens(&out)
		contextDebugLogf("[context-optimizer] after tool desc trim estimated=%d", estimated)
	}

	contextDebugLogf("[context-optimizer] final estimated=%d budget=%d", estimated, budget)
	return &out, nil
}

// ProcessResponse is a pass-through; responses are not modified.
func (c *ContextOptimizerMiddleware) ProcessResponse(resp []byte) ([]byte, error) {
	return resp, nil
}

// estimateTokens returns a rough token count estimate for req using the
// 4-chars-per-token heuristic. It counts characters in system messages,
// message content, and tool descriptions/schemas.
func estimateTokens(req *proxy.AnthropicRequest) int {
	chars := 0
	for _, s := range req.System {
		chars += len(s.Text)
	}
	for _, m := range req.Messages {
		chars += len(proxy.GetMessageContent(m.Content))
	}
	for _, t := range req.Tools {
		chars += len(t.Description)
		if schema, err := json.Marshal(t.InputSchema); err == nil {
			chars += len(schema)
		}
	}
	return (chars + charsPerToken - 1) / charsPerToken
}

// contextDebugLogf mirrors the debugLogf pattern from proxy/request.go.
func contextDebugLogf(format string, args ...any) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OPENKIRO_DEBUG"))) {
	case "1", "true", "yes", "on", "debug":
		fmt.Fprintf(os.Stderr, format+"\n", args...)
	}
}
