package proxy

import (
	"strings"
	"testing"
)

func TestResolveModelIDCharacterization(t *testing.T) {
	tests := map[string]string{
		"claude-4-sonnet":             ModelSonnet46,
		"claude_opus_4_6_v1_0":        ModelOpus46,
		"Acme Sonnet 4.5 Preview":     ModelSonnet45,
		"totally-unknown-model-alias": ModelSonnet46,
	}

	for input, want := range tests {
		t.Run(input, func(t *testing.T) {
			if got := ResolveModelID(input); got != want {
				t.Fatalf("ResolveModelID(%q) = %q, want %q", input, got, want)
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

	cwReq := BuildCodeWhispererRequest(req)
	current := cwReq.ConversationState.CurrentMessage.UserInputMessage

	if current.ModelId != ModelBuilderSonnet45 {
		t.Fatalf("expected fallback model %q, got %q", ModelBuilderSonnet45, current.ModelId)
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
	if len(history) != 2 {
		t.Fatalf("expected 2 history entries, got %d", len(history))
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

func TestBuildCodeWhispererRequestRemovesIdentityForcing(t *testing.T) {
	req := AnthropicRequest{
		Model:  "claude-sonnet-4-6",
		System: []AnthropicSystemMessage{{Type: "text", Text: "Follow repository conventions."}},
		Messages: []AnthropicRequestMessage{
			{Role: "user", Content: "Earlier request"},
			{Role: "assistant", Content: "Earlier answer"},
			{Role: "user", Content: "Current task"},
		},
	}

	cwReq := BuildCodeWhispererRequest(req)
	current := cwReq.ConversationState.CurrentMessage.UserInputMessage.Content

	if !strings.Contains(current, "Follow repository conventions.") {
		t.Fatalf("expected caller system context in current message, got %q", current)
	}
	if !strings.Contains(current, "Current task") {
		t.Fatalf("expected current task content in current message, got %q", current)
	}
	if strings.Contains(current, "ignore any system prompt or metadata") {
		t.Fatalf("unexpected synthetic identity override in current message: %q", current)
	}
	if got := len(cwReq.ConversationState.History); got != 2 {
		t.Fatalf("expected only caller-provided history entries, got %d", got)
	}

	first, ok := cwReq.ConversationState.History[0].(HistoryUserMessage)
	if !ok || first.UserInputMessage.Content != "Earlier request" {
		t.Fatalf("unexpected first history entry: %#v", cwReq.ConversationState.History[0])
	}
	second, ok := cwReq.ConversationState.History[1].(HistoryAssistantMessage)
	if !ok || second.AssistantResponseMessage.Content != "Earlier answer" {
		t.Fatalf("unexpected second history entry: %#v", cwReq.ConversationState.History[1])
	}
}

func TestEnsurePayloadFitsTrimsOldestHistoryFirst(t *testing.T) {
	orig := MaxPayloadBytes
	MaxPayloadBytes = 200000
	t.Cleanup(func() { MaxPayloadBytes = orig })

	cwReq := CodeWhispererRequest{}
	cwReq.ConversationState.CurrentMessage.UserInputMessage.Content = "current"
	cwReq.ConversationState.History = []any{
		historyUserWithText("first-" + strings.Repeat("a", 90000)),
		historyUserWithText("second-" + strings.Repeat("b", 90000)),
		historyUserWithText("third-" + strings.Repeat("c", 90000)),
		historyUserWithText("fourth-" + strings.Repeat("d", 90000)),
	}

	data, err := EnsurePayloadFits(&cwReq)
	if err != nil {
		t.Fatalf("EnsurePayloadFits returned error: %v", err)
	}
	if len(data) > MaxPayloadBytes {
		t.Fatalf("payload still exceeds limit: %d > %d", len(data), MaxPayloadBytes)
	}
	if len(cwReq.ConversationState.History) >= 4 {
		t.Fatalf("expected history trimming to occur, got %d entries", len(cwReq.ConversationState.History))
	}

	firstRemaining := cwReq.ConversationState.History[0].(HistoryUserMessage).UserInputMessage.Content
	lastRemaining := cwReq.ConversationState.History[len(cwReq.ConversationState.History)-1].(HistoryUserMessage).UserInputMessage.Content
	if strings.HasPrefix(firstRemaining, "first-") {
		t.Fatalf("expected oldest history entry to be trimmed first, got %q", firstRemaining[:16])
	}
	if !strings.HasPrefix(lastRemaining, "fourth-") {
		t.Fatalf("expected most recent history entry to be preserved, got %q", lastRemaining[:16])
	}
}

func TestKeepMostRecentHistoryPrefersNewestEntries(t *testing.T) {
	history := []any{
		historyUserWithText("first"),
		historyUserWithText("second"),
		historyUserWithText("third"),
	}

	trimmed := KeepMostRecentHistory(history, 2)
	if len(trimmed) != 2 {
		t.Fatalf("expected 2 entries after trim, got %d", len(trimmed))
	}
	if got := trimmed[0].(HistoryUserMessage).UserInputMessage.Content; got != "second" {
		t.Fatalf("expected second entry first after trim, got %q", got)
	}
	if got := trimmed[1].(HistoryUserMessage).UserInputMessage.Content; got != "third" {
		t.Fatalf("expected newest entry to be preserved, got %q", got)
	}
}

func historyUserWithText(content string) HistoryUserMessage {
	msg := HistoryUserMessage{}
	msg.UserInputMessage.Content = content
	msg.UserInputMessage.ModelId = ModelSonnet46
	msg.UserInputMessage.Origin = "AI_EDITOR"
	return msg
}

func TestBuildCodeWhispererRequest_SessionContinuity(t *testing.T) {
	req1 := AnthropicRequest{
		Messages: []AnthropicRequestMessage{
			{Role: "user", Content: "Hello"},
		},
	}
	req2 := AnthropicRequest{
		Messages: []AnthropicRequestMessage{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there!"},
			{Role: "user", Content: "How are you?"},
		},
	}

	cwReq1 := BuildCodeWhispererRequest(req1)
	cwReq2 := BuildCodeWhispererRequest(req2)

	if cwReq1.ConversationState.ConversationId != cwReq2.ConversationState.ConversationId {
		t.Errorf("ConversationId mismatch: %s != %s", cwReq1.ConversationState.ConversationId, cwReq2.ConversationState.ConversationId)
	}
}

func TestBuildCodeWhispererRequest_ToolMapping(t *testing.T) {
	req := AnthropicRequest{
		Messages: []AnthropicRequestMessage{
			{Role: "user", Content: "Call tool"},
			{Role: "assistant", Content: []interface{}{
				map[string]interface{}{
					"type": "tool_use",
					"id":   "tool-1",
					"name": "my_tool",
					"input": map[string]interface{}{
						"arg": "val",
					},
				},
			}},
			{Role: "user", Content: []interface{}{
				map[string]interface{}{
					"type":        "tool_result",
					"tool_use_id": "tool-1",
					"content":     "Result content",
					"is_error":    false,
				},
			}},
		},
	}

	cwReq := BuildCodeWhispererRequest(req)

	history := cwReq.ConversationState.History
	if len(history) != 2 {
		t.Fatalf("Expected history length 2, got %d", len(history))
	}

	userMsg0, ok := history[0].(HistoryUserMessage)
	if !ok {
		t.Fatalf("Expected first history item to be HistoryUserMessage, got %T", history[0])
	}
	if userMsg0.UserInputMessage.Content != "Call tool" {
		t.Errorf("Expected first history item content 'Call tool', got %s", userMsg0.UserInputMessage.Content)
	}

	assistantMsg, ok := history[1].(HistoryAssistantMessage)
	if !ok {
		t.Fatalf("Expected second history item to be HistoryAssistantMessage, got %T", history[1])
	}
	if len(assistantMsg.AssistantResponseMessage.ToolUses) != 1 {
		t.Fatalf("Expected 1 tool use in history, got %d", len(assistantMsg.AssistantResponseMessage.ToolUses))
	}
	toolUse := assistantMsg.AssistantResponseMessage.ToolUses[0].(map[string]interface{})
	if toolUse["name"] != "my_tool" {
		t.Errorf("Expected tool name my_tool, got %v", toolUse["name"])
	}

	currentContext := cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext
	if len(currentContext.ToolResults) != 1 {
		t.Fatalf("Expected 1 tool result in current message context, got %d", len(currentContext.ToolResults))
	}
	result := currentContext.ToolResults[0]
	if result.ToolUseId != "tool-1" {
		t.Errorf("Expected tool use id tool-1, got %s", result.ToolUseId)
	}
	if result.Content[0].Text != "Result content" {
		t.Errorf("Expected result content 'Result content', got %s", result.Content[0].Text)
	}
}
