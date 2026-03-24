package proxy

import "time"

// AnthropicTool defines the Anthropic API tool structure.
type AnthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// InputSchema defines the tool input schema structure.
type InputSchema struct {
	Json map[string]any `json:"json"`
}

// ToolSpecification defines the tool specification structure.
type ToolSpecification struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema InputSchema `json:"inputSchema"`
}

// CodeWhispererTool defines the CodeWhisperer API tool structure.
type CodeWhispererTool struct {
	ToolSpecification ToolSpecification `json:"toolSpecification"`
}

// HistoryUserMessage defines a user message in history.
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

// HistoryAssistantMessage defines an assistant message in history.
type HistoryAssistantMessage struct {
	AssistantResponseMessage struct {
		Content  string `json:"content"`
		ToolUses []any  `json:"toolUses"`
	} `json:"assistantResponseMessage"`
}

// AnthropicRequest defines the Anthropic API request structure.
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

// AnthropicRequestMessage defines the Anthropic API message structure.
type AnthropicRequestMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // Can be string or []ContentBlock
}

// AnthropicSystemMessage defines a system message block.
type AnthropicSystemMessage struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ContentBlock defines the message content block structure.
type ContentBlock struct {
	Type      string  `json:"type"`
	Text      *string `json:"text,omitempty"`
	ToolUseId *string `json:"tool_use_id,omitempty"`
	Content   *string `json:"content,omitempty"`
	Name      *string `json:"name,omitempty"`
	Input     *any    `json:"input,omitempty"`
}

// CodeWhispererRequest defines the CodeWhisperer API request structure.
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

// AnthropicResponseBlock holds a translated response content block.
type AnthropicResponseBlock struct {
	Type      string
	Text      string
	ToolUseID string
	ToolName  string
	ToolInput map[string]any
	RawInput  string
}

// TranslatedAnthropicResponse holds the assembled response.
type TranslatedAnthropicResponse struct {
	Blocks       []AnthropicResponseBlock
	StopReason   string
	OutputTokens int
}

const (
	ModelSonnet46 = "CLAUDE_SONNET_4_6_V1_0"
	ModelSonnet45 = "CLAUDE_SONNET_4_5_20250929_V1_0"
	ModelOpus46   = "CLAUDE_OPUS_4_6_V1_0"
	ModelHaiku45  = "CLAUDE_HAIKU_4_5_20251001_V1_0"

	// Builder ID free tier models
	ModelBuilderSonnet45 = "claude-sonnet-4.5"
	ModelBuilderHaiku45  = "claude-haiku-4.5"
	ModelBuilderSonnet35 = "CLAUDE_3_5_SONNET_20241022_V2_0"

	// IAM Identity Center profile ARN (paid/enterprise accounts)
	ProfileArnIAM = "arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK"

	MaxToolDescLen       = 200
	ServerReadTimeout    = 30 * time.Second
	ServerWriteTimeout   = 60 * time.Second
	ServerIdleTimeout    = 120 * time.Second
	ServerHeaderTimeout  = 10 * time.Second
	DefaultListenAddress = "127.0.0.1"
	DefaultPort          = "1234"
	LaunchdLabel         = "com.openkiro.proxy"
)

// MaxRequestBodyBytes is the max inbound request body size (200 MiB).
var MaxRequestBodyBytes int64 = 200 << 20

// MaxPayloadBytes is the soft limit for total request JSON (~250MB).
var MaxPayloadBytes = 250000000

// ModelMap maps Anthropic model aliases to CodeWhisperer model IDs.
var ModelMap = map[string]string{
	"default":                    ModelSonnet45,
	"claude-sonnet-4-6":          ModelSonnet46,
	"claude-sonnet-4-5":          ModelSonnet45,
	"claude-sonnet-4-5-20250929": ModelSonnet45,
	"claude-sonnet-4-20250514":   ModelSonnet46,
	"claude-opus-4-6":            ModelOpus46,
	"claude-haiku-4-5-20251001":  ModelHaiku45,
	"claude-3-5-sonnet-20241022": ModelSonnet46,
	"claude-3-5-haiku-20241022":  ModelHaiku45,
	"claude-3-7-sonnet-20250219": ModelSonnet46,
	"claude-3-7-haiku-20250219":  ModelHaiku45,
	"claude-4-sonnet":            ModelSonnet46,
	"claude-4-haiku":             ModelHaiku45,
	"claude-4-opus":              ModelOpus46,
}
