package proxy

import (
	"encoding/json"
	"log"
	"sort"
	"strings"

	"github.com/ryolambert/openkiro/internal/protocol"
)

// ResponseModelID returns the model ID to use in the response.
func ResponseModelID(cwReq CodeWhispererRequest, anthropicReq AnthropicRequest) string {
	modelID := strings.TrimSpace(cwReq.ConversationState.CurrentMessage.UserInputMessage.ModelId)
	if modelID != "" {
		return modelID
	}
	return anthropicReq.Model
}

// AssembleAnthropicResponse translates CodeWhisperer events into an Anthropic response.
func AssembleAnthropicResponse(events []protocol.SSEEvent) TranslatedAnthropicResponse {
	type blockAccumulator struct {
		AnthropicResponseBlock
	}

	blocks := map[int]*blockAccumulator{}
	order := []int{}
	stopReason := ""
	outputTokens := 0

	ensureBlock := func(index int, blockType string) *blockAccumulator {
		if block, ok := blocks[index]; ok {
			if blockType != "" && block.Type == "" {
				block.Type = blockType
			}
			return block
		}

		block := &blockAccumulator{}
		block.Type = blockType
		blocks[index] = block
		order = append(order, index)
		return block
	}

	for _, event := range events {
		dataMap, ok := event.Data.(map[string]any)
		if !ok {
			continue
		}

		switch dataMap["type"] {
		case "content_block_start":
			index := eventIndex(dataMap["index"])
			contentBlock, _ := dataMap["content_block"].(map[string]any)
			blockType, _ := contentBlock["type"].(string)
			block := ensureBlock(index, blockType)
			if blockType == "tool_use" {
				block.ToolUseID, _ = contentBlock["id"].(string)
				block.ToolName, _ = contentBlock["name"].(string)
				if input, ok := contentBlock["input"].(map[string]any); ok {
					block.ToolInput = input
				}
			}
		case "content_block_delta":
			index := eventIndex(dataMap["index"])
			deltaMap, _ := dataMap["delta"].(map[string]any)
			deltaType, _ := deltaMap["type"].(string)
			blockType := ""
			if deltaType == "text_delta" {
				blockType = "text"
			} else if deltaType == "input_json_delta" {
				blockType = "tool_use"
			}
			block := ensureBlock(index, blockType)

			switch deltaType {
			case "text_delta":
				text, _ := deltaMap["text"].(string)
				block.Text += text
				outputTokens++
			case "input_json_delta":
				if block.ToolUseID == "" {
					block.ToolUseID, _ = deltaMap["id"].(string)
				}
				if block.ToolName == "" {
					block.ToolName, _ = deltaMap["name"].(string)
				}
				switch partial := deltaMap["partial_json"].(type) {
				case string:
					block.RawInput += partial
				case *string:
					if partial != nil {
						block.RawInput += *partial
					}
				}
				outputTokens++
			}
		case "message_delta":
			if deltaMap, ok := dataMap["delta"].(map[string]any); ok {
				if reason, _ := deltaMap["stop_reason"].(string); reason != "" {
					stopReason = reason
				}
			}
		}
	}

	sort.Ints(order)
	translated := TranslatedAnthropicResponse{StopReason: stopReason, OutputTokens: outputTokens}
	translated.Blocks = make([]AnthropicResponseBlock, 0, len(order))

	for _, index := range order {
		block := blocks[index]
		if block == nil || block.Type == "" {
			continue
		}

		if block.Type == "tool_use" {
			if strings.TrimSpace(block.RawInput) != "" {
				toolInput := map[string]any{}
				if err := json.Unmarshal([]byte(block.RawInput), &toolInput); err != nil {
					log.Printf("tool input unmarshal error: %v", err)
				} else {
					block.ToolInput = toolInput
				}
			}
			if block.ToolInput == nil {
				block.ToolInput = map[string]any{}
			}
		}

		translated.Blocks = append(translated.Blocks, block.AnthropicResponseBlock)
	}

	if translated.StopReason == "" {
		translated.StopReason = "end_turn"
		if len(translated.Blocks) > 0 && translated.Blocks[len(translated.Blocks)-1].Type == "tool_use" {
			translated.StopReason = "tool_use"
		}
	}

	return translated
}

func eventIndex(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case float64:
		return int(v)
	default:
		return 0
	}
}

// BuildAnthropicResponsePayload builds the final JSON response payload.
func BuildAnthropicResponsePayload(conversationId, model string, inputTokens int, translated TranslatedAnthropicResponse) map[string]any {
	content := make([]map[string]any, 0, len(translated.Blocks))
	for _, block := range translated.Blocks {
		switch block.Type {
		case "text":
			content = append(content, map[string]any{
				"type": "text",
				"text": block.Text,
			})
		case "tool_use":
			content = append(content, map[string]any{
				"type":  "tool_use",
				"id":    block.ToolUseID,
				"name":  block.ToolName,
				"input": block.ToolInput,
			})
		}
	}

	return map[string]any{
		"content":         content,
		"model":           model,
		"role":            "assistant",
		"stop_reason":     translated.StopReason,
		"stop_sequence":   nil,
		"type":            "message",
		"conversation_id": conversationId,
		"usage": map[string]any{
			"input_tokens":  inputTokens,
			"output_tokens": translated.OutputTokens,
		},
	}
}

// BuildAnthropicStreamEvents builds the complete SSE event sequence for streaming.
func BuildAnthropicStreamEvents(conversationId, messageId, model string, inputTokens int, translated TranslatedAnthropicResponse) []protocol.SSEEvent {
	events := []protocol.SSEEvent{{
		Event: "message_start",
		Data: map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":              messageId,
				"type":            "message",
				"role":            "assistant",
				"content":         []any{},
				"model":           model,
				"stop_reason":     nil,
				"stop_sequence":   nil,
				"conversation_id": conversationId,
				"usage": map[string]any{
					"input_tokens":  inputTokens,
					"output_tokens": 1,
				},
			},
		},
	}, {
		Event: "ping",
		Data:  map[string]string{"type": "ping"},
	}}

	for index, block := range translated.Blocks {
		switch block.Type {
		case "text":
			events = append(events,
				protocol.SSEEvent{
					Event: "content_block_start",
					Data: map[string]any{
						"type":  "content_block_start",
						"index": index,
						"content_block": map[string]any{
							"type": "text",
							"text": "",
						},
					},
				},
				protocol.SSEEvent{
					Event: "content_block_delta",
					Data: map[string]any{
						"type":  "content_block_delta",
						"index": index,
						"delta": map[string]any{
							"type": "text_delta",
							"text": block.Text,
						},
					},
				},
				protocol.SSEEvent{
					Event: "content_block_stop",
					Data: map[string]any{
						"type":  "content_block_stop",
						"index": index,
					},
				},
			)
		case "tool_use":
			events = append(events, protocol.SSEEvent{
				Event: "content_block_start",
				Data: map[string]any{
					"type":  "content_block_start",
					"index": index,
					"content_block": map[string]any{
						"type":  "tool_use",
						"id":    block.ToolUseID,
						"name":  block.ToolName,
						"input": map[string]any{},
					},
				},
			})

			if len(block.ToolInput) > 0 {
				if rawInput, err := json.Marshal(block.ToolInput); err == nil {
					events = append(events, protocol.SSEEvent{
						Event: "content_block_delta",
						Data: map[string]any{
							"type":  "content_block_delta",
							"index": index,
							"delta": map[string]any{
								"type":         "input_json_delta",
								"id":           block.ToolUseID,
								"name":         block.ToolName,
								"partial_json": string(rawInput),
							},
						},
					})
				}
			}

			events = append(events, protocol.SSEEvent{
				Event: "content_block_stop",
				Data: map[string]any{
					"type":  "content_block_stop",
					"index": index,
				},
			})
		}
	}

	events = append(events,
		protocol.SSEEvent{
			Event: "message_delta",
			Data: map[string]any{
				"type": "message_delta",
				"delta": map[string]any{
					"stop_reason":   translated.StopReason,
					"stop_sequence": nil,
				},
				"usage": map[string]any{"output_tokens": translated.OutputTokens},
			},
		},
		protocol.SSEEvent{
			Event: "message_stop",
			Data:  map[string]any{"type": "message_stop"},
		},
	)

	return events
}
