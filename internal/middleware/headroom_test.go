package middleware_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ryolambert/openkiro/internal/headroom"
	"github.com/ryolambert/openkiro/internal/middleware"
	"github.com/ryolambert/openkiro/internal/proxy"
)

// fakeHeadroomServer returns an httptest.Server that mimics headroom's
// /v1/compress endpoint — it echoes messages back with mocked stats.
func fakeHeadroomServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/compress" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var req headroom.CompressRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Simulate compression by echoing messages as-is with stats.
		resp := headroom.CompressResponse{
			Messages:          req.Messages,
			TokensBefore:      500,
			TokensAfter:       150,
			TokensSaved:       350,
			CompressionRatio:  0.70,
			TransformsApplied: []string{"router:smart_crusher"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
}

func TestHeadroomMiddleware_Name(t *testing.T) {
	m := middleware.NewHeadroomMiddleware(nil, false)
	if m.Name() != "headroom" {
		t.Errorf("expected name %q, got %q", "headroom", m.Name())
	}
}

func TestHeadroomMiddleware_Disabled(t *testing.T) {
	m := middleware.NewHeadroomMiddleware(nil, false)

	req := &proxy.AnthropicRequest{
		Model:    "claude-sonnet-4-5",
		Messages: []proxy.AnthropicRequestMessage{{Role: "user", Content: "hello"}},
	}
	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != req {
		t.Error("disabled middleware should return the same pointer")
	}
}

func TestHeadroomMiddleware_ProcessRequest(t *testing.T) {
	srv := fakeHeadroomServer(t)
	defer srv.Close()

	client := headroom.NewClientWithURL(srv.URL, 5*time.Second)
	m := middleware.NewHeadroomMiddleware(client, true)

	req := &proxy.AnthropicRequest{
		Model: "claude-sonnet-4-5",
		System: []proxy.AnthropicSystemMessage{
			{Type: "text", Text: "You are a helpful assistant."},
		},
		Messages: []proxy.AnthropicRequestMessage{
			{Role: "user", Content: "What is Go?"},
			{Role: "assistant", Content: "Go is a programming language."},
		},
	}

	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The fake server echoes messages back, so we should have the system
	// message restored and user/assistant messages preserved.
	if len(got.System) == 0 {
		t.Error("expected system message after compression round-trip")
	}
	if len(got.Messages) < 2 {
		t.Errorf("expected at least 2 messages, got %d", len(got.Messages))
	}
}

func TestHeadroomMiddleware_FallbackOnError(t *testing.T) {
	// Closed server → connection error → should fall back gracefully.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close()

	client := headroom.NewClientWithURL(srv.URL, 1*time.Second)
	m := middleware.NewHeadroomMiddleware(client, true)

	req := &proxy.AnthropicRequest{
		Model:    "claude-sonnet-4-5",
		Messages: []proxy.AnthropicRequestMessage{{Role: "user", Content: "hi"}},
	}

	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("expected graceful fallback, got error: %v", err)
	}
	// On failure the original request is returned unchanged.
	if got.Model != req.Model {
		t.Errorf("expected model %q, got %q", req.Model, got.Model)
	}
	if len(got.Messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(got.Messages))
	}
}

func TestHeadroomMiddleware_ProcessResponse_Noop(t *testing.T) {
	m := middleware.NewHeadroomMiddleware(nil, true)
	data := []byte(`{"type":"message"}`)
	got, err := m.ProcessResponse(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("expected passthrough, got %q", got)
	}
}

func TestHeadroomMiddleware_InChain(t *testing.T) {
	srv := fakeHeadroomServer(t)
	defer srv.Close()

	client := headroom.NewClientWithURL(srv.URL, 5*time.Second)
	m := middleware.NewHeadroomMiddleware(client, true)

	var chain middleware.Chain
	chain.Add(m)

	req := &proxy.AnthropicRequest{
		Model:    "claude-sonnet-4-5",
		Messages: []proxy.AnthropicRequestMessage{{Role: "user", Content: "test"}},
	}

	got, err := chain.ProcessRequest(req)
	if err != nil {
		t.Fatalf("chain error: %v", err)
	}
	if len(got.Messages) == 0 {
		t.Error("expected at least 1 message from chain")
	}
}
