package token

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Credentials holds the stored env var values.
type Credentials struct {
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
}

// CredentialsDir returns ~/.openkiro/.
func CredentialsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("credentials dir: %w", err)
	}
	return filepath.Join(home, ".openkiro"), nil
}

// CredentialsFilePath returns ~/.openkiro/credentials.json.
func CredentialsFilePath() (string, error) {
	dir, err := CredentialsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "credentials.json"), nil
}

// WriteCredentials writes credentials to ~/.openkiro/credentials.json with 0600 perms.
func WriteCredentials(baseURL, apiKey string) error {
	dir, err := CredentialsDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("credentials mkdir: %w", err)
	}
	data, err := json.MarshalIndent(Credentials{BaseURL: baseURL, APIKey: apiKey}, "", "  ")
	if err != nil {
		return fmt.Errorf("credentials marshal: %w", err)
	}
	fp, err := CredentialsFilePath()
	if err != nil {
		return err
	}
	if err := os.WriteFile(fp, data, 0o600); err != nil {
		return fmt.Errorf("credentials write: %w", err)
	}
	return nil
}

// ReadCredentials reads credentials from ~/.openkiro/credentials.json.
func ReadCredentials() (baseURL, apiKey string, err error) {
	fp, err := CredentialsFilePath()
	if err != nil {
		return "", "", err
	}
	data, err := os.ReadFile(fp)
	if err != nil {
		return "", "", fmt.Errorf("credentials read: %w", err)
	}
	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return "", "", fmt.Errorf("credentials parse: %w", err)
	}
	return creds.BaseURL, creds.APIKey, nil
}
