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
		ChatTriggerType     string `json:"chatTriggerType"`
		ConversationId      string `json:"conversationId"`
		AgentContinuationId string `json:"agentContinuationId,omitempty"`
		AgentTaskType       string `json:"agentTaskType,omitempty"`
		CurrentMessage      struct {
			UserInputMessage struct {
				Content                 string `json:"content"`
				ModelId                 string `json:"modelId"`
				Origin                  string `json:"origin"`
				UserInputMessageContext struct {
					EnvState *EnvState `json:"envState,omitempty"`
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

// EnvState describes the client environment. Sent in userInputMessageContext.
// kiro-cli always populates operatingSystem and currentWorkingDirectory.
type EnvState struct {
	OperatingSystem         string `json:"operatingSystem,omitempty"`
	CurrentWorkingDirectory string `json:"currentWorkingDirectory,omitempty"`
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
	// Model IDs in the dot-notation kiro-cli uses. The Amazon Q runtime
	// validates these strictly; underscores or kebab-case are rejected.
	ModelSonnet46 = "claude-sonnet-4.6"
	ModelSonnet45 = "claude-sonnet-4.5"
	ModelOpus46   = "claude-opus-4.6"
	ModelHaiku45  = "claude-haiku-4.5"

	// Builder ID free tier models
	ModelBuilderSonnet45 = "claude-sonnet-4.5"
	ModelBuilderHaiku45  = "claude-haiku-4.5"
	ModelBuilderSonnet35 = "CLAUDE_3_5_SONNET_20241022_V2_0"

	// IAM Identity Center profile ARN (paid/enterprise accounts).
	// Used as a fallback when ListAvailableProfiles fails or no env var is set.
	ProfileArnIAM = "arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK"

	// CodeWhispererRuntimeURL is the Amazon Q endpoint for both control-plane
	// (ListAvailableProfiles) and streaming (GenerateAssistantResponse) calls.
	// Differentiated by the X-Amz-Target header.
	CodeWhispererRuntimeURL = "https://q.us-east-1.amazonaws.com/"

	// Coral RPC headers for the streaming runtime.
	CoralContentType             = "application/x-amz-json-1.0"
	CoralTargetGenerateAssistant = "AmazonCodeWhispererStreamingService.GenerateAssistantResponse"
	CoralTargetListProfiles      = "AmazonCodeWhispererService.ListAvailableProfiles"

	// KiroUserAgent identifies openkiro to the Amazon Q runtime as a kiro-cli
	// compatible client. The "app/AmazonQ-For-CLI" suffix is what AWS uses for
	// entitlement decisions — without it, requests get AccessDeniedException.
	KiroUserAgent = "aws-sdk-rust/1.3.16 ua/2.1 api/codewhispererstreaming/0.1.16551 os/macos lang/rust/1.92.0 exec-env/AmazonQ-For-CLI Version/2.4.2 md/appVersion-2.4.2 app/AmazonQ-For-CLI"

	// Origin sent in every UserInputMessage. KIRO_CLI is the value AWS expects
	// for the Q-For-CLI client tier.
	KiroOrigin = "KIRO_CLI"

	// AgentTaskType used for chat-style conversations (vs "spec" for spec-mode).
	AgentTaskTypeVibe = "vibe"

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
