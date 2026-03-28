package token

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteReadCredentialsRoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		apiKey  string
	}{
		{"basic", "http://localhost:1234", "test-key-abc123"},
		{"custom port", "http://localhost:9000", "another-key-xyz"},
		{"empty key", "http://localhost:1234", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := t.TempDir()
			fp := filepath.Join(tmp, "credentials.json")

			creds := Credentials{BaseURL: tt.baseURL, APIKey: tt.apiKey}
			data, err := json.MarshalIndent(creds, "", "  ")
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if err := os.WriteFile(fp, data, 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}

			info, err := os.Stat(fp)
			if err != nil {
				t.Fatalf("stat: %v", err)
			}
			perm := info.Mode().Perm()
			if runtime.GOOS != "windows" && perm != 0o600 {
				t.Errorf("permissions = %o, want 0600", perm)
			}

			raw, err := os.ReadFile(fp)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			var got Credentials
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got.BaseURL != tt.baseURL {
				t.Errorf("baseURL = %q, want %q", got.BaseURL, tt.baseURL)
			}
			if got.APIKey != tt.apiKey {
				t.Errorf("apiKey = %q, want %q", got.APIKey, tt.apiKey)
			}
		})
	}
}
