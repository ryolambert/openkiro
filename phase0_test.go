package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDebugLoggingEnabledUsesEnv(t *testing.T) {
	t.Setenv("KIROLINK_DEBUG", "")
	if debugLoggingEnabled() {
		t.Fatalf("expected debug logging to be disabled by default")
	}

	t.Setenv("KIROLINK_DEBUG", "true")
	if !debugLoggingEnabled() {
		t.Fatalf("expected debug logging to be enabled when KIROLINK_DEBUG=true")
	}
}

func TestNewHTTPServerUsesLocalhostOnlyAndTimeouts(t *testing.T) {
	server := newHTTPServer("8080", http.NewServeMux())

	if got := server.Addr; got != "127.0.0.1:8080" {
		t.Fatalf("expected loopback-only listen address, got %q", got)
	}
	if got := server.ReadTimeout; got != serverReadTimeout {
		t.Fatalf("expected ReadTimeout %v, got %v", serverReadTimeout, got)
	}
	if got := server.WriteTimeout; got != serverWriteTimeout {
		t.Fatalf("expected WriteTimeout %v, got %v", serverWriteTimeout, got)
	}
	if got := server.IdleTimeout; got != serverIdleTimeout {
		t.Fatalf("expected IdleTimeout %v, got %v", serverIdleTimeout, got)
	}
	if got := server.ReadHeaderTimeout; got != serverHeaderTimeout {
		t.Fatalf("expected ReadHeaderTimeout %v, got %v", serverHeaderTimeout, got)
	}
}

func TestNewProxyHandlerRejectsOversizedRequestBody(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	tokenDir := filepath.Join(tempHome, ".aws", "sso", "cache")
	if err := os.MkdirAll(tokenDir, 0o755); err != nil {
		t.Fatalf("mkdir token dir: %v", err)
	}
	tokenFile := filepath.Join(tokenDir, "kiro-auth-token.json")
	if err := os.WriteFile(tokenFile, []byte(`{"accessToken":"token","refreshToken":"refresh"}`), 0o644); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	payload := `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"` +
		strings.Repeat("a", maxRequestBodyBytes) + `"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(payload))
	recorder := httptest.NewRecorder()

	newProxyHandler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected status 413 for oversized request, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "Request body exceeds") {
		t.Fatalf("expected oversized body message, got %q", recorder.Body.String())
	}
}

func TestHandlePanicHidesRecoveredValue(t *testing.T) {
	recorder := httptest.NewRecorder()

	handlePanic(recorder, "secret panic details")

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
