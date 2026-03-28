package middleware_test

import (
	"strings"
	"testing"

	"github.com/ryolambert/openkiro/internal/middleware"
	"github.com/ryolambert/openkiro/internal/proxy"
)

// ── RED: expectations written before implementation ──────────────────────────

func TestCompressionMiddleware_Name(t *testing.T) {
	m := &middleware.CompressionMiddleware{Level: middleware.CompressionLight}
	if got := m.Name(); got != "compression" {
		t.Errorf("expected %q, got %q", "compression", got)
	}
}

// ── GREEN: CompressionNone is a pure pass-through ────────────────────────────

func TestCompressionMiddleware_None_passThrough(t *testing.T) {
	m := &middleware.CompressionMiddleware{Level: middleware.CompressionNone}
	req := &proxy.AnthropicRequest{
		System: []proxy.AnthropicSystemMessage{{Type: "text", Text: "  hello   world  "}},
	}
	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != req {
		t.Error("expected same pointer for CompressionNone")
	}
	if got.System[0].Text != "  hello   world  " {
		t.Errorf("expected original text, got %q", got.System[0].Text)
	}
}

func TestCompressionMiddleware_ProcessResponse_passThrough(t *testing.T) {
	m := &middleware.CompressionMiddleware{Level: middleware.CompressionAggressive}
	resp := []byte(`{"type":"message"}`)
	got, err := m.ProcessResponse(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != string(resp) {
		t.Errorf("ProcessResponse must be pass-through, got %q", got)
	}
}

// ── REFACTOR: table-driven compression strategy tests ───────────────────────

func TestCompressionMiddleware_Light_systemPrompt(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "strips leading/trailing whitespace",
			input: "  hello  ",
			want:  "hello",
		},
		{
			name:  "collapses multiple inline spaces",
			input: "hello   world",
			want:  "hello world",
		},
		{
			name:  "collapses excessive blank lines",
			input: "line1\n\n\n\nline2",
			want:  "line1\n\nline2",
		},
		{
			name:  "normalises Windows line endings",
			input: "line1\r\nline2",
			want:  "line1\nline2",
		},
		{
			name:  "empty string stays empty",
			input: "",
			want:  "",
		},
		{
			name:  "already short stays unchanged content-wise",
			input: "ok",
			want:  "ok",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := &middleware.CompressionMiddleware{Level: middleware.CompressionLight}
			req := &proxy.AnthropicRequest{
				System: []proxy.AnthropicSystemMessage{{Type: "text", Text: tc.input}},
			}
			got, err := m.ProcessRequest(req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.System[0].Text != tc.want {
				t.Errorf("input=%q: expected %q, got %q", tc.input, tc.want, got.System[0].Text)
			}
		})
	}
}

func TestCompressionMiddleware_Aggressive_deduplicatesLines(t *testing.T) {
	m := &middleware.CompressionMiddleware{Level: middleware.CompressionAggressive}
	input := "do this\ndo this\ndo something else"
	req := &proxy.AnthropicRequest{
		System: []proxy.AnthropicSystemMessage{{Type: "text", Text: input}},
	}
	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := got.System[0].Text
	// "do this" should appear only once.
	if count := strings.Count(text, "do this"); count != 1 {
		t.Errorf("expected 1 occurrence of 'do this', got %d in %q", count, text)
	}
}

func TestCompressionMiddleware_Aggressive_truncatesToolDesc(t *testing.T) {
	m := &middleware.CompressionMiddleware{Level: middleware.CompressionAggressive}
	longDesc := strings.Repeat("x", 300)
	req := &proxy.AnthropicRequest{
		Tools: []proxy.AnthropicTool{
			{Name: "my_tool", Description: longDesc, InputSchema: map[string]any{"type": "object"}},
		},
		Messages: []proxy.AnthropicRequestMessage{{Role: "user", Content: "hi"}},
	}
	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Tools[0].Description) >= 300 {
		t.Errorf("expected truncated tool description, got length %d", len(got.Tools[0].Description))
	}
}

func TestCompressionMiddleware_Light_compressesMessageContent(t *testing.T) {
	m := &middleware.CompressionMiddleware{Level: middleware.CompressionLight}
	req := &proxy.AnthropicRequest{
		Messages: []proxy.AnthropicRequestMessage{
			{Role: "user", Content: "hello   world"},
		},
	}
	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Messages[0].Content.(string) != "hello world" {
		t.Errorf("expected compressed message content, got %q", got.Messages[0].Content)
	}
}

func TestCompressionMiddleware_Light_compressesBlockContent(t *testing.T) {
	m := &middleware.CompressionMiddleware{Level: middleware.CompressionLight}
	req := &proxy.AnthropicRequest{
		Messages: []proxy.AnthropicRequestMessage{
			{Role: "user", Content: []interface{}{
				map[string]interface{}{"type": "text", "text": "hello   world"},
			}},
		},
	}
	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	blocks := got.Messages[0].Content.([]interface{})
	block := blocks[0].(map[string]interface{})
	if block["text"] != "hello world" {
		t.Errorf("expected compressed block text, got %q", block["text"])
	}
}

func TestCompressionMiddleware_nilFields_safeToProcess(t *testing.T) {
	m := &middleware.CompressionMiddleware{Level: middleware.CompressionAggressive}
	// Empty request with no system, no messages, no tools.
	req := &proxy.AnthropicRequest{Model: "claude-sonnet-4-5"}
	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error on empty request: %v", err)
	}
	if got.Model != "claude-sonnet-4-5" {
		t.Errorf("model should be unchanged, got %q", got.Model)
	}
}
