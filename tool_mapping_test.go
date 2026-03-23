package main

import (
	"testing"
)

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

	cwReq1 := buildCodeWhispererRequest(req1)
	cwReq2 := buildCodeWhispererRequest(req2)

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

	cwReq := buildCodeWhispererRequest(req)

	// Check History items
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

	// Check Current Message ToolResults
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
