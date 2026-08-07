package middleware_test

import (
	"strings"
	"testing"

	"github.com/ryolambert/openkiro/internal/middleware"
	"github.com/ryolambert/openkiro/internal/proxy"
)

// TestIntegration_FullChain verifies that CompressionMiddleware,
// MemoryMiddleware, and ContextOptimizerMiddleware can be composed together in
// a Chain and that each middleware's effect is applied in order.
func TestIntegration_FullChain_allThreeMiddlewares(t *testing.T) {
	store := &middleware.InMemoryStore{}
	_ = store.Store(middleware.MemoryEntry{Key: "tip", Value: "write tests first"})

	var chain middleware.Chain
	chain.Add(&middleware.CompressionMiddleware{Level: middleware.CompressionLight})
	chain.Add(&middleware.MemoryMiddleware{Store: store})
	chain.Add(&middleware.ContextOptimizerMiddleware{TokenBudget: 128_000})

	req := &proxy.AnthropicRequest{
		System: []proxy.AnthropicSystemMessage{
			{Type: "text", Text: "  be   helpful  "},
		},
		Messages: []proxy.AnthropicRequestMessage{
			{Role: "user", Content: "hello"},
		},
	}

	got, err := chain.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Compression middleware should have cleaned up whitespace.
	if got.System[0].Text == "  be   helpful  " {
		t.Error("expected system text to be compressed")
	}

	// Memory middleware should have injected a <memory> block.
	if !strings.Contains(got.System[0].Text, "<memory>") {
		t.Errorf("expected <memory> block in system text, got: %q", got.System[0].Text)
	}
	if !strings.Contains(got.System[0].Text, "write tests first") {
		t.Errorf("expected memory value in system text, got: %q", got.System[0].Text)
	}
}

// TestIntegration_FullChain_errorPropagation verifies that an error from the
// first middleware in the chain stops processing and surfaces to the caller.
func TestIntegration_FullChain_errorPropagation(t *testing.T) {
	var chain middleware.Chain
	chain.Add(&alwaysErrorMiddleware{msg: "boom"})
	chain.Add(&middleware.CompressionMiddleware{Level: middleware.CompressionLight})

	req := &proxy.AnthropicRequest{
		Messages: []proxy.AnthropicRequestMessage{{Role: "user", Content: "hi"}},
	}
	_, err := chain.ProcessRequest(req)
	if err == nil {
		t.Fatal("expected error from chain, got nil")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected error to contain 'boom', got: %v", err)
	}
}

// TestIntegration_FullChain_responsePassThrough verifies that all three
// middlewares leave the response bytes unchanged (all are pass-throughs on the
// response path).
func TestIntegration_FullChain_responsePassThrough(t *testing.T) {
	var chain middleware.Chain
	chain.Add(&middleware.CompressionMiddleware{Level: middleware.CompressionAggressive})
	chain.Add(&middleware.MemoryMiddleware{Store: &middleware.InMemoryStore{}})
	chain.Add(&middleware.ContextOptimizerMiddleware{TokenBudget: 128_000})

	resp := []byte(`{"type":"message","role":"assistant","content":[{"type":"text","text":"ok"}]}`)
	got, err := chain.ProcessResponse(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != string(resp) {
		t.Errorf("expected response pass-through, got %q", got)
	}
}

// TestIntegration_Ordering verifies that middleware effects compose in the
// order they are added: Compression runs before Memory injection, so by the
// time Memory injects its block, the system prompt is already compressed.
func TestIntegration_Ordering_compressionBeforeMemory(t *testing.T) {
	store := &middleware.InMemoryStore{}
	_ = store.Store(middleware.MemoryEntry{Key: "k", Value: "v"})

	var chain middleware.Chain
	chain.Add(&middleware.CompressionMiddleware{Level: middleware.CompressionLight})
	chain.Add(&middleware.MemoryMiddleware{Store: store})

	// System prompt with trailing spaces — Compression should strip them before
	// Memory prepends its block.
	req := &proxy.AnthropicRequest{
		System:   []proxy.AnthropicSystemMessage{{Type: "text", Text: "system   "}},
		Messages: []proxy.AnthropicRequestMessage{{Role: "user", Content: "q"}},
	}

	got, err := chain.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := got.System[0].Text
	// The memory block must be present.
	if !strings.Contains(text, "<memory>") {
		t.Errorf("expected <memory> block, got: %q", text)
	}
	// The original system content should not have trailing spaces.
	if strings.Contains(text, "system   ") {
		t.Errorf("expected compressed system text (no trailing spaces), got: %q", text)
	}
}

// TestIntegration_ContextOptimizer_trimsAfterInjection verifies that the
// ContextOptimizerMiddleware (placed last) can trim a request that was already
// processed by the earlier middlewares.
func TestIntegration_ContextOptimizer_trimsAfterInjection(t *testing.T) {
	store := &middleware.InMemoryStore{}
	// Inject a large memory value to push the request over budget.
	_ = store.Store(middleware.MemoryEntry{Key: "big", Value: strings.Repeat("x", 2000)})

	var chain middleware.Chain
	chain.Add(&middleware.MemoryMiddleware{Store: store})
	chain.Add(&middleware.ContextOptimizerMiddleware{TokenBudget: 10}) // very tight

	msgs := make([]proxy.AnthropicRequestMessage, 0, 11)
	for i := 0; i < 10; i++ {
		msgs = append(msgs, proxy.AnthropicRequestMessage{
			Role:    "user",
			Content: strings.Repeat("y", 100),
		})
	}
	msgs = append(msgs, proxy.AnthropicRequestMessage{Role: "user", Content: "final"})

	req := &proxy.AnthropicRequest{Messages: msgs}

	got, err := chain.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// History should have been trimmed.
	if len(got.Messages) >= len(msgs) {
		t.Errorf("expected history trimming, got %d messages (original %d)", len(got.Messages), len(msgs))
	}
	// Last message must be preserved.
	if got.Messages[len(got.Messages)-1].Content.(string) != "final" {
		t.Errorf("last message should be preserved")
	}
}
