package headroom_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ryolambert/openkiro/internal/headroom"
)

func TestDefaultConfig(t *testing.T) {
	cfg := headroom.DefaultConfig()
	if cfg.Host != "127.0.0.1" {
		t.Errorf("expected host 127.0.0.1, got %s", cfg.Host)
	}
	if cfg.Port != "8787" {
		t.Errorf("expected port 8787, got %s", cfg.Port)
	}
	if cfg.BaseURL() != "http://127.0.0.1:8787" {
		t.Errorf("expected base URL http://127.0.0.1:8787, got %s", cfg.BaseURL())
	}
	if !cfg.Enabled {
		t.Error("expected Enabled to be true by default")
	}
	if cfg.PipPackage != "headroom-ai[proxy]" {
		t.Errorf("expected PipPackage headroom-ai[proxy], got %s", cfg.PipPackage)
	}
	if cfg.PythonBin != "python3" {
		t.Errorf("expected PythonBin python3, got %s", cfg.PythonBin)
	}
}

func TestConfig_BaseURL_CustomHostPort(t *testing.T) {
	cfg := headroom.Config{Host: "0.0.0.0", Port: "9999"}
	want := "http://0.0.0.0:9999"
	if got := cfg.BaseURL(); got != want {
		t.Errorf("BaseURL() = %q, want %q", got, want)
	}
}

func TestNewClient(t *testing.T) {
	cfg := headroom.DefaultConfig()
	client := headroom.NewClient(cfg)
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	// Verify it can at least attempt a health check (will fail — no server).
	if client.Healthy(context.Background()) {
		t.Error("expected unhealthy when no server is running")
	}
}

func TestClient_Compress(t *testing.T) {
	// Fake headroom server that echoes messages back with token stats.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/compress" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req headroom.CompressRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		resp := headroom.CompressResponse{
			Messages:          req.Messages,
			TokensBefore:      1000,
			TokensAfter:       300,
			TokensSaved:       700,
			CompressionRatio:  0.70,
			TransformsApplied: []string{"router:smart_crusher:0.30"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := headroom.NewClientWithURL(srv.URL, 5*time.Second)

	msgs := []headroom.Message{
		{Role: "user", Content: "Hello, world!"},
	}

	result, err := client.Compress(context.Background(), msgs, "claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TokensSaved != 700 {
		t.Errorf("expected 700 tokens saved, got %d", result.TokensSaved)
	}
	if result.CompressionRatio != 0.70 {
		t.Errorf("expected compression ratio 0.70, got %f", result.CompressionRatio)
	}
	if len(result.Messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(result.Messages))
	}
	if len(result.TransformsApplied) != 1 {
		t.Errorf("expected 1 transform, got %d", len(result.TransformsApplied))
	}
}

func TestClient_CompressErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"missing model"}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	client := headroom.NewClientWithURL(srv.URL, 5*time.Second)

	_, err := client.Compress(context.Background(), nil, "")
	if err == nil {
		t.Fatal("expected error for bad request, got nil")
	}
}

func TestClient_CompressInvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{invalid json}`))
	}))
	defer srv.Close()

	client := headroom.NewClientWithURL(srv.URL, 5*time.Second)

	_, err := client.Compress(context.Background(), []headroom.Message{
		{Role: "user", Content: "hi"},
	}, "gpt-4o")
	if err == nil {
		t.Fatal("expected error for invalid JSON response, got nil")
	}
}

func TestClient_CompressConnectionRefused(t *testing.T) {
	// Connect to a closed server.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close()

	client := headroom.NewClientWithURL(srv.URL, 1*time.Second)

	_, err := client.Compress(context.Background(), []headroom.Message{
		{Role: "user", Content: "hi"},
	}, "gpt-4o")
	if err == nil {
		t.Fatal("expected error for connection refused, got nil")
	}
}

func TestClient_CompressCancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer srv.Close()

	client := headroom.NewClientWithURL(srv.URL, 5*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := client.Compress(ctx, []headroom.Message{
		{Role: "user", Content: "hi"},
	}, "gpt-4o")
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
}

func TestClient_Healthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	client := headroom.NewClientWithURL(srv.URL, 5*time.Second)
	if !client.Healthy(context.Background()) {
		t.Error("expected healthy to return true")
	}
}

func TestClient_HealthyUnhealthyStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	client := headroom.NewClientWithURL(srv.URL, 5*time.Second)
	if client.Healthy(context.Background()) {
		t.Error("expected healthy to return false for 503")
	}
}

func TestClient_HealthyDown(t *testing.T) {
	// Connect to a closed server.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close()

	client := headroom.NewClientWithURL(srv.URL, 1*time.Second)
	if client.Healthy(context.Background()) {
		t.Error("expected healthy to return false for closed server")
	}
}
