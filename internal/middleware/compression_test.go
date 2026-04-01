package middleware_test

import (
	"strings"
	"testing"

	"github.com/ryolambert/openkiro/internal/middleware"
	"github.com/ryolambert/openkiro/internal/proxy"
)

// ── DefaultCompressor tests ─────────────────────────────────────────────────

func TestDefaultCompressor_EmptyString(t *testing.T) {
	c := &middleware.DefaultCompressor{}
	got := c.Compress("")
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestDefaultCompressor_StripANSI(t *testing.T) {
	c := &middleware.DefaultCompressor{}
	input := "\x1b[32mOK\x1b[0m test passed"
	got := c.Compress(input)
	if strings.Contains(got, "\x1b") {
		t.Errorf("expected ANSI codes to be stripped, got %q", got)
	}
	if !strings.Contains(got, "OK") || !strings.Contains(got, "test passed") {
		t.Errorf("expected text content to be preserved, got %q", got)
	}
}

func TestDefaultCompressor_CollapseBlankLines(t *testing.T) {
	c := &middleware.DefaultCompressor{}
	input := "line1\n\n\n\nline2\n\n\n\n\nline3"
	got := c.Compress(input)
	// Should have at most one blank line between content lines.
	lines := strings.Split(got, "\n")
	consecutiveBlanks := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			consecutiveBlanks++
			if consecutiveBlanks > 1 {
				t.Errorf("found consecutive blank lines in compressed output: %q", got)
				break
			}
		} else {
			consecutiveBlanks = 0
		}
	}
}

func TestDefaultCompressor_DeduplicateLines(t *testing.T) {
	c := &middleware.DefaultCompressor{}
	input := "ok\nok\nok\nok\nok"
	got := c.Compress(input)
	if !strings.Contains(got, "(×5)") {
		t.Errorf("expected deduplicated line with (×5), got %q", got)
	}
	// Should be a single line now.
	lines := strings.Split(got, "\n")
	if len(lines) != 1 {
		t.Errorf("expected 1 line after dedup, got %d: %q", len(lines), got)
	}
}

func TestDefaultCompressor_DeduplicateMixed(t *testing.T) {
	c := &middleware.DefaultCompressor{}
	input := "a\na\na\nb\nc\nc"
	got := c.Compress(input)
	if !strings.Contains(got, "a (×3)") {
		t.Errorf("expected 'a (×3)' in output, got %q", got)
	}
	if !strings.Contains(got, "c (×2)") {
		t.Errorf("expected 'c (×2)' in output, got %q", got)
	}
	if !strings.Contains(got, "b") {
		t.Errorf("expected 'b' preserved, got %q", got)
	}
}

func TestDefaultCompressor_TruncateLongLines(t *testing.T) {
	c := &middleware.DefaultCompressor{MaxLineLen: 20}
	input := strings.Repeat("x", 50)
	got := c.Compress(input)
	if len(got) >= 50 {
		t.Errorf("expected line to be truncated, got length %d", len(got))
	}
	if !strings.Contains(got, "…[truncated]") {
		t.Errorf("expected truncation marker, got %q", got)
	}
}

func TestDefaultCompressor_StripTrailingWhitespace(t *testing.T) {
	c := &middleware.DefaultCompressor{}
	input := "hello   \nworld\t\t\n"
	got := c.Compress(input)
	for _, line := range strings.Split(got, "\n") {
		trimmed := strings.TrimRight(line, " \t\r")
		if line != trimmed {
			t.Errorf("line has trailing whitespace: %q", line)
		}
	}
}

func TestDefaultCompressor_AllFiltersCombined(t *testing.T) {
	c := &middleware.DefaultCompressor{MaxLineLen: 30}
	input := "\x1b[31mERROR\x1b[0m   \n\n\n\nrepeat\nrepeat\nrepeat\n" +
		strings.Repeat("z", 50) + "\nfinal"
	got := c.Compress(input)

	if strings.Contains(got, "\x1b") {
		t.Error("ANSI codes should be stripped")
	}
	if strings.Contains(got, "repeat\nrepeat") {
		t.Error("duplicate lines should be collapsed")
	}
	if !strings.Contains(got, "repeat (×3)") {
		t.Errorf("expected 'repeat (×3)', got %q", got)
	}
	if !strings.Contains(got, "…[truncated]") {
		t.Error("long line should be truncated")
	}
	if !strings.Contains(got, "final") {
		t.Error("final line should be preserved")
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

	m := middleware.NewCompressionMiddleware(true, middleware.WithThreshold(10))
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

	m := middleware.NewCompressionMiddleware(true, middleware.WithThreshold(10))
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

	m := middleware.NewCompressionMiddleware(true, middleware.WithThreshold(10))
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

	m := middleware.NewCompressionMiddleware(true, middleware.WithThreshold(10))
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

	m := middleware.NewCompressionMiddleware(true, middleware.WithThreshold(10))
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

	m := middleware.NewCompressionMiddleware(true, middleware.WithThreshold(10))
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

	m := middleware.NewCompressionMiddleware(true, middleware.WithThreshold(10))
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
	m := middleware.NewCompressionMiddleware(true, middleware.WithThreshold(10))
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
	// Should not panic and should use default compressor.
	largeText := strings.Repeat("test\ntest\n", 100)
	req := &proxy.AnthropicRequest{
		Model: "claude-sonnet-4-5",
		Messages: []proxy.AnthropicRequestMessage{
			{Role: "user", Content: largeText},
		},
	}
	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	compressed := got.Messages[0].Content.(string)
	if len(compressed) >= len(largeText) {
		t.Errorf("default compressor should compress duplicate lines")
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
