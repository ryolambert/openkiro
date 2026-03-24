package protocol

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"log"
	"strings"
)

// Debug controls verbose logging in the protocol package.
// Set to true from the main package when debug mode is enabled.
var Debug bool

type assistantResponseEvent struct {
	Content   string  `json:"content"`
	Input     *string `json:"input,omitempty"`
	Name      string  `json:"name"`
	ToolUseId string  `json:"toolUseId"`
	Stop      bool    `json:"stop"`
}

type SSEEvent struct {
	Event string      `json:"event"`
	Data  interface{} `json:"data"`
}

type parserState struct {
	currentBlockType  string
	currentBlockIndex int
	currentToolUseID  string
	currentToolName   string
	nextBlockIndex    int
}

func ParseEvents(resp []byte) []SSEEvent {

	events := []SSEEvent{}
	state := parserState{currentBlockIndex: -1}

	r := bytes.NewReader(resp)
	for {
		if r.Len() < 12 {
			break
		}

		var totalLen, headerLen uint32
		if err := binary.Read(r, binary.BigEndian, &totalLen); err != nil {
			break
		}
		if err := binary.Read(r, binary.BigEndian, &headerLen); err != nil {
			break
		}

		if int(totalLen) > r.Len()+8 {
			if Debug {
				log.Println("Frame length invalid")
			}
			break
		}

		// Skip header
		header := make([]byte, headerLen)
		if _, err := io.ReadFull(r, header); err != nil {
			break
		}

		payloadLen := int(totalLen) - int(headerLen) - 12
		payload := make([]byte, payloadLen)
		if _, err := io.ReadFull(r, payload); err != nil {
			break
		}

		// Skip CRC32
		if _, err := r.Seek(4, io.SeekCurrent); err != nil {
			break
		}

		// Handle binary framing and clean up payload
		payloadStr := string(payload)
		if idx := strings.Index(payloadStr, "{"); idx != -1 {
			payloadStr = payloadStr[idx:]
		}

		// First try parsing as assistantResponseEvent
		var assistantEvt assistantResponseEvent
		if err := json.Unmarshal([]byte(payloadStr), &assistantEvt); err == nil && (assistantEvt.Content != "" || assistantEvt.ToolUseId != "" || assistantEvt.Stop) {
			appendAssistantEvent(&events, &state, assistantEvt)
			continue
		}

		// Handling 2026+ metadata events (metering, context usage)
		var metaData map[string]any
		if err := json.Unmarshal([]byte(payloadStr), &metaData); err == nil {
			if _, exists := metaData["contextUsagePercentage"]; exists {
				// Translate to a ping/metadata event for Claude Code
				events = append(events, SSEEvent{
					Event: "ping",
					Data:  map[string]any{"type": "ping", "metadata": metaData},
				})
			} else if _, exists := metaData["unit"]; exists {
				if Debug {
					log.Printf("Usage: %v %v", metaData["usage"], metaData["unit"])
				}
			}
		}
	}

	return events
}

func appendAssistantEvent(events *[]SSEEvent, state *parserState, evt assistantResponseEvent) {
	switch {
	case evt.Content != "":
		if state.currentBlockType != "text" {
			closeCurrentBlock(events, state)
			startTextBlock(events, state)
		}
		*events = append(*events, SSEEvent{
			Event: "content_block_delta",
			Data: map[string]interface{}{
				"type":  "content_block_delta",
				"index": state.currentBlockIndex,
				"delta": map[string]interface{}{
					"type": "text_delta",
					"text": evt.Content,
				},
			},
		})
	case evt.ToolUseId != "":
		toolName := evt.Name
		if toolName == "" {
			toolName = state.currentToolName
		}

		if state.currentBlockType != "tool_use" || state.currentToolUseID != evt.ToolUseId {
			closeCurrentBlock(events, state)
			startToolBlock(events, state, evt.ToolUseId, toolName)
		}

		if evt.Input != nil {
			*events = append(*events, SSEEvent{
				Event: "content_block_delta",
				Data: map[string]interface{}{
					"type":  "content_block_delta",
					"index": state.currentBlockIndex,
					"delta": map[string]interface{}{
						"type":         "input_json_delta",
						"id":           evt.ToolUseId,
						"name":         toolName,
						"partial_json": *evt.Input,
					},
				},
			})
		}

		if evt.Stop {
			closeCurrentBlock(events, state)
			appendMessageDelta(events, "tool_use")
		}
	case evt.Stop:
		closeCurrentBlock(events, state)
		appendMessageDelta(events, "end_turn")
	}
}

func startTextBlock(events *[]SSEEvent, state *parserState) {
	index := state.nextBlockIndex
	state.nextBlockIndex++
	state.currentBlockType = "text"
	state.currentBlockIndex = index
	state.currentToolUseID = ""
	state.currentToolName = ""

	*events = append(*events, SSEEvent{
		Event: "content_block_start",
		Data: map[string]interface{}{
			"type":  "content_block_start",
			"index": index,
			"content_block": map[string]interface{}{
				"type": "text",
				"text": "",
			},
		},
	})
}

func startToolBlock(events *[]SSEEvent, state *parserState, toolUseID, name string) {
	index := state.nextBlockIndex
	state.nextBlockIndex++
	state.currentBlockType = "tool_use"
	state.currentBlockIndex = index
	state.currentToolUseID = toolUseID
	state.currentToolName = name

	*events = append(*events, SSEEvent{
		Event: "content_block_start",
		Data: map[string]interface{}{
			"type":  "content_block_start",
			"index": index,
			"content_block": map[string]interface{}{
				"type":  "tool_use",
				"id":    toolUseID,
				"name":  name,
				"input": map[string]interface{}{},
			},
		},
	})
}

func closeCurrentBlock(events *[]SSEEvent, state *parserState) {
	if state.currentBlockType == "" || state.currentBlockIndex < 0 {
		return
	}

	*events = append(*events, SSEEvent{
		Event: "content_block_stop",
		Data: map[string]interface{}{
			"type":  "content_block_stop",
			"index": state.currentBlockIndex,
		},
	})

	state.currentBlockType = ""
	state.currentBlockIndex = -1
	state.currentToolUseID = ""
	state.currentToolName = ""
}

func appendMessageDelta(events *[]SSEEvent, stopReason string) {
	*events = append(*events, SSEEvent{
		Event: "message_delta",
		Data: map[string]interface{}{
			"type": "message_delta",
			"delta": map[string]interface{}{
				"stop_reason":   stopReason,
				"stop_sequence": nil,
			},
			"usage": map[string]interface{}{"output_tokens": 0},
		},
	})
}
