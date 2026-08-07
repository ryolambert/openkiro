package middleware_test

import (
	"strings"
	"testing"

	"github.com/ryolambert/openkiro/internal/middleware"
	"github.com/ryolambert/openkiro/internal/proxy"
)

// ── RtkCompressor tests ─────────────────────────────────────────────────────

func TestRtkCompressor_EmptyString(t *testing.T) {
	c := middleware.NewRtkCompressor()
	got := c.Compress("")
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestRtkCompressor_Available(t *testing.T) {
	c := middleware.NewRtkCompressor()
	// In CI the rtk binary is typically not installed, so Available() returns
	// false and Compress is a passthrough. This test validates both branches.
	if c.Available() {
		t.Log("rtk binary found on PATH — subprocess compression active")
	} else {
		t.Log("rtk binary not found — passthrough mode")
	}
}

func TestRtkCompressor_Passthrough_WhenUnavailable(t *testing.T) {
	c := middleware.NewRtkCompressor()
	if c.Available() {
		t.Skip("rtk binary is available; passthrough test not applicable")
	}
	input := "hello world\nhello world\nhello world"
	got := c.Compress(input)
	if got != input {
		t.Errorf("expected passthrough when rtk unavailable, got %q", got)
	}
}

// ── CompressionMiddleware tests ─────────────────────────────────────────────

func TestCompressionMiddleware_Name(t *testing.T) {
	m := middleware.NewCompressionMiddleware(false)
	if m.Name() != "compression" {
		t.Errorf("expected name %q, got %q", "compression", m.Name())
	}
}

func TestCompressionMiddleware_Disabled(t *testing.T) {
	m := middleware.NewCompressionMiddleware(false)
	req := &proxy.AnthropicRequest{
		Model:    "claude-sonnet-4-5",
		Messages: []proxy.AnthropicRequestMessage{{Role: "user", Content: "hello"}},
	}
	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != req {
		t.Error("disabled middleware should return the same pointer")
	}
}

func TestCompressionMiddleware_ZeroThreshold(t *testing.T) {
	m := middleware.NewCompressionMiddleware(true, middleware.WithThreshold(0))
	req := &proxy.AnthropicRequest{
		Model:    "claude-sonnet-4-5",
		Messages: []proxy.AnthropicRequestMessage{{Role: "user", Content: "hello"}},
	}
	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != req {
		t.Error("zero-threshold middleware should return the same pointer")
	}
}

func TestCompressionMiddleware_EmptyMessages(t *testing.T) {
	m := middleware.NewCompressionMiddleware(true)
	req := &proxy.AnthropicRequest{
		Model:    "claude-sonnet-4-5",
		Messages: nil,
	}
	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != req {
		t.Error("empty-messages request should be returned as-is")
	}
}

func TestCompressionMiddleware_ToolResultStringContent(t *testing.T) {
	largeContent := strings.Repeat("test output\ntest output\ntest output\n\n\n\n", 100)
	blocks := []interface{}{
		map[string]interface{}{
			"type":        "tool_result",
			"tool_use_id": "t1",
			"content":     largeContent,
		},
	}
	req := &proxy.AnthropicRequest{
		Model: "claude-sonnet-4-5",
		Messages: []proxy.AnthropicRequestMessage{
			{Role: "user", Content: blocks},
		},
	}

	m := middleware.NewCompressionMiddleware(true,
		middleware.WithThreshold(10),
		middleware.WithCompressor(&halvingCompressor{}),
	)
	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotBlocks := got.Messages[0].Content.([]interface{})
	block := gotBlocks[0].(map[string]interface{})
	compressed := block["content"].(string)
	if len(compressed) >= len(largeContent) {
		t.Errorf("expected compressed content to be shorter: compressed=%d original=%d",
			len(compressed), len(largeContent))
	}
}

func TestCompressionMiddleware_ToolResultTextField(t *testing.T) {
	largeText := strings.Repeat("log line\nlog line\nlog line\n", 100)
	blocks := []interface{}{
		map[string]interface{}{
			"type":        "tool_result",
			"tool_use_id": "t2",
			"text":        largeText,
		},
	}
	req := &proxy.AnthropicRequest{
		Model: "claude-sonnet-4-5",
		Messages: []proxy.AnthropicRequestMessage{
			{Role: "user", Content: blocks},
		},
	}

	m := middleware.NewCompressionMiddleware(true,
		middleware.WithThreshold(10),
		middleware.WithCompressor(&halvingCompressor{}),
	)
	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotBlocks := got.Messages[0].Content.([]interface{})
	block := gotBlocks[0].(map[string]interface{})
	compressed := block["text"].(string)
	if len(compressed) >= len(largeText) {
		t.Errorf("expected compressed text to be shorter: compressed=%d original=%d",
			len(compressed), len(largeText))
	}
}

func TestCompressionMiddleware_BelowThreshold(t *testing.T) {
	blocks := []interface{}{
		map[string]interface{}{
			"type":        "tool_result",
			"tool_use_id": "t3",
			"content":     "short",
		},
	}
	req := &proxy.AnthropicRequest{
		Model: "claude-sonnet-4-5",
		Messages: []proxy.AnthropicRequestMessage{
			{Role: "user", Content: blocks},
		},
	}

	m := middleware.NewCompressionMiddleware(true, middleware.WithThreshold(200))
	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotBlocks := got.Messages[0].Content.([]interface{})
	block := gotBlocks[0].(map[string]interface{})
	if block["content"] != "short" {
		t.Errorf("block below threshold should not be compressed, got %q", block["content"])
	}
}

func TestCompressionMiddleware_NonToolResultBlocks_Untouched(t *testing.T) {
	blocks := []interface{}{
		map[string]interface{}{
			"type": "text",
			"text": strings.Repeat("big text\nbig text\nbig text\n", 100),
		},
	}
	req := &proxy.AnthropicRequest{
		Model: "claude-sonnet-4-5",
		Messages: []proxy.AnthropicRequestMessage{
			{Role: "user", Content: blocks},
		},
	}

	m := middleware.NewCompressionMiddleware(true,
		middleware.WithThreshold(10),
		middleware.WithCompressor(&halvingCompressor{}),
	)
	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotBlocks := got.Messages[0].Content.([]interface{})
	block := gotBlocks[0].(map[string]interface{})
	original := blocks[0].(map[string]interface{})
	if block["text"] != original["text"] {
		t.Error("non-tool_result blocks should not be compressed")
	}
}

func TestCompressionMiddleware_PlainStringContent_User(t *testing.T) {
	largeText := strings.Repeat("output\noutput\noutput\n\n\n\n", 100)
	req := &proxy.AnthropicRequest{
		Model: "claude-sonnet-4-5",
		Messages: []proxy.AnthropicRequestMessage{
			{Role: "user", Content: largeText},
		},
	}

	m := middleware.NewCompressionMiddleware(true,
		middleware.WithThreshold(10),
		middleware.WithCompressor(&halvingCompressor{}),
	)
	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	compressed, ok := got.Messages[0].Content.(string)
	if !ok {
		t.Fatal("expected string content")
	}
	if len(compressed) >= len(largeText) {
		t.Errorf("expected compressed text to be shorter: compressed=%d original=%d",
			len(compressed), len(largeText))
	}
}

func TestCompressionMiddleware_PlainStringContent_AssistantNotCompressed(t *testing.T) {
	largeText := strings.Repeat("output\noutput\noutput\n", 100)
	req := &proxy.AnthropicRequest{
		Model: "claude-sonnet-4-5",
		Messages: []proxy.AnthropicRequestMessage{
			{Role: "assistant", Content: largeText},
		},
	}

	m := middleware.NewCompressionMiddleware(true,
		middleware.WithThreshold(10),
		middleware.WithCompressor(&halvingCompressor{}),
	)
	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.Messages[0].Content != largeText {
		t.Error("assistant plain-string content should not be compressed")
	}
}

func TestCompressionMiddleware_NestedContentBlocks(t *testing.T) {
	largeText := strings.Repeat("result\nresult\nresult\n\n\n", 100)
	blocks := []interface{}{
		map[string]interface{}{
			"type":        "tool_result",
			"tool_use_id": "t4",
			"content": []interface{}{
				map[string]interface{}{
					"type": "text",
					"text": largeText,
				},
			},
		},
	}
	req := &proxy.AnthropicRequest{
		Model: "claude-sonnet-4-5",
		Messages: []proxy.AnthropicRequestMessage{
			{Role: "user", Content: blocks},
		},
	}

	m := middleware.NewCompressionMiddleware(true,
		middleware.WithThreshold(10),
		middleware.WithCompressor(&halvingCompressor{}),
	)
	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotBlocks := got.Messages[0].Content.([]interface{})
	block := gotBlocks[0].(map[string]interface{})
	nested := block["content"].([]interface{})
	subBlock := nested[0].(map[string]interface{})
	compressed := subBlock["text"].(string)
	if len(compressed) >= len(largeText) {
		t.Errorf("nested text should be compressed: compressed=%d original=%d",
			len(compressed), len(largeText))
	}
}

func TestCompressionMiddleware_ProcessResponse_Noop(t *testing.T) {
	m := middleware.NewCompressionMiddleware(true)
	data := []byte(`{"type":"message"}`)
	got, err := m.ProcessResponse(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("expected passthrough, got %q", got)
	}
}

func TestCompressionMiddleware_DoesNotMutateOriginal(t *testing.T) {
	largeText := strings.Repeat("x\nx\nx\n", 100)
	blocks := []interface{}{
		map[string]interface{}{
			"type":        "tool_result",
			"tool_use_id": "t5",
			"content":     largeText,
		},
	}
	req := &proxy.AnthropicRequest{
		Model: "claude-sonnet-4-5",
		Messages: []proxy.AnthropicRequestMessage{
			{Role: "user", Content: blocks},
		},
	}

	m := middleware.NewCompressionMiddleware(true,
		middleware.WithThreshold(10),
		middleware.WithCompressor(&halvingCompressor{}),
	)
	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Original should be untouched.
	origBlock := blocks[0].(map[string]interface{})
	if origBlock["content"] != largeText {
		t.Error("original block should not be mutated")
	}
	// But the result should be different.
	gotBlocks := got.Messages[0].Content.([]interface{})
	gotBlock := gotBlocks[0].(map[string]interface{})
	if gotBlock["content"] == largeText {
		t.Error("result should have compressed content")
	}
}

func TestCompressionMiddleware_CustomCompressor(t *testing.T) {
	custom := &halvingCompressor{}
	m := middleware.NewCompressionMiddleware(true,
		middleware.WithThreshold(1),
		middleware.WithCompressor(custom),
	)

	largeText := strings.Repeat("hello world\n", 50)
	blocks := []interface{}{
		map[string]interface{}{
			"type":        "tool_result",
			"tool_use_id": "t6",
			"content":     largeText,
		},
	}
	req := &proxy.AnthropicRequest{
		Model: "claude-sonnet-4-5",
		Messages: []proxy.AnthropicRequestMessage{
			{Role: "user", Content: blocks},
		},
	}

	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotBlocks := got.Messages[0].Content.([]interface{})
	block := gotBlocks[0].(map[string]interface{})
	compressed := block["content"].(string)
	if compressed == largeText {
		t.Error("custom compressor should have been applied")
	}
	if len(compressed) >= len(largeText) {
		t.Errorf("halving compressor should produce shorter output: got=%d original=%d",
			len(compressed), len(largeText))
	}
}

func TestCompressionMiddleware_InChain(t *testing.T) {
	m := middleware.NewCompressionMiddleware(true,
		middleware.WithThreshold(10),
		middleware.WithCompressor(&halvingCompressor{}),
	)
	var chain middleware.Chain
	chain.Add(m)

	largeText := strings.Repeat("data\ndata\n", 100)
	req := &proxy.AnthropicRequest{
		Model: "claude-sonnet-4-5",
		Messages: []proxy.AnthropicRequestMessage{
			{Role: "user", Content: largeText},
		},
	}

	got, err := chain.ProcessRequest(req)
	if err != nil {
		t.Fatalf("chain error: %v", err)
	}
	compressed, ok := got.Messages[0].Content.(string)
	if !ok {
		t.Fatal("expected string content")
	}
	if len(compressed) >= len(largeText) {
		t.Errorf("expected chain to produce compressed output")
	}
}

func TestCompressionMiddleware_WithNilCompressor_UsesDefault(t *testing.T) {
	m := middleware.NewCompressionMiddleware(true, middleware.WithCompressor(nil))
	// Should not panic and should use default compressor (RtkCompressor).
	if m == nil {
		t.Fatal("expected non-nil middleware")
	}
	// Verify the middleware is functional (no panic).
	req := &proxy.AnthropicRequest{
		Model: "claude-sonnet-4-5",
		Messages: []proxy.AnthropicRequestMessage{
			{Role: "user", Content: "hello"},
		},
	}
	_, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

// halvingCompressor is a test-only Compressor that returns the first half of
// text, guaranteeing a shorter result for compression eligibility checks.
type halvingCompressor struct{}

func (h *halvingCompressor) Compress(text string) string {
	if len(text) <= 1 {
		return text
	}
	return text[:len(text)/2]
}