package proxy

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ResolveModelID maps a requested model alias to a CodeWhisperer model ID.
func ResolveModelID(requested string) string {
	key := strings.ToLower(strings.TrimSpace(requested))
	if key == "" {
		return ModelSonnet46
	}
	if v, ok := ModelMap[key]; ok {
		return v
	}

	if strings.HasPrefix(key, "claude_") {
		return strings.ToUpper(key)
	}

	switch {
	case strings.Contains(key, "default"):
		return ModelSonnet46
	case strings.Contains(key, "sonnet") && strings.Contains(key, "4-5"):
		return ModelSonnet45
	case strings.Contains(key, "sonnet") && strings.Contains(key, "4.5"):
		return ModelSonnet45
	case strings.Contains(key, "sonnet"):
		return ModelSonnet46
	case strings.Contains(key, "opus"):
		return ModelOpus46
	case strings.Contains(key, "haiku"):
		return ModelHaiku45
	default:
		return ModelSonnet46
	}
}

// GenerateUUID generates a simple UUID v4.
func GenerateUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("generate UUID entropy: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// GenerateDeterministicUUID generates a stable UUID based on input hash.
func GenerateDeterministicUUID(seed string) string {
	hash := sha256.Sum256([]byte(seed))
	b := hash[:16]
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// TruncateString truncates a string to maxLen, appending "..." if truncated.
func TruncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// SimplifySchema recursively simplifies a JSON schema to reduce payload size.
func SimplifySchema(schema map[string]any, depth int) map[string]any {
	if depth > 3 {
		if t, ok := schema["type"]; ok {
			return map[string]any{"type": t}
		}
		return map[string]any{"type": "object"}
	}

	result := make(map[string]any)

	for _, key := range []string{"type", "required", "enum", "const", "additionalProperties"} {
		if v, ok := schema[key]; ok {
			result[key] = v
		}
	}

	if props, ok := schema["properties"]; ok {
		if propsMap, ok := props.(map[string]any); ok {
			simplifiedProps := make(map[string]any)
			for propName, propVal := range propsMap {
				if propSchema, ok := propVal.(map[string]any); ok {
					simplifiedProps[propName] = SimplifySchema(propSchema, depth+1)
				} else {
					simplifiedProps[propName] = propVal
				}
			}
			result["properties"] = simplifiedProps
		}
	}

	if items, ok := schema["items"]; ok {
		if itemsMap, ok := items.(map[string]any); ok {
			result["items"] = SimplifySchema(itemsMap, depth+1)
		} else {
			result["items"] = items
		}
	}

	for _, combiner := range []string{"anyOf", "oneOf", "allOf"} {
		if arr, ok := schema[combiner]; ok {
			if arrSlice, ok := arr.([]any); ok {
				var simplified []any
				for _, item := range arrSlice {
					if itemMap, ok := item.(map[string]any); ok {
						simplified = append(simplified, SimplifySchema(itemMap, depth+1))
					} else {
						simplified = append(simplified, item)
					}
				}
				result[combiner] = simplified
			}
		}
	}

	return result
}

// BuildCodeWhispererTools converts Anthropic tools to CodeWhisperer format.
func BuildCodeWhispererTools(tools []AnthropicTool) []CodeWhispererTool {
	var cwTools []CodeWhispererTool
	for _, tool := range tools {
		cwTool := CodeWhispererTool{}
		cwTool.ToolSpecification.Name = tool.Name
		cwTool.ToolSpecification.Description = TruncateString(tool.Description, MaxToolDescLen)
		cwTool.ToolSpecification.InputSchema = InputSchema{
			Json: SimplifySchema(tool.InputSchema, 0),
		}
		cwTools = append(cwTools, cwTool)
	}
	return cwTools
}

// EnsurePayloadFits serializes the request and progressively trims until it fits.
func EnsurePayloadFits(cwReq *CodeWhispererRequest) ([]byte, error) {
	data, err := json.Marshal(cwReq)
	if err != nil {
		return nil, err
	}

	if len(data) <= MaxPayloadBytes {
		return data, nil
	}

	debugLogf("[payload-trim] initial size %d bytes, limit %d", len(data), MaxPayloadBytes)

	for len(data) > MaxPayloadBytes && len(cwReq.ConversationState.History) > 0 {
		cwReq.ConversationState.History = TrimOldestHistoryMessage(cwReq.ConversationState.History)
		data, err = json.Marshal(cwReq)
		if err != nil {
			return nil, err
		}
	}

	if len(data) <= MaxPayloadBytes {
		debugLogf("[payload-trim] fit after history trim: %d bytes", len(data))
		return data, nil
	}

	tools := cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools
	for i := range tools {
		tools[i].ToolSpecification.Description = TruncateString(tools[i].ToolSpecification.Description, 100)
	}
	data, err = json.Marshal(cwReq)
	if err != nil {
		return nil, err
	}

	if len(data) <= MaxPayloadBytes {
		debugLogf("[payload-trim] fit after desc trim: %d bytes", len(data))
		return data, nil
	}

	for i := range tools {
		tools[i].ToolSpecification.InputSchema = InputSchema{Json: map[string]any{"type": "object"}}
	}
	data, err = json.Marshal(cwReq)
	if err != nil {
		return nil, err
	}

	if len(data) <= MaxPayloadBytes {
		debugLogf("[payload-trim] fit after schema strip: %d bytes", len(data))
		return data, nil
	}

	cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools = nil
	data, err = json.Marshal(cwReq)
	if err != nil {
		return nil, err
	}
	debugLogf("[payload-trim] dropped all tools, final size: %d bytes", len(data))
	return data, nil
}

// TrimOldestHistoryMessage removes the oldest history entry.
func TrimOldestHistoryMessage(history []any) []any {
	if len(history) == 0 {
		return history
	}
	return history[1:]
}

// KeepMostRecentHistory keeps only the most recent entries.
func KeepMostRecentHistory(history []any, keep int) []any {
	if keep <= 0 || len(history) == 0 {
		return nil
	}
	if len(history) <= keep {
		return history
	}
	return append([]any(nil), history[len(history)-keep:]...)
}

// BuildSystemContext joins system messages into a single string.
func BuildSystemContext(system []AnthropicSystemMessage) string {
	parts := make([]string, 0, len(system))
	for _, sysMsg := range system {
		if text := strings.TrimSpace(sysMsg.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// BuildCurrentMessageContent builds the current message content with system context.
func BuildCurrentMessageContent(anthropicReq AnthropicRequest) string {
	lastMsg := anthropicReq.Messages[len(anthropicReq.Messages)-1]
	parts := make([]string, 0, 2)
	if systemContext := BuildSystemContext(anthropicReq.System); systemContext != "" {
		parts = append(parts, fmt.Sprintf("<context>\n%s\n</context>", systemContext))
	}
	parts = append(parts, fmt.Sprintf("<task>\n%s\n</task>", GetMessageContent(lastMsg.Content)))
	return strings.Join(parts, "\n\n")
}

// ExtractToolResults extracts tool_result blocks from an Anthropic message content.
func ExtractToolResults(content any) []struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
	Status    string `json:"status"`
	ToolUseId string `json:"toolUseId"`
} {
	type cwToolResult struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Status    string `json:"status"`
		ToolUseId string `json:"toolUseId"`
	}

	blocks, ok := content.([]interface{})
	if !ok {
		return nil
	}

	var results []cwToolResult
	for _, block := range blocks {
		m, ok := block.(map[string]interface{})
		if !ok {
			continue
		}
		blockType, _ := m["type"].(string)
		if blockType != "tool_result" {
			continue
		}

		toolUseId, _ := m["tool_use_id"].(string)
		if toolUseId == "" {
			continue
		}

		status := "success"
		if isErr, ok := m["is_error"].(bool); ok && isErr {
			status = "error"
		}

		var textBlocks []struct {
			Text string `json:"text"`
		}
		rawContent, hasContent := m["content"]
		if !hasContent || rawContent == nil {
			textBlocks = append(textBlocks, struct {
				Text string `json:"text"`
			}{Text: ""})
		} else {
			switch c := rawContent.(type) {
			case string:
				textBlocks = append(textBlocks, struct {
					Text string `json:"text"`
				}{Text: c})
			case []interface{}:
				for _, item := range c {
					if itemMap, ok := item.(map[string]interface{}); ok {
						if text, ok := itemMap["text"].(string); ok {
							textBlocks = append(textBlocks, struct {
								Text string `json:"text"`
							}{Text: text})
						} else {
							if data, err := json.Marshal(itemMap); err == nil {
								textBlocks = append(textBlocks, struct {
									Text string `json:"text"`
								}{Text: string(data)})
							}
						}
					}
				}
			default:
				if data, err := json.Marshal(rawContent); err == nil {
					textBlocks = append(textBlocks, struct {
						Text string `json:"text"`
					}{Text: string(data)})
				}
			}
		}

		results = append(results, cwToolResult{
			Content:   textBlocks,
			Status:    status,
			ToolUseId: toolUseId,
		})
	}

	type ret = struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Status    string `json:"status"`
		ToolUseId string `json:"toolUseId"`
	}
	var out []ret
	for _, r := range results {
		out = append(out, ret(r))
	}
	return out
}

// ExtractToolUses pulls tool_use blocks from an Anthropic assistant message content.
func ExtractToolUses(content any) []any {
	blocks, ok := content.([]interface{})
	if !ok {
		return nil
	}

	var toolUses []any
	for _, block := range blocks {
		m, ok := block.(map[string]interface{})
		if !ok {
			continue
		}
		blockType, _ := m["type"].(string)
		if blockType != "tool_use" {
			continue
		}

		toolUseId, _ := m["id"].(string)
		name, _ := m["name"].(string)
		input := m["input"]

		if toolUseId != "" && name != "" {
			inputObj := input
			if inputObj == nil {
				inputObj = map[string]any{}
			}
			toolUses = append(toolUses, map[string]any{
				"toolUseId": toolUseId,
				"name":      name,
				"input":     inputObj,
			})
		}
	}
	return toolUses
}

// HasToolResults checks if a message content contains tool_result blocks.
func HasToolResults(content any) bool {
	blocks, ok := content.([]interface{})
	if !ok {
		return false
	}
	for _, block := range blocks {
		if m, ok := block.(map[string]interface{}); ok {
			if t, _ := m["type"].(string); t == "tool_result" {
				return true
			}
		}
	}
	return false
}

// GetMessageContent extracts text content from a message.
func GetMessageContent(content any) string {
	if content == nil {
		return ""
	}
	switch v := content.(type) {
	case string:
		if len(v) == 0 {
			return ""
		}
		return v
	case []interface{}:
		var texts []string
		for _, block := range v {
			if m, ok := block.(map[string]interface{}); ok {
				blockType, _ := m["type"].(string)
				switch blockType {
				case "text":
					if text, ok := m["text"].(string); ok {
						texts = append(texts, text)
					}
				case "thought":
					if thought, ok := m["thought"].(string); ok {
						texts = append(texts, fmt.Sprintf("[Thinking: %s]", thought))
					}
				case "tool_result":
					rawContent, hasContent := m["content"]
					if !hasContent || rawContent == nil {
						if toolUseId, ok := m["tool_use_id"].(string); ok {
							texts = append(texts, fmt.Sprintf("[Tool result pending: %s]", toolUseId))
						}
						break
					}
					switch c := rawContent.(type) {
					case string:
						texts = append(texts, c)
					case []interface{}:
						for _, item := range c {
							if itemMap, ok := item.(map[string]interface{}); ok {
								itemType, _ := itemMap["type"].(string)
								if itemType == "text" {
									if text, ok := itemMap["text"].(string); ok {
										texts = append(texts, text)
									}
								} else {
									if data, err := json.Marshal(itemMap); err == nil {
										texts = append(texts, string(data))
									}
								}
							}
						}
					}
				case "tool_use":
					name, _ := m["name"].(string)
					id, _ := m["id"].(string)
					if name != "" {
						texts = append(texts, fmt.Sprintf("<tool_executed name=%q id=%q />", name, id))
					}
				case "tool_search":
					if query, ok := m["query"].(string); ok {
						texts = append(texts, fmt.Sprintf("[Tool search: %s]", query))
					}
				default:
					if data, err := json.Marshal(m); err == nil {
						texts = append(texts, string(data))
					}
				}
			}
		}
		if len(texts) == 0 {
			if s, err := json.Marshal(content); err == nil {
				return string(s)
			}
			return ""
		}
		return strings.Join(texts, "\n")
	default:
		s, err := json.Marshal(content)
		if err != nil {
			return ""
		}
		return string(s)
	}
}

// GetProfileArn returns the CodeWhisperer profileArn to use.
func GetProfileArn() string {
	if v := os.Getenv("KIRO_PROFILE_ARN"); v != "" {
		return v
	}
	return ""
}

// BuildCodeWhispererRequest builds a CodeWhisperer request from an Anthropic request.
func BuildCodeWhispererRequest(anthropicReq AnthropicRequest) CodeWhispererRequest {
	profileArn := GetProfileArn()
	cwReq := CodeWhispererRequest{
		ProfileArn: profileArn,
	}

	resolvedModel := ResolveModelID(anthropicReq.Model)
	if profileArn == "" {
		switch resolvedModel {
		case ModelSonnet46, ModelSonnet45, ModelOpus46:
			resolvedModel = ModelBuilderSonnet45
		case ModelHaiku45:
			resolvedModel = ModelBuilderHaiku45
		}
	}
	cwReq.ConversationState.ChatTriggerType = "MANUAL"

	if anthropicReq.ConversationId != nil && *anthropicReq.ConversationId != "" {
		cwReq.ConversationState.ConversationId = *anthropicReq.ConversationId
	} else if len(anthropicReq.Messages) > 0 {
		firstMsg := anthropicReq.Messages[0]
		cwReq.ConversationState.ConversationId = GenerateDeterministicUUID(GetMessageContent(firstMsg.Content))
	} else {
		cwReq.ConversationState.ConversationId = GenerateUUID()
	}

	cwReq.ConversationState.CurrentMessage.UserInputMessage.Content = BuildCurrentMessageContent(anthropicReq)
	cwReq.ConversationState.CurrentMessage.UserInputMessage.ModelId = resolvedModel
	cwReq.ConversationState.CurrentMessage.UserInputMessage.Origin = "AI_EDITOR"

	if len(anthropicReq.Tools) > 0 {
		cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools = BuildCodeWhispererTools(anthropicReq.Tools)
	}

	if lastMsg := anthropicReq.Messages[len(anthropicReq.Messages)-1]; lastMsg.Role == "user" {
		if results := ExtractToolResults(lastMsg.Content); len(results) > 0 {
			cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.ToolResults = results
		}
	}

	history := make([]any, 0, len(anthropicReq.Messages)-1)
	for i := 0; i < len(anthropicReq.Messages)-1; i++ {
		msg := anthropicReq.Messages[i]
		content := GetMessageContent(msg.Content)
		if strings.TrimSpace(content) == "" && !HasToolResults(msg.Content) {
			continue
		}

		if msg.Role == "assistant" {
			assistantMsg := HistoryAssistantMessage{}
			assistantMsg.AssistantResponseMessage.Content = content
			assistantMsg.AssistantResponseMessage.ToolUses = ExtractToolUses(msg.Content)
			history = append(history, assistantMsg)
			continue
		}

		userMsg := HistoryUserMessage{}
		userMsg.UserInputMessage.Content = content
		userMsg.UserInputMessage.ModelId = resolvedModel
		userMsg.UserInputMessage.Origin = "AI_EDITOR"

		if results := ExtractToolResults(msg.Content); len(results) > 0 {
			userMsg.UserInputMessage.UserInputMessageContext.ToolResults = results
		}

		history = append(history, userMsg)
	}
	cwReq.ConversationState.History = history

	return cwReq
}

// debugLogf is a package-level debug logger that checks OPENKIRO_DEBUG.
func debugLogf(format string, args ...any) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OPENKIRO_DEBUG"))) {
	case "1", "true", "yes", "on", "debug":
		fmt.Fprintf(os.Stderr, format+"\n", args...)
	}
}
