package main

import (
	"bytes"
	"encoding/json"
	jsonStr "encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"crypto/sha256"
	"github.com/ryolambert/openkiro/protocol"
)

// TokenData defines the token file structure
type TokenData struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresAt    string `json:"expiresAt,omitempty"`
}

// RefreshRequest defines the token refresh request payload
type RefreshRequest struct {
	RefreshToken string `json:"refreshToken"`
}

// RefreshResponse defines the token refresh response payload
type RefreshResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresAt    string `json:"expiresAt,omitempty"`
}

// AnthropicTool defines the Anthropic API tool structure
type AnthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// InputSchema defines the tool input schema structure
type InputSchema struct {
	Json map[string]any `json:"json"`
}

// ToolSpecification defines the tool specification structure
type ToolSpecification struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema InputSchema `json:"inputSchema"`
}

// CodeWhispererTool defines the CodeWhisperer API tool structure
type CodeWhispererTool struct {
	ToolSpecification ToolSpecification `json:"toolSpecification"`
}

// HistoryUserMessage defines a user message in history
type HistoryUserMessage struct {
	UserInputMessage struct {
		Content                 string `json:"content"`
		ModelId                 string `json:"modelId"`
		Origin                  string `json:"origin"`
		UserInputMessageContext struct {
			ToolResults []struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
				Status    string `json:"status"`
				ToolUseId string `json:"toolUseId"`
			} `json:"toolResults,omitempty"`
		} `json:"userInputMessageContext,omitempty"`
	} `json:"userInputMessage"`
}

// HistoryAssistantMessage defines an assistant message in history
type HistoryAssistantMessage struct {
	AssistantResponseMessage struct {
		Content  string `json:"content"`
		ToolUses []any  `json:"toolUses"`
	} `json:"assistantResponseMessage"`
}

// AnthropicRequest defines the Anthropic API request structure
type AnthropicRequest struct {
	Model       string                    `json:"model"`
	MaxTokens   int                       `json:"max_tokens"`
	Messages    []AnthropicRequestMessage `json:"messages"`
	System      []AnthropicSystemMessage  `json:"system,omitempty"`
	Tools       []AnthropicTool           `json:"tools,omitempty"`
	Stream      bool                      `json:"stream"`
	Temperature *float64                  `json:"temperature,omitempty"`
	Metadata    map[string]any            `json:"metadata,omitempty"`
	// openkiro extensions
	ConversationId *string `json:"conversation_id,omitempty"`
}

// AnthropicStreamResponse defines the Anthropic streaming response structure
type AnthropicStreamResponse struct {
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentDelta struct {
		Text string `json:"text"`
		Type string `json:"type"`
	} `json:"delta,omitempty"`
	Content []struct {
		Text string `json:"text"`
		Type string `json:"type"`
	} `json:"content,omitempty"`
	StopReason   string `json:"stop_reason,omitempty"`
	StopSequence string `json:"stop_sequence,omitempty"`
	Usage        struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage,omitempty"`
}

// AnthropicRequestMessage defines the Anthropic API message structure
type AnthropicRequestMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // Can be string or []ContentBlock
}

type AnthropicSystemMessage struct {
	Type string `json:"type"`
	Text string `json:"text"` // Can be string or []ContentBlock
}

// ContentBlock defines the message content block structure
type ContentBlock struct {
	Type      string  `json:"type"`
	Text      *string `json:"text,omitempty"`
	ToolUseId *string `json:"tool_use_id,omitempty"`
	Content   *string `json:"content,omitempty"`
	Name      *string `json:"name,omitempty"`
	Input     *any    `json:"input,omitempty"`
}

// getMessageContent extracts text content from a message, handling latest block types
func getMessageContent(content any) string {
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
				case "thought": // 2025+ Thinking feature
					if thought, ok := m["thought"].(string); ok {
						texts = append(texts, fmt.Sprintf("[Thinking: %s]", thought))
					}
				case "tool_result":
					// tool_result content can be a string, array, or nil (e.g. exa deep research pending)
					rawContent, hasContent := m["content"]
					if !hasContent || rawContent == nil {
						// No content yet (async tool still running)
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
									// Support for images/other types in results as strings
									if data, err := jsonStr.Marshal(itemMap); err == nil {
										texts = append(texts, string(data))
									}
								}
							}
						}
					}
				case "tool_use":
					// Include tool info as XML metadata — NOT as a mimicable text pattern.
					// Using "[Tool call: name(args)]" caused the model to output tool calls
					// as text instead of using structured tool calling. XML tags give context
					// without creating a pattern the model copies.
					name, _ := m["name"].(string)
					id, _ := m["id"].(string)
					if name != "" {
						texts = append(texts, fmt.Sprintf("<tool_executed name=%q id=%q />", name, id))
					}
				case "tool_search": // 2026 agentic feature
					if query, ok := m["query"].(string); ok {
						texts = append(texts, fmt.Sprintf("[Tool search: %s]", query))
					}
				default:
					// Fallback for unknown block types
					if data, err := jsonStr.Marshal(m); err == nil {
						texts = append(texts, string(data))
					}
				}
			}
		}
		if len(texts) == 0 {
			if s, err := jsonStr.Marshal(content); err == nil {
				return string(s)
			}
			return ""
		}
		return strings.Join(texts, "\n")
	default:
		s, err := jsonStr.Marshal(content)
		if err != nil {
			return ""
		}
		return string(s)
	}
}

// CodeWhispererRequest defines the CodeWhisperer API request structure
type CodeWhispererRequest struct {
	ConversationState struct {
		ChatTriggerType string `json:"chatTriggerType"`
		ConversationId  string `json:"conversationId"`
		CurrentMessage  struct {
			UserInputMessage struct {
				Content                 string `json:"content"`
				ModelId                 string `json:"modelId"`
				Origin                  string `json:"origin"`
				UserInputMessageContext struct {
					ToolResults []struct {
						Content []struct {
							Text string `json:"text"`
						} `json:"content"`
						Status    string `json:"status"`
						ToolUseId string `json:"toolUseId"`
					} `json:"toolResults,omitempty"`
					Tools []CodeWhispererTool `json:"tools,omitempty"`
				} `json:"userInputMessageContext"`
			} `json:"userInputMessage"`
		} `json:"currentMessage"`
		History []any `json:"history"`
	} `json:"conversationState"`
	ProfileArn string `json:"profileArn,omitempty"`
}

// CodeWhispererEvent defines a CodeWhisperer event response
type CodeWhispererEvent struct {
	ContentType string `json:"content-type"`
	MessageType string `json:"message-type"`
	Content     string `json:"content"`
	EventType   string `json:"event-type"`
}

const (
	modelSonnet46 = "CLAUDE_SONNET_4_6_V1_0"
	modelSonnet45 = "CLAUDE_SONNET_4_5_20250929_V1_0"
	modelOpus46   = "CLAUDE_OPUS_4_6_V1_0"
	modelHaiku45  = "CLAUDE_HAIKU_4_5_20251001_V1_0"

	// Builder ID free tier models — these use dot-notation IDs, not underscore
	modelBuilderSonnet45 = "claude-sonnet-4.5"
	modelBuilderHaiku45  = "claude-haiku-4.5"
	modelBuilderSonnet35 = "CLAUDE_3_5_SONNET_20241022_V2_0" // last-resort fallback

	// IAM Identity Center profile ARN (paid/enterprise accounts)
	// Leave empty for Builder ID (free tier) accounts
	profileArnIAM = "arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK"

	// Payload safety limits for CodeWhisperer
	maxToolDescLen       = 200 // max characters per tool description
	serverReadTimeout    = 30 * time.Second
	serverWriteTimeout   = 60 * time.Second
	serverIdleTimeout    = 120 * time.Second
	serverHeaderTimeout  = 10 * time.Second
	upstreamHTTPTimeout  = 60 * time.Second
	defaultListenAddress = "127.0.0.1"
	defaultPort          = "1234"
	launchdLabel         = "com.openkiro.proxy"
)

var maxRequestBodyBytes int64 = 200 << 20 // 200 MiB max inbound request body

var maxPayloadBytes = 250000000 // ~ 250MB soft limit for total request JSON

var ModelMap = map[string]string{
	"default":                    modelSonnet45,
	"claude-sonnet-4-6":          modelSonnet46,
	"claude-sonnet-4-5":          modelSonnet45,
	"claude-sonnet-4-5-20250929": modelSonnet45,
	"claude-sonnet-4-20250514":   modelSonnet46,
	"claude-opus-4-6":            modelOpus46,
	"claude-haiku-4-5-20251001":  modelHaiku45,
	"claude-3-5-sonnet-20241022": modelSonnet46,
	"claude-3-5-haiku-20241022":  modelHaiku45,
	"claude-3-7-sonnet-20250219": modelSonnet46,
	"claude-3-7-haiku-20250219":  modelHaiku45,
	"claude-4-sonnet":            modelSonnet46,
	"claude-4-haiku":             modelHaiku45,
	"claude-4-opus":              modelOpus46,
}

func resolveModelID(requested string) string {
	key := strings.ToLower(strings.TrimSpace(requested))
	if key == "" {
		return modelSonnet46
	}
	if v, ok := ModelMap[key]; ok {
		return v
	}

	// Accept direct provider IDs.
	if strings.HasPrefix(key, "claude_") {
		return strings.ToUpper(key)
	}

	// Handle loose UI labels / aliases from ACP clients.
	switch {
	case strings.Contains(key, "default"):
		return modelSonnet46
	case strings.Contains(key, "sonnet") && strings.Contains(key, "4-5"):
		return modelSonnet45
	case strings.Contains(key, "sonnet") && strings.Contains(key, "4.5"):
		return modelSonnet45
	case strings.Contains(key, "sonnet"):
		return modelSonnet46
	case strings.Contains(key, "opus"):
		return modelOpus46
	case strings.Contains(key, "haiku"):
		return modelHaiku45
	default:
		// Safe default keeps Obsidian sessions working if it sends unknown aliases.
		return modelSonnet46
	}
}

// generateUUID generates a simple UUID v4
func generateUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // Version 4
	b[8] = (b[8] & 0x3f) | 0x80 // Variant bits
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// generateDeterministicUUID generates a stable UUID based on input hash
func generateDeterministicUUID(seed string) string {
	hash := sha256.Sum256([]byte(seed))
	b := hash[:16]
	b[6] = (b[6] & 0x0f) | 0x40 // Version 4
	b[8] = (b[8] & 0x3f) | 0x80 // Variant bits
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// truncateString truncates a string to maxLen, appending "..." if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// simplifySchema recursively simplifies a JSON schema to reduce payload size.
// It keeps property names, types, required fields, and enum values but strips
// verbose nested descriptions, examples, and deeply nested sub-schemas.
func simplifySchema(schema map[string]any, depth int) map[string]any {
	if depth > 3 {
		// Beyond depth 3, collapse to just the type
		if t, ok := schema["type"]; ok {
			return map[string]any{"type": t}
		}
		return map[string]any{"type": "object"}
	}

	result := make(map[string]any)

	// Always keep these keys
	for _, key := range []string{"type", "required", "enum", "const", "additionalProperties"} {
		if v, ok := schema[key]; ok {
			result[key] = v
		}
	}

	// Simplify "properties" recursively
	if props, ok := schema["properties"]; ok {
		if propsMap, ok := props.(map[string]any); ok {
			simplifiedProps := make(map[string]any)
			for propName, propVal := range propsMap {
				if propSchema, ok := propVal.(map[string]any); ok {
					simplifiedProps[propName] = simplifySchema(propSchema, depth+1)
				} else {
					simplifiedProps[propName] = propVal
				}
			}
			result["properties"] = simplifiedProps
		}
	}

	// Simplify "items" for arrays
	if items, ok := schema["items"]; ok {
		if itemsMap, ok := items.(map[string]any); ok {
			result["items"] = simplifySchema(itemsMap, depth+1)
		} else {
			result["items"] = items
		}
	}

	// Simplify "anyOf" / "oneOf" / "allOf"
	for _, combiner := range []string{"anyOf", "oneOf", "allOf"} {
		if arr, ok := schema[combiner]; ok {
			if arrSlice, ok := arr.([]any); ok {
				var simplified []any
				for _, item := range arrSlice {
					if itemMap, ok := item.(map[string]any); ok {
						simplified = append(simplified, simplifySchema(itemMap, depth+1))
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

// buildCodeWhispererTools converts Anthropic tools to CodeWhisperer format with
// truncation and schema simplification to keep the payload within safe limits.
func buildCodeWhispererTools(tools []AnthropicTool) []CodeWhispererTool {
	var cwTools []CodeWhispererTool
	for _, tool := range tools {
		cwTool := CodeWhispererTool{}
		cwTool.ToolSpecification.Name = tool.Name
		cwTool.ToolSpecification.Description = truncateString(tool.Description, maxToolDescLen)
		cwTool.ToolSpecification.InputSchema = InputSchema{
			Json: simplifySchema(tool.InputSchema, 0),
		}
		cwTools = append(cwTools, cwTool)
	}
	return cwTools
}

// ensurePayloadFits serializes the request and if it exceeds maxPayloadBytes,
// progressively trims history (oldest first) and further truncates tool
// descriptions until it fits. Returns the final serialized JSON.
func ensurePayloadFits(cwReq *CodeWhispererRequest) ([]byte, error) {
	data, err := jsonStr.Marshal(cwReq)
	if err != nil {
		return nil, err
	}

	// Fast path: already fits
	if len(data) <= maxPayloadBytes {
		return data, nil
	}

	debugLogf("[payload-trim] initial size %d bytes, limit %d", len(data), maxPayloadBytes)

	// Phase 1: Trim history from the front (oldest caller context first)
	for len(data) > maxPayloadBytes && len(cwReq.ConversationState.History) > 0 {
		cwReq.ConversationState.History = trimOldestHistoryMessage(cwReq.ConversationState.History)
		data, err = jsonStr.Marshal(cwReq)
		if err != nil {
			return nil, err
		}
	}

	if len(data) <= maxPayloadBytes {
		debugLogf("[payload-trim] fit after history trim: %d bytes", len(data))
		return data, nil
	}

	// Phase 2: Further truncate tool descriptions to 100 chars
	tools := cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools
	for i := range tools {
		tools[i].ToolSpecification.Description = truncateString(tools[i].ToolSpecification.Description, 100)
	}
	data, err = jsonStr.Marshal(cwReq)
	if err != nil {
		return nil, err
	}

	if len(data) <= maxPayloadBytes {
		debugLogf("[payload-trim] fit after desc trim: %d bytes", len(data))
		return data, nil
	}

	// Phase 3: Strip tool schemas entirely (keep only name + short description)
	for i := range tools {
		tools[i].ToolSpecification.InputSchema = InputSchema{Json: map[string]any{"type": "object"}}
	}
	data, err = jsonStr.Marshal(cwReq)
	if err != nil {
		return nil, err
	}

	if len(data) <= maxPayloadBytes {
		debugLogf("[payload-trim] fit after schema strip: %d bytes", len(data))
		return data, nil
	}

	// Phase 4: Drop tools entirely as last resort
	cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools = nil
	data, err = jsonStr.Marshal(cwReq)
	if err != nil {
		return nil, err
	}
	debugLogf("[payload-trim] dropped all tools, final size: %d bytes", len(data))
	return data, nil
}

func trimOldestHistoryMessage(history []any) []any {
	if len(history) == 0 {
		return history
	}
	return history[1:]
}

func keepMostRecentHistory(history []any, keep int) []any {
	if keep <= 0 || len(history) == 0 {
		return nil
	}
	if len(history) <= keep {
		return history
	}
	return append([]any(nil), history[len(history)-keep:]...)
}

func buildSystemContext(system []AnthropicSystemMessage) string {
	parts := make([]string, 0, len(system))
	for _, sysMsg := range system {
		if text := strings.TrimSpace(sysMsg.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func buildCurrentMessageContent(anthropicReq AnthropicRequest) string {
	lastMsg := anthropicReq.Messages[len(anthropicReq.Messages)-1]
	parts := make([]string, 0, 2)
	if systemContext := buildSystemContext(anthropicReq.System); systemContext != "" {
		parts = append(parts, fmt.Sprintf("<context>\n%s\n</context>", systemContext))
	}
	parts = append(parts, fmt.Sprintf("<task>\n%s\n</task>", getMessageContent(lastMsg.Content)))
	return strings.Join(parts, "\n\n")
}

// extractToolResults extracts tool_result blocks from an Anthropic message content
// and returns them in CodeWhisperer toolResults format.
func extractToolResults(content any) []struct {
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

		// Extract text content from tool_result
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
							if data, err := jsonStr.Marshal(itemMap); err == nil {
								textBlocks = append(textBlocks, struct {
									Text string `json:"text"`
								}{Text: string(data)})
							}
						}
					}
				}
			default:
				if data, err := jsonStr.Marshal(rawContent); err == nil {
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

	// Return type matches the struct definition in CodeWhispererRequest
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

// extractToolUses pulls tool_use blocks from an Anthropic assistant message content
// and returns them for the CodeWhisperer history toolUses field.
func extractToolUses(content any) []any {
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

// hasToolResults checks if a message content contains tool_result blocks.
func hasToolResults(content any) bool {
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

// buildCodeWhispererRequest builds a CodeWhisperer request
func buildCodeWhispererRequest(anthropicReq AnthropicRequest) CodeWhispererRequest {
	profileArn := getProfileArn()
	cwReq := CodeWhispererRequest{
		ProfileArn: profileArn,
	}

	resolvedModel := resolveModelID(anthropicReq.Model)
	// Builder ID free tier only supports Claude 3.5 models.
	// Builder ID free tier uses dot-notation model IDs (e.g. "claude-sonnet-4.5")
	if profileArn == "" {
		switch resolvedModel {
		case modelSonnet46, modelSonnet45, modelOpus46:
			resolvedModel = modelBuilderSonnet45
		case modelHaiku45:
			resolvedModel = modelBuilderHaiku45
		}
	}
	cwReq.ConversationState.ChatTriggerType = "MANUAL"

	// Session continuity: use client-provided ID or a deterministic one based on the first message
	if anthropicReq.ConversationId != nil && *anthropicReq.ConversationId != "" {
		cwReq.ConversationState.ConversationId = *anthropicReq.ConversationId
	} else if len(anthropicReq.Messages) > 0 {
		// Heuristic: Use the first user message as a stable seed for the conversation.
		// Note: We skip potential system prompts or earlier turns to keep it stable.
		firstMsg := anthropicReq.Messages[0]
		cwReq.ConversationState.ConversationId = generateDeterministicUUID(getMessageContent(firstMsg.Content))
	} else {
		cwReq.ConversationState.ConversationId = generateUUID()
	}

	cwReq.ConversationState.CurrentMessage.UserInputMessage.Content = buildCurrentMessageContent(anthropicReq)
	cwReq.ConversationState.CurrentMessage.UserInputMessage.ModelId = resolvedModel
	cwReq.ConversationState.CurrentMessage.UserInputMessage.Origin = "AI_EDITOR"

	// Map tools information with truncation and schema simplification
	if len(anthropicReq.Tools) > 0 {
		cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools = buildCodeWhispererTools(anthropicReq.Tools)
	}

	// Extract tool results for the current message if they exist
	if lastMsg := anthropicReq.Messages[len(anthropicReq.Messages)-1]; lastMsg.Role == "user" {
		if results := extractToolResults(lastMsg.Content); len(results) > 0 {
			cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.ToolResults = results
		}
	}

	// Build history from caller-provided conversation only.
	history := make([]any, 0, len(anthropicReq.Messages)-1)
	for i := 0; i < len(anthropicReq.Messages)-1; i++ {
		msg := anthropicReq.Messages[i]
		content := getMessageContent(msg.Content)
		if strings.TrimSpace(content) == "" && !hasToolResults(msg.Content) {
			continue
		}

		if msg.Role == "assistant" {
			assistantMsg := HistoryAssistantMessage{}
			assistantMsg.AssistantResponseMessage.Content = content
			assistantMsg.AssistantResponseMessage.ToolUses = extractToolUses(msg.Content)
			history = append(history, assistantMsg)
			continue
		}

		userMsg := HistoryUserMessage{}
		userMsg.UserInputMessage.Content = content
		userMsg.UserInputMessage.ModelId = resolvedModel
		userMsg.UserInputMessage.Origin = "AI_EDITOR"

		// Extract tool results for history if they exist
		if results := extractToolResults(msg.Content); len(results) > 0 {
			userMsg.UserInputMessage.UserInputMessageContext.ToolResults = results
		}

		history = append(history, userMsg)
	}
	cwReq.ConversationState.History = history

	return cwReq
}

// resolvePort returns the port to use: flag > env > default.
// Returns an error if the value is not a valid port number.
func resolvePort(flagValue string) (string, error) {
	port := defaultPort
	if flagValue != "" {
		port = flagValue
	} else if envPort := os.Getenv("OPENKIRO_PORT"); envPort != "" {
		port = envPort
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("invalid port %q: must be 1-65535", port)
	}
	return port, nil
}

// logDir returns the platform-appropriate log directory for openkiro.
// Creates the directory if it does not exist.
func logDir() (string, error) {
	var dir string
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("logDir: %w", err)
		}
		dir = filepath.Join(home, "Library", "Logs", "openkiro")
	case "windows":
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			return "", fmt.Errorf("logDir: LOCALAPPDATA not set")
		}
		dir = filepath.Join(local, "openkiro", "logs")
	default: // linux and others
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("logDir: %w", err)
		}
		dir = filepath.Join(home, ".local", "state", "openkiro")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("logDir: mkdir %q: %w", dir, err)
	}
	return dir, nil
}

// pidFilePath returns the path to the PID file.
func pidFilePath() (string, error) {
	dir, err := logDir()
	if err != nil {
		return "", fmt.Errorf("pidFilePath: %w", err)
	}
	return filepath.Join(dir, "openkiro.pid"), nil
}

// logFilePath returns the path to the log file.
func logFilePath() (string, error) {
	dir, err := logDir()
	if err != nil {
		return "", fmt.Errorf("logFilePath: %w", err)
	}
	return filepath.Join(dir, "openkiro.log"), nil
}

func writePID(pid int) error {
	p, err := pidFilePath()
	if err != nil {
		return fmt.Errorf("writePID: %w", err)
	}
	if err := os.WriteFile(p, []byte(strconv.Itoa(pid)), 0o644); err != nil {
		return fmt.Errorf("writePID: %w", err)
	}
	return nil
}

func readPID() (int, error) {
	p, err := pidFilePath()
	if err != nil {
		return 0, fmt.Errorf("readPID: %w", err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return 0, fmt.Errorf("readPID: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("readPID: invalid pid: %w", err)
	}
	return pid, nil
}

func removePID() error {
	p, err := pidFilePath()
	if err != nil {
		return fmt.Errorf("removePID: %w", err)
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removePID: %w", err)
	}
	return nil
}

func isRunning(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

func cleanStalePID() error {
	pid, err := readPID()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("cleanStalePID: %w", err)
	}
	if isRunning(pid) {
		return fmt.Errorf("cleanStalePID: already running with PID %d", pid)
	}
	return removePID()
}

// selfPath returns the absolute path to the current binary.
func selfPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("selfPath: %w", err)
	}
	real, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("selfPath: %w", err)
	}
	return real, nil
}

// launchdPlistPath returns the path to the launchd plist file.
func launchdPlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("launchdPlistPath: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist"), nil
}

// generatePlist returns a launchd plist XML for the proxy.
func generatePlist(binaryPath, port, logPath string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>server</string>
		<string>--port</string>
		<string>%s</string>
	</array>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
	<key>KeepAlive</key>
	<true/>
	<key>RunAtLoad</key>
	<false/>
</dict>
</plist>
`, launchdLabel, binaryPath, port, logPath, logPath)
}

// parsePortFlag extracts --port value from os.Args[2:].
func parsePortFlag() string {
	for i := 2; i < len(os.Args); i++ {
		if os.Args[i] == "--port" && i+1 < len(os.Args) {
			return os.Args[i+1]
		}
	}
	return ""
}

func cmdStart() {
	if err := cleanStalePID(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	port, err := resolvePort(parsePortFlag())
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	bin, err := selfPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	logP, err := logFilePath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	switch runtime.GOOS {
	case "darwin":
		plistPath, err := launchdPlistPath()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		content := generatePlist(bin, port, logP)
		if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "mkdir LaunchAgents: %v\n", err)
			os.Exit(1)
		}
		if err := os.WriteFile(plistPath, []byte(content), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write plist: %v\n", err)
			os.Exit(1)
		}
		out, err := exec.Command("launchctl", "load", plistPath).CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "launchctl load: %s: %v\n", strings.TrimSpace(string(out)), err)
			os.Exit(1)
		}
		fmt.Printf("openkiro started (launchd) on port %s\n", port)

	case "linux":
		cmd := exec.Command(bin, "server", "--port", port)
		logFile, err := os.OpenFile(logP, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "open log: %v\n", err)
			os.Exit(1)
		}
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		if err := cmd.Start(); err != nil {
			logFile.Close()
			fmt.Fprintf(os.Stderr, "start: %v\n", err)
			os.Exit(1)
		}
		logFile.Close()
		if err := writePID(cmd.Process.Pid); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		fmt.Printf("openkiro started (PID %d) on port %s\n", cmd.Process.Pid, port)

	default:
		fmt.Fprintf(os.Stderr, "start: use 'openkiro server' directly on %s (or wait for Windows Service support)\n", runtime.GOOS)
		os.Exit(1)
	}
}

func cmdStop() {
	switch runtime.GOOS {
	case "darwin":
		plistPath, err := launchdPlistPath()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		out, err := exec.Command("launchctl", "unload", plistPath).CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "launchctl unload: %s: %v\n", strings.TrimSpace(string(out)), err)
		}
		_ = os.Remove(plistPath)
		_ = removePID()
		fmt.Println("openkiro stopped (launchd)")

	case "linux":
		pid, err := readPID()
		if err != nil {
			fmt.Fprintf(os.Stderr, "read PID: %v\n", err)
			os.Exit(1)
		}
		proc, err := os.FindProcess(pid)
		if err != nil {
			fmt.Fprintf(os.Stderr, "find process %d: %v\n", pid, err)
			os.Exit(1)
		}
		if err := proc.Signal(syscall.Signal(0xf)); err != nil { // SIGTERM
			fmt.Fprintf(os.Stderr, "signal %d: %v\n", pid, err)
		}
		_ = removePID()
		fmt.Printf("openkiro stopped (PID %d)\n", pid)

	default:
		fmt.Fprintf(os.Stderr, "stop: not supported on %s\n", runtime.GOOS)
		os.Exit(1)
	}
}

func cmdStatus() {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("launchctl", "list", launchdLabel).CombinedOutput()
		if err != nil {
			fmt.Println("openkiro: not running")
			return
		}
		// Parse PID from launchctl output (first column of second line)
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		pidStr := "-"
		if len(lines) > 1 {
			fields := strings.Fields(lines[1])
			if len(fields) > 0 && fields[0] != "-" {
				pidStr = fields[0]
			}
		}
		port := defaultPort
		if p := os.Getenv("OPENKIRO_PORT"); p != "" {
			port = p
		}
		fmt.Printf("openkiro: running (PID %s, port %s)\n", pidStr, port)

	case "linux":
		pid, err := readPID()
		if err != nil {
			fmt.Println("openkiro: not running")
			return
		}
		if !isRunning(pid) {
			_ = removePID()
			fmt.Println("openkiro: not running (stale PID removed)")
			return
		}
		port := defaultPort
		if p := os.Getenv("OPENKIRO_PORT"); p != "" {
			port = p
		}
		fmt.Printf("openkiro: running (PID %d, port %s)\n", pid, port)

	default:
		fmt.Fprintf(os.Stderr, "status: not supported on %s\n", runtime.GOOS)
		os.Exit(1)
	}
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage:")
		fmt.Println("  openkiro read    - Read and display token")
		fmt.Println("  openkiro refresh - Refresh token")
		fmt.Println("  openkiro export  - Export environment variables")
		fmt.Println("  openkiro claude  - Skip Claude region restrictions")
		fmt.Println("  openkiro server [port] - Start Anthropic API proxy server")
		fmt.Println("  openkiro start   - Start background proxy")
		fmt.Println("  openkiro stop    - Stop background proxy")
		fmt.Println("  openkiro status  - Show proxy status")
		fmt.Println("  https://github.com/ryolambert/openkiro")
		os.Exit(1)
	}

	command := os.Args[1]

	switch command {
	case "read":
		readToken()
	case "refresh":
		refreshToken()
	case "export":
		var flagPort string
		for i := 2; i < len(os.Args); i++ {
			if os.Args[i] == "--port" && i+1 < len(os.Args) {
				flagPort = os.Args[i+1]
				i++
			}
		}
		port, err := resolvePort(flagPort)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		exportEnvVars(port)

	case "claude":
		setClaude()
	case "server":
		var flagPort string
		listenAddr := defaultListenAddress
		for i := 2; i < len(os.Args); i++ {
			switch {
			case os.Args[i] == "--listen" && i+1 < len(os.Args):
				listenAddr = os.Args[i+1]
				i++
			case os.Args[i] == "--port" && i+1 < len(os.Args):
				flagPort = os.Args[i+1]
				i++
			case !strings.HasPrefix(os.Args[i], "--"):
				flagPort = os.Args[i] // backward compat: positional port
			}
		}
		port, err := resolvePort(flagPort)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		startServer(listenAddr, port)
	case "start":
		cmdStart()
	case "stop":
		cmdStop()
	case "status":
		cmdStatus()
	default:
		fmt.Printf("Unknown command: %s\n", command)
		os.Exit(1)
	}
}

// getTokenFilePath returns the cross-platform token file path
func getTokenFilePath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("Failed to get user home directory: %v\n", err)
		os.Exit(1)
	}

	return filepath.Join(homeDir, ".aws", "sso", "cache", "kiro-auth-token.json")
}

// readToken reads and prints token information
func readToken() {
	tokenPath := getTokenFilePath()

	data, err := os.ReadFile(tokenPath)
	if err != nil {
		fmt.Printf("Failed to read token file: %v\n", err)
		os.Exit(1)
	}

	var token TokenData
	if err := jsonStr.Unmarshal(data, &token); err != nil {
		fmt.Printf("Failed to parse token file: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Token Information:")
	fmt.Printf("Access Token: %s\n", token.AccessToken)
	fmt.Printf("Refresh Token: %s\n", token.RefreshToken)
	if token.ExpiresAt != "" {
		fmt.Printf("Expires at: %s\n", token.ExpiresAt)
	}
}

// getKiroDBPath returns the path to Kiro CLI's SQLite database
func getKiroDBPath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("Failed to get home directory: %v\n", err)
		os.Exit(1)
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(homeDir, "Library", "Application Support", "kiro-cli", "data.sqlite3")
	case "windows":
		configDir, err := os.UserConfigDir()
		if err != nil {
			fmt.Printf("Failed to get config directory: %v\n", err)
			os.Exit(1)
		}
		return filepath.Join(configDir, "kiro-cli", "data.sqlite3")
	default:
		return filepath.Join(homeDir, ".local", "share", "kiro-cli", "data.sqlite3")
	}
}

// refreshToken syncs the live token from Kiro CLI's SQLite database.
// The upstream /refreshToken HTTP endpoint is unreliable; Kiro CLI manages
// its own session in sqlite and the access token remains valid well beyond
// the expires_at field — so we just pull it directly from the DB.
func refreshToken() {
	dbPath := getKiroDBPath()

	if _, err := exec.LookPath("sqlite3"); err != nil {
		fmt.Fprintf(os.Stderr, "Error: 'sqlite3' not found on PATH.\n")
		fmt.Fprintf(os.Stderr, "The 'refresh' command requires the sqlite3 CLI to read Kiro's local database.\n")
		fmt.Fprintf(os.Stderr, "Install it:\n")
		fmt.Fprintf(os.Stderr, "  macOS:   brew install sqlite3\n")
		fmt.Fprintf(os.Stderr, "  Linux:   sudo apt install sqlite3  (or your distro's equivalent)\n")
		fmt.Fprintf(os.Stderr, "  Windows: winget install SQLite.SQLite  (or download from https://sqlite.org/download.html)\n")
		os.Exit(1)
	}

	out, err := exec.Command("sqlite3", dbPath,
		"SELECT value FROM auth_kv WHERE key='kirocli:odic:token';").Output()
	if err != nil {
		fmt.Printf("Failed to read Kiro token from database: %v\nRun 'kiro login' to authenticate.\n", err)
		os.Exit(1)
	}

	var sqliteToken struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresAt    string `json:"expires_at"`
	}
	if err := jsonStr.Unmarshal(bytes.TrimSpace(out), &sqliteToken); err != nil {
		fmt.Printf("Failed to parse Kiro token from database: %v\nRun 'kiro login' to authenticate.\n", err)
		os.Exit(1)
	}

	newToken := TokenData{
		AccessToken:  sqliteToken.AccessToken,
		RefreshToken: sqliteToken.RefreshToken,
		ExpiresAt:    sqliteToken.ExpiresAt,
	}

	tokenPath := getTokenFilePath()
	newData, err := jsonStr.MarshalIndent(newToken, "", "  ")
	if err != nil {
		fmt.Printf("Failed to serialize token: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(tokenPath, newData, 0600); err != nil {
		fmt.Printf("Failed to write token file: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Token synced from Kiro CLI successfully!")
	fmt.Printf("Access Token: %s\n", redactToken(newToken.AccessToken))
}

// getProfileArn returns the CodeWhisperer profileArn to use.
// - IAM Identity Center accounts: set KIRO_PROFILE_ARN env var
// - Builder ID (free tier): leave unset → returns ""
func getProfileArn() string {
	if v := os.Getenv("KIRO_PROFILE_ARN"); v != "" {
		return v
	}
	return "" // Builder ID free tier — no profileArn needed
}

// exportEnvVars exports environment variables
func exportEnvVars(port string) {
	tokenPath := getTokenFilePath()

	data, err := os.ReadFile(tokenPath)
	if err != nil {
		fmt.Printf("Failed to read token, please install Kiro and login first!: %v\n", err)
		os.Exit(1)
	}

	var token TokenData
	if err := jsonStr.Unmarshal(data, &token); err != nil {
		fmt.Printf("Failed to parse token file: %v\n", err)
		os.Exit(1)
	}

	baseURL := fmt.Sprintf("http://localhost:%s", port)

	// Output env var setup commands in OS-specific formats
	if runtime.GOOS == "windows" {
		fmt.Println("CMD")
		fmt.Printf("set ANTHROPIC_BASE_URL=%s\n", baseURL)
		fmt.Printf("set ANTHROPIC_API_KEY=%s\n\n", token.AccessToken)
		fmt.Println("Powershell")
		fmt.Printf("$env:ANTHROPIC_BASE_URL=\"%s\"\n", baseURL)
		fmt.Printf(`$env:ANTHROPIC_API_KEY="%s"`, token.AccessToken)
	} else {
		fmt.Printf("export ANTHROPIC_BASE_URL=%s\n", baseURL)
		fmt.Printf("export ANTHROPIC_API_KEY=\"%s\"\n", token.AccessToken)
	}
}

func legacyClaudeConfigKey() string {
	return strings.Join([]string{"kiro", "2cc"}, "")
}

func setClaude() {
	// C:\Users\WIN10\.claude.json
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("Failed to get user home directory: %v\n", err)
		os.Exit(1)
	}

	claudeJsonPath := filepath.Join(homeDir, ".claude.json")
	ok, _ := FileExists(claudeJsonPath)
	if !ok {
		fmt.Println("Claude configuration file not found, please check if Claude Code is installed")
		fmt.Println("npm install -g @anthropic-ai/claude-code")
		os.Exit(1)
	}

	data, err := os.ReadFile(claudeJsonPath)
	if err != nil {
		fmt.Printf("Failed to read Claude file: %v\n", err)
		os.Exit(1)
	}

	var jsonData map[string]interface{}

	err = jsonStr.Unmarshal(data, &jsonData)

	if err != nil {
		fmt.Printf("Failed to parse JSON file: %v\n", err)
		os.Exit(1)
	}

	jsonData["hasCompletedOnboarding"] = true
	jsonData["openkiro"] = true
	delete(jsonData, "kirolink")
	delete(jsonData, legacyClaudeConfigKey())

	newJson, err := json.MarshalIndent(jsonData, "", "  ")

	if err != nil {
		fmt.Printf("Failed to generate JSON file: %v\n", err)
		os.Exit(1)
	}

	err = os.WriteFile(claudeJsonPath, newJson, 0644)

	if err != nil {
		fmt.Printf("Failed to write JSON file: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Claude configuration file updated")

}

// getToken gets the current token, retrying once on parse failure (race with writer).
func getToken() (TokenData, error) {
	tokenPath := getTokenFilePath()

	readAndParse := func() (TokenData, error) {
		data, err := os.ReadFile(tokenPath)
		if err != nil {
			return TokenData{}, fmt.Errorf("reading token file: %w", err)
		}
		var token TokenData
		if err := jsonStr.Unmarshal(data, &token); err != nil {
			return TokenData{}, fmt.Errorf("parsing token file: %w", err)
		}
		return token, nil
	}

	token, err := readAndParse()
	if err == nil {
		return token, nil
	}

	// Retry once after 100ms — file may have been partially written
	debugLogf("token read failed, retrying in 100ms: %v", err)
	time.Sleep(100 * time.Millisecond)

	token, retryErr := readAndParse()
	if retryErr != nil {
		return TokenData{}, fmt.Errorf("token read failed after retry: %w", retryErr)
	}
	return token, nil
}

func debugLoggingEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OPENKIRO_DEBUG"))) {
	case "1", "true", "yes", "on", "debug":
		return true
	}
	// Legacy fallback
	switch strings.ToLower(strings.TrimSpace(os.Getenv("KIROLINK_DEBUG"))) {
	case "1", "true", "yes", "on", "debug":
		fmt.Fprintln(os.Stderr, "WARNING: KIROLINK_DEBUG is deprecated, use OPENKIRO_DEBUG")
		return true
	}
	return false
}

func debugLogf(format string, args ...any) {
	if debugLoggingEnabled() {
		log.Printf(format, args...)
	}
}

func redactToken(s string) string {
	if len(s) <= 12 {
		return "***"
	}
	return s[:8] + "..." + s[len(s)-4:]
}

func debugLogBodySummary(label string, body []byte) {
	if !debugLoggingEnabled() {
		return
	}
	sum := sha256.Sum256(body)
	debugLogf("%s size=%d sha256=%x", label, len(body), sum[:8])
}

var (
	upstreamClientOnce sync.Once
	upstreamClient     *http.Client
	// upstreamTransport is the RoundTripper used by the pooled client.
	// Tests may call resetUpstreamClient to swap this before the next init.
	upstreamTransport http.RoundTripper = &http.Transport{
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}
)

// resetUpstreamClient clears the pooled client so the next getUpstreamClient
// call re-initializes it with the current upstreamTransport. Test-only.
func resetUpstreamClient() {
	upstreamClientOnce = *new(sync.Once)
	upstreamClient = nil
}

func getUpstreamClient() *http.Client {
	upstreamClientOnce.Do(func() {
		upstreamClient = &http.Client{
			Timeout:   upstreamHTTPTimeout,
			Transport: upstreamTransport,
		}
	})
	return upstreamClient
}

func serverAddress(listenAddr, port string) string {
	return net.JoinHostPort(listenAddr, port)
}

func newHTTPServer(listenAddr, port string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              serverAddress(listenAddr, port),
		Handler:           handler,
		ReadTimeout:       serverReadTimeout,
		WriteTimeout:      serverWriteTimeout,
		IdleTimeout:       serverIdleTimeout,
		ReadHeaderTimeout: serverHeaderTimeout,
	}
}

func handlePanic(w http.ResponseWriter, recovered any) {
	if recovered == nil {
		return
	}
	log.Printf("panic in request handler: %v", recovered)
	http.Error(w, `{"error":{"type":"server_error","message":"Internal server error"}}`, http.StatusInternalServerError)
}

// logMiddleware logs all HTTP requests
func logMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		startTime := time.Now()

		// Call next handler
		next(w, r)

		// Measure processing duration
		duration := time.Since(startTime)
		log.Printf("%s %s completed in %v", r.Method, r.URL.Path, duration)
	}
}

func newProxyHandler() http.Handler {
	// Create router
	mux := http.NewServeMux()

	// Register all endpoints
	mux.HandleFunc("/v1/messages", logMiddleware(func(w http.ResponseWriter, r *http.Request) {
		// Only handle POST requests
		if r.Method != http.MethodPost {
			http.Error(w, "Only POST requests are supported", http.StatusMethodNotAllowed)
			return
		}

		// Get current token
		token, err := getToken()
		if err != nil {
			log.Printf("failed to get token: %v", err)
			http.Error(w, fmt.Sprintf("Failed to get token: %v", err), http.StatusInternalServerError)
			return
		}

		// Read request body
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		defer r.Body.Close()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				http.Error(w, fmt.Sprintf("Request body exceeds %d bytes", maxRequestBodyBytes), http.StatusRequestEntityTooLarge)
				return
			}
			log.Printf("failed to read request body: %v", err)
			http.Error(w, "Failed to read request body", http.StatusInternalServerError)
			return
		}
		debugLogBodySummary("anthropic request", body)

		// Parse Anthropic request
		var anthropicReq AnthropicRequest
		if err := jsonStr.Unmarshal(body, &anthropicReq); err != nil {
			log.Printf("failed to parse request body: %v", err)
			http.Error(w, fmt.Sprintf("Failed to parse request body: %v", err), http.StatusBadRequest)
			return
		}

		// Basic validation with explicit error messages
		if anthropicReq.Model == "" {
			http.Error(w, `{"message":"Missing required field: model"}`, http.StatusBadRequest)
			return
		}
		if len(anthropicReq.Messages) == 0 {
			http.Error(w, `{"message":"Missing required field: messages"}`, http.StatusBadRequest)
			return
		}
		resolvedModel := resolveModelID(anthropicReq.Model)
		if strings.TrimSpace(anthropicReq.Model) == "" {
			anthropicReq.Model = "default"
		}
		if _, ok := ModelMap[strings.ToLower(strings.TrimSpace(anthropicReq.Model))]; !ok {
			log.Printf("unknown model alias %q, using fallback %q", anthropicReq.Model, resolvedModel)
		}

		// Handle streaming request
		if anthropicReq.Stream {
			func() {
				defer func() {
					if recovered := recover(); recovered != nil {
						log.Printf("panic in streaming handler: %v", recovered)
					}
				}()
				handleStreamRequest(w, anthropicReq, token.AccessToken)
			}()
			return
		}

		// Handle non-streaming request
		func() {
			defer func() {
				handlePanic(w, recover())
			}()
			handleNonStreamRequest(w, anthropicReq, token.AccessToken)
		}()
	}))

	// Add models endpoint
	mux.HandleFunc("/v1/models", logMiddleware(func(w http.ResponseWriter, r *http.Request) {
		type ModelEntry struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			Created int64  `json:"created"`
			OwnedBy string `json:"owned_by"`
		}
		type ModelsResponse struct {
			Object string       `json:"object"`
			Data   []ModelEntry `json:"data"`
		}

		keys := make([]string, 0, len(ModelMap))
		for k := range ModelMap {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		data := make([]ModelEntry, 0, len(keys))
		for _, k := range keys {
			data = append(data, ModelEntry{
				ID:      k,
				Object:  "model",
				Created: 1686960000,
				OwnedBy: "anthropic",
			})
		}

		w.Header().Set("Content-Type", "application/json")
		jsonStr.NewEncoder(w).Encode(ModelsResponse{
			Object: "list",
			Data:   data,
		})
	}))

	// Add health check endpoint
	mux.HandleFunc("/health", logMiddleware(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))

	// Add 404 handler
	mux.HandleFunc("/", logMiddleware(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("unknown endpoint accessed: %s", r.URL.Path)
		http.Error(w, "404 Not Found", http.StatusNotFound)
	}))

	return mux
}

// startServer starts the HTTP proxy server
func startServer(listenAddr, port string) {
	protocol.Debug = debugLoggingEnabled()
	if listenAddr != defaultListenAddress {
		log.Printf("WARNING: listening on %s — server is accessible from the network", listenAddr)
	}
	server := newHTTPServer(listenAddr, port, newProxyHandler())

	log.Printf("Starting Anthropic API proxy server on %s", server.Addr)
	log.Printf("Available endpoints:")
	log.Printf("  POST /v1/messages - Anthropic API proxy")
	log.Printf("  GET  /v1/models   - List available models")
	log.Printf("  GET  /health      - Health check")
	log.Printf("Press Ctrl+C to stop the server")

	if err := server.ListenAndServe(); err != nil {
		log.Printf("Failed to start server: %v", err)
		os.Exit(1)
	}
}

type anthropicResponseBlock struct {
	Type      string
	Text      string
	ToolUseID string
	ToolName  string
	ToolInput map[string]any
	rawInput  string
}

type translatedAnthropicResponse struct {
	Blocks       []anthropicResponseBlock
	StopReason   string
	OutputTokens int
}

func responseModelID(cwReq CodeWhispererRequest, anthropicReq AnthropicRequest) string {
	modelID := strings.TrimSpace(cwReq.ConversationState.CurrentMessage.UserInputMessage.ModelId)
	if modelID != "" {
		return modelID
	}
	return anthropicReq.Model
}

func assembleAnthropicResponse(events []protocol.SSEEvent) translatedAnthropicResponse {
	type blockAccumulator struct {
		anthropicResponseBlock
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
					block.rawInput += partial
				case *string:
					if partial != nil {
						block.rawInput += *partial
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
	translated := translatedAnthropicResponse{StopReason: stopReason, OutputTokens: outputTokens}
	translated.Blocks = make([]anthropicResponseBlock, 0, len(order))

	for _, index := range order {
		block := blocks[index]
		if block == nil || block.Type == "" {
			continue
		}

		if block.Type == "tool_use" {
			if strings.TrimSpace(block.rawInput) != "" {
				toolInput := map[string]any{}
				if err := jsonStr.Unmarshal([]byte(block.rawInput), &toolInput); err != nil {
					log.Printf("tool input unmarshal error: %v", err)
				} else {
					block.ToolInput = toolInput
				}
			}
			if block.ToolInput == nil {
				block.ToolInput = map[string]any{}
			}
		}

		translated.Blocks = append(translated.Blocks, block.anthropicResponseBlock)
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

func buildAnthropicResponsePayload(conversationId, model string, inputTokens int, translated translatedAnthropicResponse) map[string]any {
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

func buildAnthropicStreamEvents(conversationId, messageId, model string, inputTokens int, translated translatedAnthropicResponse) []protocol.SSEEvent {
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
				if rawInput, err := jsonStr.Marshal(block.ToolInput); err == nil {
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

// handleStreamRequest handles streaming requests
func handleStreamRequest(w http.ResponseWriter, anthropicReq AnthropicRequest, accessToken string) {
	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
		return
	}
	rc := http.NewResponseController(w)
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("panic in streaming handler: %v", recovered)
			sendErrorEvent(w, flusher, "Internal server error", nil)
		}
	}()

	messageId := fmt.Sprintf("msg_%s", time.Now().Format("20060102150405"))

	// Build CodeWhisperer request
	cwReq := buildCodeWhispererRequest(anthropicReq)

	// Serialize with payload-size enforcement
	cwReqBody, err := ensurePayloadFits(&cwReq)
	if err != nil {
		sendErrorEvent(w, flusher, "Failed to serialize request", err)
		return
	}

	debugLogBodySummary("codewhisperer streaming request", cwReqBody)

	// Create streaming proxy request
	proxyReq, err := http.NewRequest(
		http.MethodPost,
		"https://codewhisperer.us-east-1.amazonaws.com/generateAssistantResponse",
		bytes.NewBuffer(cwReqBody),
	)
	if err != nil {
		sendErrorEvent(w, flusher, "Failed to create proxy request", err)
		return
	}

	// Set request headers
	proxyReq.Header.Set("Authorization", "Bearer "+accessToken)
	proxyReq.Header.Set("Content-Type", "application/json")
	proxyReq.Header.Set("Accept", "text/event-stream")

	// Send request with retry on "Improperly formed request"
	client := getUpstreamClient()

	var resp *http.Response
	const maxRetries = 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			// Rebuild the HTTP request with the (possibly trimmed) body
			proxyReq, err = http.NewRequest(
				http.MethodPost,
				"https://codewhisperer.us-east-1.amazonaws.com/generateAssistantResponse",
				bytes.NewBuffer(cwReqBody),
			)
			if err != nil {
				sendErrorEvent(w, flusher, "Failed to create retry request", err)
				return
			}
			proxyReq.Header.Set("Authorization", "Bearer "+accessToken)
			proxyReq.Header.Set("Content-Type", "application/json")
			proxyReq.Header.Set("Accept", "text/event-stream")
		}

		resp, err = client.Do(proxyReq)
		if err != nil {
			sendErrorEvent(w, flusher, "CodeWhisperer request error", fmt.Errorf("request error: %s", err.Error()))
			return
		}

		if resp.StatusCode == http.StatusOK {
			break // success
		}

		respBodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		respStr := string(respBodyBytes)
		debugLogBodySummary("codewhisperer streaming error response", respBodyBytes)
		log.Printf("CodeWhisperer streaming request failed with status %d", resp.StatusCode)

		if resp.StatusCode == 400 && strings.Contains(respStr, "Improperly formed request") && attempt < maxRetries-1 {
			log.Printf("CodeWhisperer streaming request improperly formed; retrying with trimmed payload (attempt %d)", attempt+1)
			// Aggressively trim retry payload while keeping the most recent caller history.
			cwReq.ConversationState.History = keepMostRecentHistory(cwReq.ConversationState.History, 2)
			// Strip tools to just name + minimal schema
			tools := cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools
			for i := range tools {
				tools[i].ToolSpecification.Description = truncateString(tools[i].ToolSpecification.Description, 80)
				tools[i].ToolSpecification.InputSchema = InputSchema{Json: map[string]any{"type": "object"}}
			}
			cwReqBody, err = jsonStr.Marshal(cwReq)
			if err != nil {
				sendErrorEvent(w, flusher, "Failed to serialize retry request", err)
				return
			}
			debugLogf("[retry] trimmed payload size: %d bytes", len(cwReqBody))
			continue
		}

		// 403 = token expired — sync from Kiro CLI sqlite and retry once
		if resp.StatusCode == 403 && attempt < maxRetries-1 {
			log.Printf("Token expired (403), syncing from Kiro CLI database...")
			refreshToken()
			newToken, tokenErr := getToken()
			if tokenErr != nil {
				sendErrorEvent(w, flusher, "error", fmt.Errorf("Token sync failed: %s", tokenErr.Error()))
				return
			}
			accessToken = newToken.AccessToken
			log.Printf("Token synced, retrying request...")
			continue
		}
		sendErrorEvent(w, flusher, "error", fmt.Errorf("CodeWhisperer Error: %s", respStr))
		return
	}
	defer resp.Body.Close()

	// Send message_start + ping immediately so the client sees activity
	model := responseModelID(cwReq, anthropicReq)
	sendSSEEvent(w, flusher, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":              messageId,
			"type":            "message",
			"role":            "assistant",
			"content":         []any{},
			"model":           model,
			"stop_reason":     nil,
			"stop_sequence":   nil,
			"conversation_id": cwReq.ConversationState.ConversationId,
			"usage": map[string]any{
				"input_tokens":  len(cwReq.ConversationState.CurrentMessage.UserInputMessage.Content),
				"output_tokens": 1,
			},
		},
	})
	sendSSEEvent(w, flusher, "ping", map[string]string{"type": "ping"})

	// Stream events incrementally — each upstream frame is parsed and forwarded
	// to the client as it arrives, instead of buffering the entire response.
	err = protocol.ParseEventStream(resp.Body, func(evt protocol.SSEEvent) error {
		_ = rc.SetWriteDeadline(time.Now().Add(serverWriteTimeout))
		sendSSEEvent(w, flusher, evt.Event, evt.Data)
		return nil
	})
	if err != nil {
		sendErrorEvent(w, flusher, "Stream processing error", err)
		return
	}

	_ = rc.SetWriteDeadline(time.Now().Add(serverWriteTimeout))
	sendSSEEvent(w, flusher, "message_stop", map[string]any{"type": "message_stop"})

}

// handleNonStreamRequest handles non-streaming requests
func handleNonStreamRequest(w http.ResponseWriter, anthropicReq AnthropicRequest, accessToken string) {
	// Build CodeWhisperer request
	cwReq := buildCodeWhispererRequest(anthropicReq)

	// Serialize with payload-size enforcement
	cwReqBody, err := ensurePayloadFits(&cwReq)
	if err != nil {
		log.Printf("Failed to serialize request: %v", err)
		http.Error(w, fmt.Sprintf("Failed to serialize request: %v", err), http.StatusInternalServerError)
		return
	}

	debugLogBodySummary("codewhisperer request", cwReqBody)

	// Create proxy request
	proxyReq, err := http.NewRequest(
		http.MethodPost,
		"https://codewhisperer.us-east-1.amazonaws.com/generateAssistantResponse",
		bytes.NewBuffer(cwReqBody),
	)
	if err != nil {
		log.Printf("Failed to create proxy request: %v", err)
		http.Error(w, fmt.Sprintf("Failed to create proxy request: %v", err), http.StatusInternalServerError)
		return
	}

	// Set request headers
	proxyReq.Header.Set("Authorization", "Bearer "+accessToken)
	proxyReq.Header.Set("Content-Type", "application/json")

	// Send request
	client := getUpstreamClient()

	resp, err := client.Do(proxyReq)
	if err != nil {
		log.Printf("Failed to send request: %v", err)
		http.Error(w, fmt.Sprintf("Failed to send request: %v", err), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// Read response
	cwRespBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Failed to read response: %v", err)
		http.Error(w, fmt.Sprintf("Failed to read response: %v", err), http.StatusInternalServerError)
		return
	}

	debugLogBodySummary("codewhisperer response", cwRespBody)

	respBodyStr := string(cwRespBody)

	translated := assembleAnthropicResponse(protocol.ParseEvents(cwRespBody))

	// Check if response is an error
	if strings.Contains(string(cwRespBody), "Improperly formed request.") {
		log.Printf("CodeWhisperer returned incorrect format")
		http.Error(w, fmt.Sprintf("Request format error: %s", respBodyStr), http.StatusBadRequest)
		return
	}

	// Build Anthropic response
	anthropicResp := buildAnthropicResponsePayload(
		cwReq.ConversationState.ConversationId,
		responseModelID(cwReq, anthropicReq),
		len(cwReq.ConversationState.CurrentMessage.UserInputMessage.Content),
		translated,
	)

	// Send response
	w.Header().Set("Content-Type", "application/json")
	jsonStr.NewEncoder(w).Encode(anthropicResp)
}

// sendSSEEvent sends an SSE event
func sendSSEEvent(w http.ResponseWriter, flusher http.Flusher, eventType string, data any) {

	json, err := jsonStr.Marshal(data)
	if err != nil {
		return
	}

	debugLogf("sse event=%s payload_size=%d", eventType, len(json))

	fmt.Fprintf(w, "event: %s\n", eventType)
	fmt.Fprintf(w, "data: %s\n\n", string(json))
	flusher.Flush()

}

// sendErrorEvent sends an error event
func sendErrorEvent(w http.ResponseWriter, flusher http.Flusher, message string, err error) {
	errorResp := map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    "overloaded_error",
			"message": message,
		},
	}

	// data: {"type": "error", "error": {"type": "overloaded_error", "message": "Overloaded"}}

	sendSSEEvent(w, flusher, "error", errorResp)
}

func FileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil // File or directory exists
	}
	if os.IsNotExist(err) {
		return false, nil // File or directory does not exist
	}
	return false, err // Other error
}
