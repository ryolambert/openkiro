package main

import (
	"strings"
	"testing"
)

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

	cwReq := buildCodeWhispererRequest(req)
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
	orig := maxPayloadBytes
	maxPayloadBytes = 200000 // small limit to trigger trimming in test
	t.Cleanup(func() { maxPayloadBytes = orig })

	cwReq := CodeWhispererRequest{}
	cwReq.ConversationState.CurrentMessage.UserInputMessage.Content = "current"
	cwReq.ConversationState.History = []any{
		historyUserWithText("first-" + strings.Repeat("a", 90000)),
		historyUserWithText("second-" + strings.Repeat("b", 90000)),
		historyUserWithText("third-" + strings.Repeat("c", 90000)),
		historyUserWithText("fourth-" + strings.Repeat("d", 90000)),
	}

	data, err := ensurePayloadFits(&cwReq)
	if err != nil {
		t.Fatalf("ensurePayloadFits returned error: %v", err)
	}
	if len(data) > maxPayloadBytes {
		t.Fatalf("payload still exceeds limit: %d > %d", len(data), maxPayloadBytes)
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

	trimmed := keepMostRecentHistory(history, 2)
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
	msg.UserInputMessage.ModelId = modelSonnet46
	msg.UserInputMessage.Origin = "AI_EDITOR"
	return msg
}
