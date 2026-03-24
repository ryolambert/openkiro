package main

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDebugLoggingEnabledUsesEnv(t *testing.T) {
	t.Setenv("OPENKIRO_DEBUG", "")
	t.Setenv("KIROLINK_DEBUG", "")
	if debugLoggingEnabled() {
		t.Fatalf("expected debug logging to be disabled by default")
	}

	t.Setenv("OPENKIRO_DEBUG", "true")
	if !debugLoggingEnabled() {
		t.Fatalf("expected debug logging to be enabled when OPENKIRO_DEBUG=true")
	}

	// Legacy fallback
	t.Setenv("OPENKIRO_DEBUG", "")
	t.Setenv("KIROLINK_DEBUG", "true")
	if !debugLoggingEnabled() {
		t.Fatalf("expected debug logging to be enabled via legacy KIROLINK_DEBUG fallback")
	}
}

func TestNewHTTPServerUsesLocalhostOnlyAndTimeouts(t *testing.T) {
	server := newHTTPServer(defaultListenAddress, "1234", http.NewServeMux())

	if got := server.Addr; got != "127.0.0.1:1234" {
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

func TestNewHTTPServerCustomListenAddress(t *testing.T) {
	server := newHTTPServer("0.0.0.0", "5678", http.NewServeMux())
	if got := server.Addr; got != "0.0.0.0:5678" {
		t.Fatalf("expected custom listen address, got %q", got)
	}
}

func TestNewProxyHandlerRejectsOversizedRequestBody(t *testing.T) {
	orig := maxRequestBodyBytes
	maxRequestBodyBytes = 1 << 10 // 1KB for test
	t.Cleanup(func() { maxRequestBodyBytes = orig })

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
		strings.Repeat("a", int(maxRequestBodyBytes)) + `"}]}`
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

func TestRedactToken(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{"normal", "abcdefghijklmnopqrst", "abcdefgh...qrst"},
		{"short", "abc", "***"},
		{"exactly12", "123456789012", "***"},
		{"13chars", "1234567890123", "12345678...0123"},
		{"empty", "", "***"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := redactToken(tt.input); got != tt.want {
				t.Errorf("redactToken(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestPayloadTrimLogsRequireDebug(t *testing.T) {
	// Ensure payload-trim logs don't appear when debug is off
	t.Setenv("OPENKIRO_DEBUG", "")
	t.Setenv("KIROLINK_DEBUG", "")

	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	debugLogf("[payload-trim] test message %d", 42)

	if buf.Len() > 0 {
		t.Fatalf("expected no output with debug off, got %q", buf.String())
	}

	// Now enable debug
	t.Setenv("OPENKIRO_DEBUG", "true")
	debugLogf("[payload-trim] test message %d", 42)

	if !strings.Contains(buf.String(), "[payload-trim] test message 42") {
		t.Fatalf("expected debug output, got %q", buf.String())
	}
}

func TestGetKiroDBPath(t *testing.T) {
	homeDir, _ := os.UserHomeDir()
	tests := []struct {
		goos     string
		contains string
	}{
		{"darwin", filepath.Join("Library", "Application Support", "kiro-cli", "data.sqlite3")},
		{"linux", filepath.Join(".local", "share", "kiro-cli", "data.sqlite3")},
		{"windows", filepath.Join("kiro-cli", "data.sqlite3")},
	}
	got := getKiroDBPath()
	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			if runtime.GOOS == tt.goos {
				if !strings.Contains(got, tt.contains) {
					t.Errorf("getKiroDBPath() on %s = %q, want substring %q", tt.goos, got, tt.contains)
				}
				if !strings.HasPrefix(got, homeDir) && runtime.GOOS != "windows" {
					t.Errorf("getKiroDBPath() on %s = %q, want prefix %q", tt.goos, got, homeDir)
				}
			}
		})
	}
	// Verify filepath.Join is used (no raw separators)
	if strings.Contains(got, "\\\\") || (runtime.GOOS != "windows" && strings.Contains(got, "\\")) {
		t.Errorf("getKiroDBPath() = %q, appears to use raw backslashes instead of filepath.Join", got)
	}
}
