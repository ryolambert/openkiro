package token

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDebugLoggingEnabledUsesEnv(t *testing.T) {
	t.Setenv("OPENKIRO_DEBUG", "")
	t.Setenv("KIROLINK_DEBUG", "")
	if DebugLoggingEnabled() {
		t.Fatalf("expected debug logging to be disabled by default")
	}

	t.Setenv("OPENKIRO_DEBUG", "true")
	if !DebugLoggingEnabled() {
		t.Fatalf("expected debug logging to be enabled when OPENKIRO_DEBUG=true")
	}

	// Legacy fallback
	t.Setenv("OPENKIRO_DEBUG", "")
	t.Setenv("KIROLINK_DEBUG", "true")
	if !DebugLoggingEnabled() {
		t.Fatalf("expected debug logging to be enabled via legacy KIROLINK_DEBUG fallback")
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
			if got := RedactToken(tt.input); got != tt.want {
				t.Errorf("RedactToken(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestPayloadTrimLogsRequireDebug(t *testing.T) {
	t.Setenv("OPENKIRO_DEBUG", "")
	t.Setenv("KIROLINK_DEBUG", "")

	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	DebugLogf("[payload-trim] test message %d", 42)

	if buf.Len() > 0 {
		t.Fatalf("expected no output with debug off, got %q", buf.String())
	}

	t.Setenv("OPENKIRO_DEBUG", "true")
	DebugLogf("[payload-trim] test message %d", 42)

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
	got := GetKiroDBPath()
	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			if runtime.GOOS == tt.goos {
				if !strings.Contains(got, tt.contains) {
					t.Errorf("GetKiroDBPath() on %s = %q, want substring %q", tt.goos, got, tt.contains)
				}
				if !strings.HasPrefix(got, homeDir) && runtime.GOOS != "windows" {
					t.Errorf("GetKiroDBPath() on %s = %q, want prefix %q", tt.goos, got, homeDir)
				}
			}
		})
	}
	if strings.Contains(got, "\\\\") || (runtime.GOOS != "windows" && strings.Contains(got, "\\")) {
		t.Errorf("GetKiroDBPath() = %q, appears to use raw backslashes instead of filepath.Join", got)
	}
}

func TestGetTokenRetryOnParseFailure(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token.json")

	tests := []struct {
		name        string
		initialData string
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

			readAndParse := func() (Data, error) {
				data, err := os.ReadFile(tokenFile)
				if err != nil {
					return Data{}, err
				}
				var tok Data
				if err := json.Unmarshal(data, &tok); err != nil {
					return Data{}, err
				}
				return tok, nil
			}

			tok, err := readAndParse()
			if err != nil && !tt.wantErr {
				t.Fatalf("unexpected error: %v", err)
			}
			if err == nil && tt.wantErr {
				t.Fatal("expected error, got nil")
			}
			if err == nil && tok.AccessToken == "" {
				t.Error("expected non-empty AccessToken")
			}
		})
	}
}

func TestGetTokenRetrySucceedsAfterFix(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token.json")

	if err := os.WriteFile(tokenFile, []byte(`{truncated`), 0600); err != nil {
		t.Fatal(err)
	}

	validJSON := `{"accessToken":"abc123","refreshToken":"def456","expiresAt":"2026-12-31T00:00:00Z","region":"us-east-1"}`

	go func() {
		time.Sleep(50 * time.Millisecond)
		os.WriteFile(tokenFile, []byte(validJSON), 0600)
	}()

	readAndParse := func() (Data, error) {
		data, err := os.ReadFile(tokenFile)
		if err != nil {
			return Data{}, err
		}
		var tok Data
		if err := json.Unmarshal(data, &tok); err != nil {
			return Data{}, err
		}
		return tok, nil
	}

	_, err := readAndParse()
	if err == nil {
		t.Fatal("first read should fail with truncated JSON")
	}

	time.Sleep(100 * time.Millisecond)

	tok, err := readAndParse()
	if err != nil {
		t.Fatalf("retry should succeed after file fix: %v", err)
	}
	if tok.AccessToken != "abc123" {
		t.Errorf("got AccessToken=%q, want abc123", tok.AccessToken)
	}
}

func TestGetUpstreamClientSingleton(t *testing.T) {
	oldTransport := UpstreamTransport
	UpstreamTransport = &http.Transport{
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}
	ResetUpstreamClient()
	t.Cleanup(func() {
		UpstreamTransport = oldTransport
		ResetUpstreamClient()
	})

	c1 := GetUpstreamClient()
	c2 := GetUpstreamClient()
	if c1 != c2 {
		t.Fatal("GetUpstreamClient must return the same instance")
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
	if c1.Timeout != UpstreamHTTPTimeout {
		t.Errorf("Timeout=%v, want %v", c1.Timeout, UpstreamHTTPTimeout)
	}
}
