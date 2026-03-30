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

func TestClient_HealthyDown(t *testing.T) {
	// Connect to a closed server.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close()

	client := headroom.NewClientWithURL(srv.URL, 1*time.Second)
	if client.Healthy(context.Background()) {
		t.Error("expected healthy to return false for closed server")
	}
}
