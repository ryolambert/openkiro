package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func withTestTransport(t *testing.T, fn roundTripFunc) {
	t.Helper()
	oldTransport := http.DefaultTransport
	http.DefaultTransport = fn
	t.Cleanup(func() { http.DefaultTransport = oldTransport })
}

func encodeAssistantFrame(t *testing.T, payload any) []byte {
	t.Helper()

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	var frame bytes.Buffer
	totalLen := uint32(len(data) + 12)
	if err := binary.Write(&frame, binary.BigEndian, totalLen); err != nil {
		t.Fatalf("write totalLen: %v", err)
	}
	if err := binary.Write(&frame, binary.BigEndian, uint32(0)); err != nil {
		t.Fatalf("write headerLen: %v", err)
	}
	frame.Write(data)
	frame.Write([]byte{0, 0, 0, 0})
	return frame.Bytes()
}

func parseSSEOutput(t *testing.T, body string) []map[string]any {
	t.Helper()

	chunks := strings.Split(strings.TrimSpace(body), "\n\n")
	events := make([]map[string]any, 0, len(chunks))
	for _, chunk := range chunks {
		if strings.TrimSpace(chunk) == "" {
			continue
		}
		lines := strings.Split(chunk, "\n")
		if len(lines) < 2 {
			t.Fatalf("unexpected SSE chunk: %q", chunk)
		}
		eventType := strings.TrimPrefix(lines[0], "event: ")
		dataLine := strings.TrimPrefix(lines[1], "data: ")
		var data map[string]any
		if err := json.Unmarshal([]byte(dataLine), &data); err != nil {
			t.Fatalf("unmarshal SSE data %q: %v", dataLine, err)
		}
		events = append(events, map[string]any{"event": eventType, "data": data})
	}
	return events
}

func TestResolveModelIDCharacterization(t *testing.T) {
	tests := map[string]string{
		"claude-4-sonnet":             modelSonnet46,
		"claude_opus_4_6_v1_0":        modelOpus46,
		"Acme Sonnet 4.5 Preview":     modelSonnet45,
		"totally-unknown-model-alias": modelSonnet46,
	}

	for input, want := range tests {
		t.Run(input, func(t *testing.T) {
			if got := resolveModelID(input); got != want {
				t.Fatalf("resolveModelID(%q) = %q, want %q", input, got, want)
			}
		})
	}
}

func TestBuildCodeWhispererRequestCharacterizationPreservesCallerContext(t *testing.T) {
	req := AnthropicRequest{
		Model:  "totally-unknown-model-alias",
		System: []AnthropicSystemMessage{{Type: "text", Text: "Follow the caller's instructions."}},
		Messages: []AnthropicRequestMessage{
			{Role: "user", Content: "Earlier question"},
			{Role: "assistant", Content: "Earlier answer"},
			{Role: "user", Content: "Current question"},
		},
	}

	cwReq := buildCodeWhispererRequest(req)
	current := cwReq.ConversationState.CurrentMessage.UserInputMessage

	if current.ModelId != modelBuilderSonnet45 {
		t.Fatalf("expected fallback model %q, got %q", modelBuilderSonnet45, current.ModelId)
	}
	if !strings.Contains(current.Content, req.System[0].Text) {
		t.Fatalf("expected current content to include system context, got %q", current.Content)
	}
	if !strings.Contains(current.Content, "<task>\nCurrent question\n</task>") {
		t.Fatalf("expected task wrapper in current content, got %q", current.Content)
	}
	if strings.Contains(current.Content, "You are Claude, an AI created by Anthropic") {
		t.Fatalf("did not expect injected identity reminder in current content, got %q", current.Content)
	}

	history := cwReq.ConversationState.History
	expectedHistoryLen := 2
	if len(history) != expectedHistoryLen {
		t.Fatalf("expected %d history entries, got %d", expectedHistoryLen, len(history))
	}

	firstUser, ok := history[0].(HistoryUserMessage)
	if !ok || firstUser.UserInputMessage.Content != "Earlier question" {
		t.Fatalf("expected first history entry to be the caller user turn, got %#v", history[0])
	}
	firstAssistant, ok := history[1].(HistoryAssistantMessage)
	if !ok || firstAssistant.AssistantResponseMessage.Content != "Earlier answer" {
		t.Fatalf("expected second history entry to be the caller assistant turn, got %#v", history[1])
	}

	for _, entry := range history {
		if userMsg, ok := entry.(HistoryUserMessage); ok && strings.Contains(userMsg.UserInputMessage.Content, "identity") {
			t.Fatalf("did not expect synthetic identity history, got %#v", history)
		}
	}
}

func TestHandleStreamRequestCharacterizationTextOnly(t *testing.T) {
	body := encodeAssistantFrame(t, map[string]any{"content": "hello from stream"})
	withTestTransport(t, func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(body)),
		}, nil
	})

	recorder := httptest.NewRecorder()
	handleStreamRequest(recorder, AnthropicRequest{
		Model:    "mystery-model",
		Messages: []AnthropicRequestMessage{{Role: "user", Content: "hello"}},
		Stream:   true,
	}, "token")

	events := parseSSEOutput(t, recorder.Body.String())
	if len(events) != 7 {
		t.Fatalf("expected 7 SSE events, got %d: %s", len(events), recorder.Body.String())
	}

	gotOrder := []string{}
	for _, event := range events {
		gotOrder = append(gotOrder, event["event"].(string))
	}
	wantOrder := []string{"message_start", "ping", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}
	if strings.Join(gotOrder, ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("unexpected SSE order %v, want %v", gotOrder, wantOrder)
	}

	message := events[0]["data"].(map[string]any)["message"].(map[string]any)
	if got := message["model"]; got != modelBuilderSonnet45 {
		t.Fatalf("expected streaming response to report resolved model, got %#v", got)
	}
	messageDelta := events[5]["data"].(map[string]any)["delta"].(map[string]any)
	if got := messageDelta["stop_reason"]; got != "end_turn" {
		t.Fatalf("expected end_turn stop_reason, got %#v", got)
	}
}

func TestHandleNonStreamRequestCharacterizationMixedTextToolKeepsBothBlocks(t *testing.T) {
	toolInput := `{"query":"drift"}`
	body := bytes.Join([][]byte{
		encodeAssistantFrame(t, map[string]any{"content": "hello before tool"}),
		encodeAssistantFrame(t, map[string]any{"toolUseId": "tool-1", "name": "search"}),
		encodeAssistantFrame(t, map[string]any{"toolUseId": "tool-1", "name": "search", "input": toolInput}),
		encodeAssistantFrame(t, map[string]any{"toolUseId": "tool-1", "name": "search", "stop": true}),
	}, nil)

	withTestTransport(t, func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(body)),
		}, nil
	})

	recorder := httptest.NewRecorder()
	handleNonStreamRequest(recorder, AnthropicRequest{
		Model:    "mystery-model",
		Messages: []AnthropicRequestMessage{{Role: "user", Content: "hello"}},
	}, "token")

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode non-stream response: %v", err)
	}

	if got := resp["model"]; got != modelBuilderSonnet45 {
		t.Fatalf("expected non-stream response to report resolved model, got %#v", got)
	}
	if got := resp["stop_reason"]; got != "tool_use" {
		t.Fatalf("expected mixed text/tool response to stop for tool_use, got %#v", got)
	}

	content := resp["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("expected text and tool_use blocks after mixed text/tool response, got %#v", content)
	}
	textBlock := content[0].(map[string]any)
	if got := textBlock["type"]; got != "text" {
		t.Fatalf("expected first block to be text, got %#v", got)
	}
	if got := textBlock["text"]; got != "hello before tool" {
		t.Fatalf("expected text block content to be preserved, got %#v", got)
	}
	block := content[1].(map[string]any)
	if got := block["type"]; got != "tool_use" {
		t.Fatalf("expected second block to be tool_use, got %#v", got)
	}
	input := block["input"].(map[string]any)
	if got := input["query"]; got != "drift" {
		t.Fatalf("expected parsed tool input query, got %#v", got)
	}
}

func TestSetClaudeUpdatesClaudeConfig(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	claudeConfigPath := filepath.Join(tempHome, ".claude.json")
	initial := map[string]any{
		"hasCompletedOnboarding": false,
		"kirolink":               true,
		legacyClaudeConfigKey():  true,
		"theme":                  "dark",
	}
	data, err := json.Marshal(initial)
	if err != nil {
		t.Fatalf("marshal initial claude config: %v", err)
	}
	if err := os.WriteFile(claudeConfigPath, data, 0o644); err != nil {
		t.Fatalf("write initial claude config: %v", err)
	}

	setClaude()

	updatedData, err := os.ReadFile(claudeConfigPath)
	if err != nil {
		t.Fatalf("read updated claude config: %v", err)
	}

	var updated map[string]any
	if err := json.Unmarshal(updatedData, &updated); err != nil {
		t.Fatalf("unmarshal updated claude config: %v", err)
	}

	if got := updated["hasCompletedOnboarding"]; got != true {
		t.Fatalf("expected hasCompletedOnboarding=true, got %#v", got)
	}
	if got := updated["openkiro"]; got != true {
		t.Fatalf("expected openkiro=true, got %#v", got)
	}
	if _, ok := updated["kirolink"]; ok {
		t.Fatalf("expected legacy kirolink key to be removed during config update")
	}
	if _, ok := updated[legacyClaudeConfigKey()]; ok {
		t.Fatalf("expected legacy helper key to be removed during config update")
	}
	if got := updated["theme"]; got != "dark" {
		t.Fatalf("expected unrelated config to be preserved, got %#v", got)
	}
}
