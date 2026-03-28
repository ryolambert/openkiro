package middleware_test

import (
	"strings"
	"testing"

	"github.com/ryolambert/openkiro/internal/middleware"
	"github.com/ryolambert/openkiro/internal/proxy"
)

// ── RED: expectations written before implementation ──────────────────────────

func TestContextOptimizerMiddleware_Name(t *testing.T) {
	m := &middleware.ContextOptimizerMiddleware{}
	if got := m.Name(); got != "context-optimizer" {
		t.Errorf("expected %q, got %q", "context-optimizer", got)
	}
}

// ── GREEN: small requests pass through unchanged ─────────────────────────────

func TestContextOptimizerMiddleware_SmallRequest_noTrim(t *testing.T) {
	m := &middleware.ContextOptimizerMiddleware{TokenBudget: 128_000}
	req := &proxy.AnthropicRequest{
		System:   []proxy.AnthropicSystemMessage{{Type: "text", Text: "be helpful"}},
		Messages: []proxy.AnthropicRequestMessage{{Role: "user", Content: "hello"}},
	}
	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Content should be unchanged.
	if got.System[0].Text != "be helpful" {
		t.Errorf("system text should be unchanged, got %q", got.System[0].Text)
	}
	if got.Messages[0].Content.(string) != "hello" {
		t.Errorf("message content should be unchanged, got %v", got.Messages[0].Content)
	}
}

func TestContextOptimizerMiddleware_ProcessResponse_passThrough(t *testing.T) {
	m := &middleware.ContextOptimizerMiddleware{}
	resp := []byte(`{"type":"message"}`)
	got, err := m.ProcessResponse(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != string(resp) {
		t.Errorf("ProcessResponse must be pass-through, got %q", got)
	}
}

// ── REFACTOR: table-driven trimming tests ────────────────────────────────────

func TestContextOptimizerMiddleware_TrimsHistory_whenOverBudget(t *testing.T) {
	// Create a request with many history messages whose total exceeds a tiny budget.
	msgs := make([]proxy.AnthropicRequestMessage, 0, 11)
	for i := 0; i < 10; i++ {
		msgs = append(msgs, proxy.AnthropicRequestMessage{
			Role:    "user",
			Content: strings.Repeat("word ", 100), // ~500 chars each
		})
	}
	// Current (last) message.
	msgs = append(msgs, proxy.AnthropicRequestMessage{
		Role:    "user",
		Content: "final question",
	})

	m := &middleware.ContextOptimizerMiddleware{TokenBudget: 200} // ~800 chars total budget
	req := &proxy.AnthropicRequest{Messages: msgs}

	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Fewer messages than original — history trimmed.
	if len(got.Messages) >= len(req.Messages) {
		t.Errorf("expected fewer messages after trimming, got %d (original %d)", len(got.Messages), len(req.Messages))
	}
	// Last message must always be preserved.
	last := got.Messages[len(got.Messages)-1]
	if last.Content.(string) != "final question" {
		t.Errorf("last message should be preserved, got %v", last.Content)
	}
}

func TestContextOptimizerMiddleware_CompressesSystem_whenOverBudget(t *testing.T) {
	// A tiny budget and a single message — trimming history won't help; system
	// prompt should be compressed instead.
	m := &middleware.ContextOptimizerMiddleware{TokenBudget: 5}
	// System prompt with repeated lines.
	req := &proxy.AnthropicRequest{
		System: []proxy.AnthropicSystemMessage{
			{Type: "text", Text: "do this\ndo this\ndo this\n" + strings.Repeat("x", 200)},
		},
		Messages: []proxy.AnthropicRequestMessage{{Role: "user", Content: "hi"}},
	}
	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// System text should have been compressed (duplicate lines removed).
	text := got.System[0].Text
	if strings.Count(text, "do this") > 1 {
		t.Errorf("expected duplicate lines removed, still have multiple 'do this' in %q", text)
	}
}

func TestContextOptimizerMiddleware_TruncatesToolDesc_whenOverBudget(t *testing.T) {
	longDesc := strings.Repeat("z", 500)
	m := &middleware.ContextOptimizerMiddleware{TokenBudget: 5}
	req := &proxy.AnthropicRequest{
		Tools: []proxy.AnthropicTool{
			{Name: "tool", Description: longDesc, InputSchema: map[string]any{"type": "object"}},
		},
		Messages: []proxy.AnthropicRequestMessage{{Role: "user", Content: "hi"}},
	}
	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Tools[0].Description) >= 500 {
		t.Errorf("expected truncated tool description, got length %d", len(got.Tools[0].Description))
	}
}

func TestContextOptimizerMiddleware_DefaultBudget_usedWhenZero(t *testing.T) {
	m := &middleware.ContextOptimizerMiddleware{TokenBudget: 0} // should use 128k default
	req := &proxy.AnthropicRequest{
		Messages: []proxy.AnthropicRequestMessage{{Role: "user", Content: "hello"}},
	}
	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Tiny request should pass through unchanged.
	if got.Messages[0].Content.(string) != "hello" {
		t.Errorf("expected unchanged content, got %v", got.Messages[0].Content)
	}
}

func TestContextOptimizerMiddleware_EmptyRequest_noError(t *testing.T) {
	m := &middleware.ContextOptimizerMiddleware{TokenBudget: 1}
	req := &proxy.AnthropicRequest{}
	_, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error on empty request: %v", err)
	}
}
