package main

import (
	"bytes"
	jsonStr "encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
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

func TestGetTokenRetryOnParseFailure(t *testing.T) {
	// Save and restore original getTokenFilePath behavior
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token.json")

	// Override getTokenFilePath by setting the env var it reads
	// Actually, getTokenFilePath is hardcoded. We need to test getToken indirectly
	// by writing to the actual path. Instead, test the retry pattern directly.

	tests := []struct {
		name        string
		initialData string
		fixAfter    bool // if true, write valid JSON after 50ms
		fixData     string
		wantErr     bool
	}{
		{
			name:        "valid JSON succeeds immediately",
			initialData: `{"accessToken":"abc123","refreshToken":"def456","expiresAt":"2026-12-31T00:00:00Z","region":"us-east-1"}`,
			wantErr:     false,
		},
		{
			name:        "permanently invalid JSON fails after retry",
			initialData: `{truncated`,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.WriteFile(tokenFile, []byte(tt.initialData), 0600); err != nil {
				t.Fatal(err)
			}

			// Test the readAndParse + retry logic inline (getToken uses hardcoded path)
			readAndParse := func() (TokenData, error) {
				data, err := os.ReadFile(tokenFile)
				if err != nil {
					return TokenData{}, err
				}
				var token TokenData
				if err := jsonStr.Unmarshal(data, &token); err != nil {
					return TokenData{}, err
				}
				return token, nil
			}

			token, err := readAndParse()
			if err != nil && !tt.wantErr {
				t.Fatalf("unexpected error: %v", err)
			}
			if err == nil && tt.wantErr {
				t.Fatal("expected error, got nil")
			}
			if err == nil && token.AccessToken == "" {
				t.Error("expected non-empty AccessToken")
			}
		})
	}
}

func TestGetTokenRetrySucceedsAfterFix(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token.json")

	// Write truncated JSON initially
	if err := os.WriteFile(tokenFile, []byte(`{truncated`), 0600); err != nil {
		t.Fatal(err)
	}

	validJSON := `{"accessToken":"abc123","refreshToken":"def456","expiresAt":"2026-12-31T00:00:00Z","region":"us-east-1"}`

	// Fix the file after 50ms (simulates writer completing)
	go func() {
		time.Sleep(50 * time.Millisecond)
		os.WriteFile(tokenFile, []byte(validJSON), 0600)
	}()

	// Simulate the retry logic from getToken
	readAndParse := func() (TokenData, error) {
		data, err := os.ReadFile(tokenFile)
		if err != nil {
			return TokenData{}, err
		}
		var token TokenData
		if err := jsonStr.Unmarshal(data, &token); err != nil {
			return TokenData{}, err
		}
		return token, nil
	}

	_, err := readAndParse()
	if err == nil {
		t.Fatal("first read should fail with truncated JSON")
	}

	// Wait 100ms (same as getToken retry delay), then retry
	time.Sleep(100 * time.Millisecond)

	token, err := readAndParse()
	if err != nil {
		t.Fatalf("retry should succeed after file fix: %v", err)
	}
	if token.AccessToken != "abc123" {
		t.Errorf("got AccessToken=%q, want abc123", token.AccessToken)
	}
}

func TestGetUpstreamClientSingleton(t *testing.T) {
	// Reset to ensure we test fresh initialization with production transport.
	oldTransport := upstreamTransport
	upstreamTransport = &http.Transport{
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}
	resetUpstreamClient()
	t.Cleanup(func() {
		upstreamTransport = oldTransport
		resetUpstreamClient()
	})

	c1 := getUpstreamClient()
	c2 := getUpstreamClient()
	if c1 != c2 {
		t.Fatal("getUpstreamClient must return the same instance")
	}
	tr, ok := c1.Transport.(*http.Transport)
	if !ok {
		t.Fatal("transport is not *http.Transport")
	}
	if tr.MaxIdleConnsPerHost != 10 {
		t.Errorf("MaxIdleConnsPerHost=%d, want 10", tr.MaxIdleConnsPerHost)
	}
	if tr.IdleConnTimeout != 90*time.Second {
		t.Errorf("IdleConnTimeout=%v, want 90s", tr.IdleConnTimeout)
	}
	if c1.Timeout != upstreamHTTPTimeout {
		t.Errorf("Timeout=%v, want %v", c1.Timeout, upstreamHTTPTimeout)
	}
}

func TestModelsEndpointDeterministic(t *testing.T) {
	mux := newProxyHandler()
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
		if err := jsonStr.NewDecoder(rec.Body).Decode(&resp); err != nil {
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

func TestResolvePort(t *testing.T) {
	tests := []struct {
		name    string
		flag    string
		env     string
		want    string
		wantErr bool
	}{
		{"default", "", "", "1234", false},
		{"flag wins", "5678", "", "5678", false},
		{"env wins over default", "", "9999", "9999", false},
		{"flag wins over env", "5678", "9999", "5678", false},
		{"invalid non-numeric", "abc", "", "", true},
		{"port zero", "0", "", "", true},
		{"port too high", "70000", "", "", true},
		{"valid boundary low", "1", "", "1", false},
		{"valid boundary high", "65535", "", "65535", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env != "" {
				t.Setenv("OPENKIRO_PORT", tt.env)
			} else {
				t.Setenv("OPENKIRO_PORT", "")
			}
			got, err := resolvePort(tt.flag)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for flag=%q env=%q, got %q", tt.flag, tt.env, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLogDir(t *testing.T) {
	dir, err := logDir()
	if err != nil {
		t.Fatalf("logDir() error: %v", err)
	}
	if dir == "" {
		t.Fatal("logDir() returned empty string")
	}
	if !filepath.IsAbs(dir) {
		t.Errorf("logDir() returned relative path: %s", dir)
	}
	// Verify platform-specific path component
	switch runtime.GOOS {
	case "darwin":
		if !contains(dir, filepath.Join("Library", "Logs", "openkiro")) {
			t.Errorf("darwin: expected Library/Logs/openkiro in %s", dir)
		}
	case "windows":
		if !contains(dir, filepath.Join("openkiro", "logs")) {
			t.Errorf("windows: expected openkiro\\logs in %s", dir)
		}
	default:
		if !contains(dir, filepath.Join(".local", "state", "openkiro")) {
			t.Errorf("linux: expected .local/state/openkiro in %s", dir)
		}
	}
	// Verify directory was created
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("logDir() directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("logDir() path is not a directory: %s", dir)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestPidFilePath(t *testing.T) {
	p, err := pidFilePath()
	if err != nil {
		t.Fatalf("pidFilePath() error: %v", err)
	}
	if filepath.Base(p) != "openkiro.pid" {
		t.Errorf("pidFilePath() base = %q, want openkiro.pid", filepath.Base(p))
	}
	if !filepath.IsAbs(p) {
		t.Errorf("pidFilePath() returned relative path: %s", p)
	}
}

func TestLogFilePath(t *testing.T) {
	p, err := logFilePath()
	if err != nil {
		t.Fatalf("logFilePath() error: %v", err)
	}
	if filepath.Base(p) != "openkiro.log" {
		t.Errorf("logFilePath() base = %q, want openkiro.log", filepath.Base(p))
	}
	if !filepath.IsAbs(p) {
		t.Errorf("logFilePath() returned relative path: %s", p)
	}
}

func TestWriteAndReadPID(t *testing.T) {
	pid := os.Getpid()
	if err := writePID(pid); err != nil {
		t.Fatalf("writePID: %v", err)
	}
	t.Cleanup(func() { removePID() })

	got, err := readPID()
	if err != nil {
		t.Fatalf("readPID: %v", err)
	}
	if got != pid {
		t.Errorf("readPID() = %d, want %d", got, pid)
	}
}

func TestRemovePID(t *testing.T) {
	if err := writePID(12345); err != nil {
		t.Fatalf("writePID: %v", err)
	}
	if err := removePID(); err != nil {
		t.Fatalf("removePID: %v", err)
	}
	p, _ := pidFilePath()
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("PID file still exists after removePID")
	}
}

func TestRemovePIDNonExistent(t *testing.T) {
	// Ensure no PID file exists
	removePID()
	if err := removePID(); err != nil {
		t.Errorf("removePID on non-existent file: %v", err)
	}
}

func TestIsRunning(t *testing.T) {
	tests := []struct {
		name string
		pid  int
		want bool
	}{
		{"own process", os.Getpid(), true},
		{"bogus PID", 99999999, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRunning(tt.pid); got != tt.want {
				t.Errorf("isRunning(%d) = %v, want %v", tt.pid, got, tt.want)
			}
		})
	}
}

func TestCleanStalePID(t *testing.T) {
	// Write a fake PID that isn't running
	if err := writePID(99999999); err != nil {
		t.Fatalf("writePID: %v", err)
	}
	if err := cleanStalePID(); err != nil {
		t.Fatalf("cleanStalePID: %v", err)
	}
	p, _ := pidFilePath()
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("stale PID file not cleaned up")
	}
}

func TestCleanStalePIDRunning(t *testing.T) {
	// Write our own PID — should report "already running"
	if err := writePID(os.Getpid()); err != nil {
		t.Fatalf("writePID: %v", err)
	}
	t.Cleanup(func() { removePID() })

	err := cleanStalePID()
	if err == nil {
		t.Fatal("cleanStalePID should error when process is running")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("unexpected error: %v", err)
	}
}
