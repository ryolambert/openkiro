package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ryolambert/openkiro/internal/protocol"
	"github.com/ryolambert/openkiro/internal/token"
)

// ServerAddress returns the listen address string.
func ServerAddress(listenAddr, port string) string {
	return net.JoinHostPort(listenAddr, port)
}

// NewHTTPServer creates a configured HTTP server.
func NewHTTPServer(listenAddr, port string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              ServerAddress(listenAddr, port),
		Handler:           handler,
		ReadTimeout:       ServerReadTimeout,
		WriteTimeout:      ServerWriteTimeout,
		IdleTimeout:       ServerIdleTimeout,
		ReadHeaderTimeout: ServerHeaderTimeout,
	}
}

// HandlePanic handles a recovered panic in a request handler.
func HandlePanic(w http.ResponseWriter, recovered any) {
	if recovered == nil {
		return
	}
	log.Printf("panic in request handler: %v", recovered)
	http.Error(w, `{"error":{"type":"server_error","message":"Internal server error"}}`, http.StatusInternalServerError)
}

func logMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		startTime := time.Now()
		next(w, r)
		duration := time.Since(startTime)
		log.Printf("%s %s completed in %v", r.Method, r.URL.Path, duration)
	}
}

// NewProxyHandler creates the HTTP handler for the proxy.
func NewProxyHandler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/v1/messages", logMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Only POST requests are supported", http.StatusMethodNotAllowed)
			return
		}

		tok, err := token.GetToken()
		if err != nil {
			log.Printf("failed to get token: %v", err)
			http.Error(w, fmt.Sprintf("Failed to get token: %v", err), http.StatusInternalServerError)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, MaxRequestBodyBytes)
		defer r.Body.Close()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				http.Error(w, fmt.Sprintf("Request body exceeds %d bytes", MaxRequestBodyBytes), http.StatusRequestEntityTooLarge)
				return
			}
			log.Printf("failed to read request body: %v", err)
			http.Error(w, "Failed to read request body", http.StatusInternalServerError)
			return
		}
		token.DebugLogBodySummary("anthropic request", body)

		var anthropicReq AnthropicRequest
		if err := json.Unmarshal(body, &anthropicReq); err != nil {
			log.Printf("failed to parse request body: %v", err)
			http.Error(w, fmt.Sprintf("Failed to parse request body: %v", err), http.StatusBadRequest)
			return
		}

		if anthropicReq.Model == "" {
			http.Error(w, `{"message":"Missing required field: model"}`, http.StatusBadRequest)
			return
		}
		if len(anthropicReq.Messages) == 0 {
			http.Error(w, `{"message":"Missing required field: messages"}`, http.StatusBadRequest)
			return
		}
		resolvedModel := ResolveModelID(anthropicReq.Model)
		if strings.TrimSpace(anthropicReq.Model) == "" {
			anthropicReq.Model = "default"
		}
		if _, ok := ModelMap[strings.ToLower(strings.TrimSpace(anthropicReq.Model))]; !ok {
			log.Printf("unknown model alias %q, using fallback %q", anthropicReq.Model, resolvedModel)
		}

		if anthropicReq.Stream {
			func() {
				defer func() {
					if recovered := recover(); recovered != nil {
						log.Printf("panic in streaming handler: %v", recovered)
					}
				}()
				HandleStreamRequest(w, anthropicReq, tok.AccessToken)
			}()
			return
		}

		func() {
			defer func() {
				HandlePanic(w, recover())
			}()
			HandleNonStreamRequest(w, anthropicReq, tok.AccessToken)
		}()
	}))

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
		json.NewEncoder(w).Encode(ModelsResponse{
			Object: "list",
			Data:   data,
		})
	}))

	mux.HandleFunc("/health", logMiddleware(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))

	mux.HandleFunc("/", logMiddleware(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("unknown endpoint accessed: %s", r.URL.Path)
		http.Error(w, "404 Not Found", http.StatusNotFound)
	}))

	return mux
}

// StartServer starts the HTTP proxy server.
func StartServer(listenAddr, port string) {
	protocol.Debug = token.DebugLoggingEnabled()
	if listenAddr != DefaultListenAddress {
		log.Printf("WARNING: listening on %s — server is accessible from the network", listenAddr)
	}
	server := NewHTTPServer(listenAddr, port, NewProxyHandler())

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

// HandleStreamRequest handles streaming requests.
func HandleStreamRequest(w http.ResponseWriter, anthropicReq AnthropicRequest, accessToken string) {
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

	cwReq := BuildCodeWhispererRequest(anthropicReq)

	cwReqBody, err := EnsurePayloadFits(&cwReq)
	if err != nil {
		sendErrorEvent(w, flusher, "Failed to serialize request", err)
		return
	}

	token.DebugLogBodySummary("codewhisperer streaming request", cwReqBody)

	proxyReq, err := http.NewRequest(
		http.MethodPost,
		"https://codewhisperer.us-east-1.amazonaws.com/generateAssistantResponse",
		bytes.NewBuffer(cwReqBody),
	)
	if err != nil {
		sendErrorEvent(w, flusher, "Failed to create proxy request", err)
		return
	}

	proxyReq.Header.Set("Authorization", "Bearer "+accessToken)
	proxyReq.Header.Set("Content-Type", "application/json")
	proxyReq.Header.Set("Accept", "text/event-stream")

	client := token.GetUpstreamClient()

	var resp *http.Response
	const maxRetries = 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
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
			break
		}

		respBodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		respStr := string(respBodyBytes)
		token.DebugLogBodySummary("codewhisperer streaming error response", respBodyBytes)
		log.Printf("CodeWhisperer streaming request failed with status %d", resp.StatusCode)

		if resp.StatusCode == 400 && strings.Contains(respStr, "Improperly formed request") && attempt < maxRetries-1 {
			log.Printf("CodeWhisperer streaming request improperly formed; retrying with trimmed payload (attempt %d)", attempt+1)
			cwReq.ConversationState.History = KeepMostRecentHistory(cwReq.ConversationState.History, 2)
			tools := cwReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools
			for i := range tools {
				tools[i].ToolSpecification.Description = TruncateString(tools[i].ToolSpecification.Description, 80)
				tools[i].ToolSpecification.InputSchema = InputSchema{Json: map[string]any{"type": "object"}}
			}
			cwReqBody, err = json.Marshal(cwReq)
			if err != nil {
				sendErrorEvent(w, flusher, "Failed to serialize retry request", err)
				return
			}
			token.DebugLogf("[retry] trimmed payload size: %d bytes", len(cwReqBody))
			continue
		}

		if resp.StatusCode == 403 && attempt < maxRetries-1 {
			log.Printf("Token expired (403), syncing from Kiro CLI database...")
			token.RefreshToken()
			newToken, tokenErr := token.GetToken()
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

	model := ResponseModelID(cwReq, anthropicReq)
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

	err = protocol.ParseEventStream(resp.Body, func(evt protocol.SSEEvent) error {
		_ = rc.SetWriteDeadline(time.Now().Add(ServerWriteTimeout))
		sendSSEEvent(w, flusher, evt.Event, evt.Data)
		return nil
	})
	if err != nil {
		sendErrorEvent(w, flusher, "Stream processing error", err)
		return
	}

	_ = rc.SetWriteDeadline(time.Now().Add(ServerWriteTimeout))
	sendSSEEvent(w, flusher, "message_stop", map[string]any{"type": "message_stop"})
}

// HandleNonStreamRequest handles non-streaming requests.
func HandleNonStreamRequest(w http.ResponseWriter, anthropicReq AnthropicRequest, accessToken string) {
	cwReq := BuildCodeWhispererRequest(anthropicReq)

	cwReqBody, err := EnsurePayloadFits(&cwReq)
	if err != nil {
		log.Printf("Failed to serialize request: %v", err)
		http.Error(w, fmt.Sprintf("Failed to serialize request: %v", err), http.StatusInternalServerError)
		return
	}

	token.DebugLogBodySummary("codewhisperer request", cwReqBody)

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

	proxyReq.Header.Set("Authorization", "Bearer "+accessToken)
	proxyReq.Header.Set("Content-Type", "application/json")

	client := token.GetUpstreamClient()

	resp, err := client.Do(proxyReq)
	if err != nil {
		log.Printf("Failed to send request: %v", err)
		http.Error(w, fmt.Sprintf("Failed to send request: %v", err), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	cwRespBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Failed to read response: %v", err)
		http.Error(w, fmt.Sprintf("Failed to read response: %v", err), http.StatusInternalServerError)
		return
	}

	token.DebugLogBodySummary("codewhisperer response", cwRespBody)

	respBodyStr := string(cwRespBody)

	translated := AssembleAnthropicResponse(protocol.ParseEvents(cwRespBody))

	if strings.Contains(string(cwRespBody), "Improperly formed request.") {
		log.Printf("CodeWhisperer returned incorrect format")
		http.Error(w, fmt.Sprintf("Request format error: %s", respBodyStr), http.StatusBadRequest)
		return
	}

	anthropicResp := BuildAnthropicResponsePayload(
		cwReq.ConversationState.ConversationId,
		ResponseModelID(cwReq, anthropicReq),
		len(cwReq.ConversationState.CurrentMessage.UserInputMessage.Content),
		translated,
	)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(anthropicResp)
}

func sendSSEEvent(w http.ResponseWriter, flusher http.Flusher, eventType string, data any) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}

	token.DebugLogf("sse event=%s payload_size=%d", eventType, len(jsonData))

	fmt.Fprintf(w, "event: %s\n", eventType)
	fmt.Fprintf(w, "data: %s\n\n", string(jsonData))
	flusher.Flush()
}

func sendErrorEvent(w http.ResponseWriter, flusher http.Flusher, message string, err error) {
	errorResp := map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    "overloaded_error",
			"message": message,
		},
	}

	sendSSEEvent(w, flusher, "error", errorResp)
}
