package token

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Data defines the token file structure.
type Data struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresAt    string `json:"expiresAt,omitempty"`
}

// UpstreamHTTPTimeout is the timeout for upstream HTTP requests.
const UpstreamHTTPTimeout = 60 * time.Second

// GetTokenFilePath returns the cross-platform token file path.
func GetTokenFilePath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("Failed to get user home directory: %v\n", err)
		os.Exit(1)
	}
	return filepath.Join(homeDir, ".aws", "sso", "cache", "kiro-auth-token.json")
}

// ReadToken reads and prints token information.
func ReadToken() {
	tokenPath := GetTokenFilePath()

	data, err := os.ReadFile(tokenPath)
	if err != nil {
		fmt.Printf("Failed to read token file: %v\n", err)
		os.Exit(1)
	}

	var tok Data
	if err := json.Unmarshal(data, &tok); err != nil {
		fmt.Printf("Failed to parse token file: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Token Information:")
	fmt.Printf("Access Token: %s\n", tok.AccessToken)
	fmt.Printf("Refresh Token: %s\n", tok.RefreshToken)
	if tok.ExpiresAt != "" {
		fmt.Printf("Expires at: %s\n", tok.ExpiresAt)
	}
}

// GetKiroDBPath returns the path to Kiro CLI's SQLite database.
func GetKiroDBPath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("Failed to get home directory: %v\n", err)
		os.Exit(1)
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(homeDir, "Library", "Application Support", "kiro-cli", "data.sqlite3")
	case "windows":
		configDir, err := os.UserConfigDir()
		if err != nil {
			fmt.Printf("Failed to get config directory: %v\n", err)
			os.Exit(1)
		}
		return filepath.Join(configDir, "kiro-cli", "data.sqlite3")
	default:
		return filepath.Join(homeDir, ".local", "share", "kiro-cli", "data.sqlite3")
	}
}

// RefreshToken syncs the live token from Kiro CLI's SQLite database.
func RefreshToken() {
	dbPath := GetKiroDBPath()

	if _, err := exec.LookPath("sqlite3"); err != nil {
		fmt.Fprintf(os.Stderr, "Error: 'sqlite3' not found on PATH.\n")
		fmt.Fprintf(os.Stderr, "The 'refresh' command requires the sqlite3 CLI to read Kiro's local database.\n")
		fmt.Fprintf(os.Stderr, "Install it:\n")
		fmt.Fprintf(os.Stderr, "  macOS:   brew install sqlite3\n")
		fmt.Fprintf(os.Stderr, "  Linux:   sudo apt install sqlite3  (or your distro's equivalent)\n")
		fmt.Fprintf(os.Stderr, "  Windows: winget install SQLite.SQLite  (or download from https://sqlite.org/download.html)\n")
		os.Exit(1)
	}

	out, err := exec.Command("sqlite3", dbPath,
		"SELECT value FROM auth_kv WHERE key='kirocli:odic:token';").Output()
	if err != nil {
		fmt.Printf("Failed to read Kiro token from database: %v\nRun 'kiro login' to authenticate.\n", err)
		os.Exit(1)
	}

	var sqliteToken struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresAt    string `json:"expires_at"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &sqliteToken); err != nil {
		fmt.Printf("Failed to parse Kiro token from database: %v\nRun 'kiro login' to authenticate.\n", err)
		os.Exit(1)
	}

	newToken := Data{
		AccessToken:  sqliteToken.AccessToken,
		RefreshToken: sqliteToken.RefreshToken,
		ExpiresAt:    sqliteToken.ExpiresAt,
	}

	tokenPath := GetTokenFilePath()
	newData, err := json.MarshalIndent(newToken, "", "  ")
	if err != nil {
		fmt.Printf("Failed to serialize token: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(tokenPath, newData, 0600); err != nil {
		fmt.Printf("Failed to write token file: %v\n", err)
		os.Exit(1)
	}

	// Update env file if we know the base URL from stored credentials.
	if baseURL, _, err := ReadCredentials(); err == nil && baseURL != "" {
		_ = WriteEnvFile(baseURL, newToken.AccessToken)
	}

	fmt.Println("Token synced from Kiro CLI successfully!")
	fmt.Printf("Access Token: %s\n", RedactToken(newToken.AccessToken))
}

// ExportEnvVars exports environment variables.
func ExportEnvVars(port string) {
	tokenPath := GetTokenFilePath()

	data, err := os.ReadFile(tokenPath)
	if err != nil {
		fmt.Printf("Failed to read token, please install Kiro and login first!: %v\n", err)
		os.Exit(1)
	}

	var tok Data
	if err := json.Unmarshal(data, &tok); err != nil {
		fmt.Printf("Failed to parse token file: %v\n", err)
		os.Exit(1)
	}

	baseURL := fmt.Sprintf("http://localhost:%s", port)
	_ = WriteEnvFile(baseURL, tok.AccessToken)

	if runtime.GOOS == "windows" {
		fmt.Println("CMD")
		fmt.Printf("set ANTHROPIC_BASE_URL=%s\n", baseURL)
		fmt.Printf("set ANTHROPIC_API_KEY=%s\n\n", tok.AccessToken)
		fmt.Println("Powershell")
		fmt.Printf("$env:ANTHROPIC_BASE_URL=\"%s\"\n", baseURL)
		fmt.Printf(`$env:ANTHROPIC_API_KEY="%s"`, tok.AccessToken)
	} else {
		fmt.Printf("export ANTHROPIC_BASE_URL=%s\n", baseURL)
		fmt.Printf("export ANTHROPIC_API_KEY=\"%s\"\n", tok.AccessToken)
	}
}

// GetToken gets the current token, retrying once on parse failure.
func GetToken() (Data, error) {
	tokenPath := GetTokenFilePath()

	readAndParse := func() (Data, error) {
		data, err := os.ReadFile(tokenPath)
		if err != nil {
			return Data{}, fmt.Errorf("reading token file: %w", err)
		}
		var tok Data
		if err := json.Unmarshal(data, &tok); err != nil {
			return Data{}, fmt.Errorf("parsing token file: %w", err)
		}
		return tok, nil
	}

	tok, err := readAndParse()
	if err == nil {
		return tok, nil
	}

	DebugLogf("token read failed, retrying in 100ms: %v", err)
	time.Sleep(100 * time.Millisecond)

	tok, retryErr := readAndParse()
	if retryErr != nil {
		return Data{}, fmt.Errorf("token read failed after retry: %w", retryErr)
	}
	return tok, nil
}

// DebugLoggingEnabled returns true if debug logging is enabled.
func DebugLoggingEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OPENKIRO_DEBUG"))) {
	case "1", "true", "yes", "on", "debug":
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("KIROLINK_DEBUG"))) {
	case "1", "true", "yes", "on", "debug":
		fmt.Fprintln(os.Stderr, "WARNING: KIROLINK_DEBUG is deprecated, use OPENKIRO_DEBUG")
		return true
	}
	return false
}

// DebugLogf logs a message if debug logging is enabled.
func DebugLogf(format string, args ...any) {
	if DebugLoggingEnabled() {
		log.Printf(format, args...)
	}
}

// RedactToken redacts a token for display.
func RedactToken(s string) string {
	if len(s) <= 12 {
		return "***"
	}
	return s[:8] + "..." + s[len(s)-4:]
}

// DebugLogBodySummary logs a body summary if debug logging is enabled.
func DebugLogBodySummary(label string, body []byte) {
	if !DebugLoggingEnabled() {
		return
	}
	sum := sha256.Sum256(body)
	DebugLogf("%s size=%d sha256=%x", label, len(body), sum[:8])
}

var (
	upstreamClientOnce sync.Once
	upstreamClient     *http.Client
	// UpstreamTransport is the RoundTripper used by the pooled client.
	UpstreamTransport http.RoundTripper = &http.Transport{
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}
)

// ResetUpstreamClient clears the pooled client so the next GetUpstreamClient
// call re-initializes it. Test-only.
func ResetUpstreamClient() {
	upstreamClientOnce = *new(sync.Once)
	upstreamClient = nil
}

// GetUpstreamClient returns the singleton HTTP client for upstream requests.
func GetUpstreamClient() *http.Client {
	upstreamClientOnce.Do(func() {
		upstreamClient = &http.Client{
			Timeout:   UpstreamHTTPTimeout,
			Transport: UpstreamTransport,
		}
	})
	return upstreamClient
}
