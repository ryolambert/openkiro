package proxy

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"testing"

	"github.com/ryolambert/openkiro/internal/protocol"
)

func TestAssembleAnthropicResponseMixedTextAndToolUse(t *testing.T) {
	input := `{"query":"weather"}`
	events := protocol.ParseEvents(testFrames(t,
		map[string]any{"content": "Need tool"},
		map[string]any{"toolUseId": "toolu_1", "name": "lookup"},
		map[string]any{"toolUseId": "toolu_1", "name": "lookup", "input": input},
		map[string]any{"toolUseId": "toolu_1", "stop": true},
	))

	translated := AssembleAnthropicResponse(events)
	if translated.StopReason != "tool_use" {
		t.Fatalf("expected tool_use stop reason, got %q", translated.StopReason)
	}
	if translated.OutputTokens != 2 {
		t.Fatalf("expected 2 output token units, got %d", translated.OutputTokens)
	}
	if len(translated.Blocks) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(translated.Blocks))
	}

	if translated.Blocks[0].Type != "text" || translated.Blocks[0].Text != "Need tool" {
		t.Fatalf("unexpected first block: %#v", translated.Blocks[0])
	}
	if translated.Blocks[1].Type != "tool_use" || translated.Blocks[1].ToolUseID != "toolu_1" || translated.Blocks[1].ToolName != "lookup" {
		t.Fatalf("unexpected tool block: %#v", translated.Blocks[1])
	}
	if got := translated.Blocks[1].ToolInput["query"]; got != "weather" {
		t.Fatalf("expected tool input query weather, got %#v", got)
	}
}

func TestBuildAnthropicResponsePayloadUsesResolvedModel(t *testing.T) {
	cwReq := CodeWhispererRequest{}
	cwReq.ConversationState.CurrentMessage.UserInputMessage.ModelId = "CLAUDE_SONNET_4_5"

	payload := BuildAnthropicResponsePayload(
		"conv-123",
		ResponseModelID(cwReq, AnthropicRequest{Model: "claude-sonnet-4-5-20250929"}),
		11,
		TranslatedAnthropicResponse{
			Blocks:       []AnthropicResponseBlock{{Type: "text", Text: "done"}},
			StopReason:   "end_turn",
			OutputTokens: 1,
		},
	)

	if got := payload["model"]; got != "CLAUDE_SONNET_4_5" {
		t.Fatalf("expected resolved model ID, got %#v", got)
	}
	if got := payload["stop_reason"]; got != "end_turn" {
		t.Fatalf("expected end_turn stop reason, got %#v", got)
	}
}

func TestBuildAnthropicStreamEventsUsesTranslatedBlocks(t *testing.T) {
	translated := TranslatedAnthropicResponse{
		Blocks: []AnthropicResponseBlock{
			{Type: "text", Text: "Need tool"},
			{Type: "tool_use", ToolUseID: "toolu_1", ToolName: "lookup", ToolInput: map[string]any{"query": "weather"}},
		},
		StopReason:   "tool_use",
		OutputTokens: 2,
	}

	events := BuildAnthropicStreamEvents("conv-123", "msg_123", "CLAUDE_SONNET_4_5", 11, translated)
	if len(events) != 10 {
		t.Fatalf("expected 10 stream events, got %d", len(events))
	}

	message := events[0].Data.(map[string]any)["message"].(map[string]any)
	if got := message["model"]; got != "CLAUDE_SONNET_4_5" {
		t.Fatalf("expected resolved model in stream message_start, got %#v", got)
	}

	toolDelta := events[6].Data.(map[string]any)["delta"].(map[string]any)
	if got := toolDelta["partial_json"]; got != `{"query":"weather"}` {
		t.Fatalf("expected marshaled tool input, got %#v", got)
	}

	messageDelta := events[8].Data.(map[string]any)["delta"].(map[string]any)
	if got := messageDelta["stop_reason"]; got != "tool_use" {
		t.Fatalf("expected tool_use stop reason, got %#v", got)
	}
}

func testFrames(t *testing.T, payloads ...map[string]any) []byte {
	t.Helper()

	var out bytes.Buffer
	for _, payload := range payloads {
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		totalLen := uint32(len(data) + 12)
		if err := binary.Write(&out, binary.BigEndian, totalLen); err != nil {
			t.Fatalf("write totalLen: %v", err)
		}
		if err := binary.Write(&out, binary.BigEndian, uint32(0)); err != nil {
			t.Fatalf("write headerLen: %v", err)
		}
		if _, err := out.Write(data); err != nil {
			t.Fatalf("write payload: %v", err)
		}
		if err := binary.Write(&out, binary.BigEndian, uint32(0)); err != nil {
			t.Fatalf("write crc: %v", err)
		}
	}

	return out.Bytes()
}
