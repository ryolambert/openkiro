package proxy

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ryolambert/openkiro/internal/token"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func withTestTransport(t *testing.T, fn roundTripFunc) {
	t.Helper()
	oldTransport := http.DefaultTransport
	oldUpstreamTransport := token.UpstreamTransport
	http.DefaultTransport = fn
	token.UpstreamTransport = fn
	token.ResetUpstreamClient()
	t.Cleanup(func() {
		http.DefaultTransport = oldTransport
		token.UpstreamTransport = oldUpstreamTransport
		token.ResetUpstreamClient()
	})
}

func encodeAssistantFrame(t *testing.T, payload any) []byte {
	t.Helper()

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	var frame bytes.Buffer
	totalLen := uint32(len(data) + 12)
	if err := binary.Write(&frame, binary.BigEndian, totalLen); err != nil {
		t.Fatalf("write totalLen: %v", err)
	}
	if err := binary.Write(&frame, binary.BigEndian, uint32(0)); err != nil {
		t.Fatalf("write headerLen: %v", err)
	}
	frame.Write(data)
	frame.Write([]byte{0, 0, 0, 0})
	return frame.Bytes()
}

func parseSSEOutput(t *testing.T, body string) []map[string]any {
	t.Helper()

	chunks := strings.Split(strings.TrimSpace(body), "\n\n")
	events := make([]map[string]any, 0, len(chunks))
	for _, chunk := range chunks {
		if strings.TrimSpace(chunk) == "" {
			continue
		}
		lines := strings.Split(chunk, "\n")
		if len(lines) < 2 {
			t.Fatalf("unexpected SSE chunk: %q", chunk)
		}
		eventType := strings.TrimPrefix(lines[0], "event: ")
		dataLine := strings.TrimPrefix(lines[1], "data: ")
		var data map[string]any
		if err := json.Unmarshal([]byte(dataLine), &data); err != nil {
			t.Fatalf("unmarshal SSE data %q: %v", dataLine, err)
		}
		events = append(events, map[string]any{"event": eventType, "data": data})
	}
	return events
}

func TestNewHTTPServerUsesLocalhostOnlyAndTimeouts(t *testing.T) {
	server := NewHTTPServer(DefaultListenAddress, "1234", http.NewServeMux())

	if got := server.Addr; got != "127.0.0.1:1234" {
		t.Fatalf("expected loopback-only listen address, got %q", got)
	}
	if got := server.ReadTimeout; got != ServerReadTimeout {
		t.Fatalf("expected ReadTimeout %v, got %v", ServerReadTimeout, got)
	}
	if got := server.WriteTimeout; got != ServerWriteTimeout {
		t.Fatalf("expected WriteTimeout %v, got %v", ServerWriteTimeout, got)
	}
	if got := server.IdleTimeout; got != ServerIdleTimeout {
		t.Fatalf("expected IdleTimeout %v, got %v", ServerIdleTimeout, got)
	}
	if got := server.ReadHeaderTimeout; got != ServerHeaderTimeout {
		t.Fatalf("expected ReadHeaderTimeout %v, got %v", ServerHeaderTimeout, got)
	}
}

func TestNewHTTPServerCustomListenAddress(t *testing.T) {
	server := NewHTTPServer("0.0.0.0", "5678", http.NewServeMux())
	if got := server.Addr; got != "0.0.0.0:5678" {
		t.Fatalf("expected custom listen address, got %q", got)
	}
}

func TestNewProxyHandlerRejectsOversizedRequestBody(t *testing.T) {
	orig := MaxRequestBodyBytes
	MaxRequestBodyBytes = 1 << 10 // 1KB for test
	t.Cleanup(func() { MaxRequestBodyBytes = orig })

	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	t.Setenv("USERPROFILE", tempHome)
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")

	tokenDir := filepath.Join(tempHome, ".aws", "sso", "cache")
	if err := os.MkdirAll(tokenDir, 0o755); err != nil {
		t.Fatalf("mkdir token dir: %v", err)
	}
	tokenFile := filepath.Join(tokenDir, "kiro-auth-token.json")
	if err := os.WriteFile(tokenFile, []byte(`{"accessToken":"token","refreshToken":"refresh"}`), 0o644); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	payload := `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"` +
		strings.Repeat("a", int(MaxRequestBodyBytes)) + `"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(payload))
	recorder := httptest.NewRecorder()

	NewProxyHandler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected status 413 for oversized request, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "Request body exceeds") {
		t.Fatalf("expected oversized body message, got %q", recorder.Body.String())
	}
}

func TestHandlePanicHidesRecoveredValue(t *testing.T) {
	recorder := httptest.NewRecorder()

	HandlePanic(recorder, "secret panic details")

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 status, got %d", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "secret panic details") {
		t.Fatalf("expected panic response to hide recovered value, got %q", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "Internal server error") {
		t.Fatalf("expected generic panic message, got %q", recorder.Body.String())
	}
}

func TestModelsEndpointDeterministic(t *testing.T) {
	mux := NewProxyHandler()
	type ModelsResponse struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}

	var firstIDs []string
	for i := 0; i < 10; i++ {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/models", nil))
		if rec.Code != 200 {
			t.Fatalf("iteration %d: status=%d", i, rec.Code)
		}
		var resp ModelsResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("iteration %d: decode: %v", i, err)
		}
		ids := make([]string, len(resp.Data))
		for j, m := range resp.Data {
			ids[j] = m.ID
		}
		if i == 0 {
			firstIDs = ids
			if !sort.StringsAreSorted(ids) {
				t.Fatalf("model IDs not sorted: %v", ids)
			}
		} else if len(ids) != len(firstIDs) {
			t.Fatalf("iteration %d: got %d models, want %d", i, len(ids), len(firstIDs))
		} else {
			for j := range ids {
				if ids[j] != firstIDs[j] {
					t.Fatalf("iteration %d: order differs at index %d: got %q, want %q", i, j, ids[j], firstIDs[j])
				}
			}
		}
	}
}

func TestHandleStreamRequestCharacterizationTextOnly(t *testing.T) {
	body := encodeAssistantFrame(t, map[string]any{"content": "hello from stream"})
	withTestTransport(t, func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(body)),
		}, nil
	})

	recorder := httptest.NewRecorder()
	HandleStreamRequest(recorder, AnthropicRequest{
		Model:    "mystery-model",
		Messages: []AnthropicRequestMessage{{Role: "user", Content: "hello"}},
		Stream:   true,
	}, "token")

	events := parseSSEOutput(t, recorder.Body.String())
	if len(events) != 7 {
		t.Fatalf("expected 7 SSE events, got %d: %s", len(events), recorder.Body.String())
	}

	gotOrder := []string{}
	for _, event := range events {
		gotOrder = append(gotOrder, event["event"].(string))
	}
	wantOrder := []string{"message_start", "ping", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}
	if strings.Join(gotOrder, ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("unexpected SSE order %v, want %v", gotOrder, wantOrder)
	}

	message := events[0]["data"].(map[string]any)["message"].(map[string]any)
	if got := message["model"]; got != ModelBuilderSonnet45 {
		t.Fatalf("expected streaming response to report resolved model, got %#v", got)
	}
	messageDelta := events[5]["data"].(map[string]any)["delta"].(map[string]any)
	if got := messageDelta["stop_reason"]; got != "end_turn" {
		t.Fatalf("expected end_turn stop_reason, got %#v", got)
	}
}

func TestHandleNonStreamRequestCharacterizationMixedTextToolKeepsBothBlocks(t *testing.T) {
	toolInput := `{"query":"drift"}`
	body := bytes.Join([][]byte{
		encodeAssistantFrame(t, map[string]any{"content": "hello before tool"}),
		encodeAssistantFrame(t, map[string]any{"toolUseId": "tool-1", "name": "search"}),
		encodeAssistantFrame(t, map[string]any{"toolUseId": "tool-1", "name": "search", "input": toolInput}),
		encodeAssistantFrame(t, map[string]any{"toolUseId": "tool-1", "name": "search", "stop": true}),
	}, nil)

	withTestTransport(t, func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(body)),
		}, nil
	})

	recorder := httptest.NewRecorder()
	HandleNonStreamRequest(recorder, AnthropicRequest{
		Model:    "mystery-model",
		Messages: []AnthropicRequestMessage{{Role: "user", Content: "hello"}},
	}, "token")

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode non-stream response: %v", err)
	}

	if got := resp["model"]; got != ModelBuilderSonnet45 {
		t.Fatalf("expected non-stream response to report resolved model, got %#v", got)
	}
	if got := resp["stop_reason"]; got != "tool_use" {
		t.Fatalf("expected mixed text/tool response to stop for tool_use, got %#v", got)
	}

	content := resp["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("expected text and tool_use blocks after mixed text/tool response, got %#v", content)
	}
	textBlock := content[0].(map[string]any)
	if got := textBlock["type"]; got != "text" {
		t.Fatalf("expected first block to be text, got %#v", got)
	}
	if got := textBlock["text"]; got != "hello before tool" {
		t.Fatalf("expected text block content to be preserved, got %#v", got)
	}
	block := content[1].(map[string]any)
	if got := block["type"]; got != "tool_use" {
		t.Fatalf("expected second block to be tool_use, got %#v", got)
	}
	input := block["input"].(map[string]any)
	if got := input["query"]; got != "drift" {
		t.Fatalf("expected parsed tool input query, got %#v", got)
	}
}
