package headroom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// CompressRequest is the payload sent to headroom's POST /v1/compress.
type CompressRequest struct {
	Messages []Message `json:"messages"`
	Model    string    `json:"model"`
}

// Message is a single chat message in OpenAI format, which headroom expects.
type Message struct {
	Role       string `json:"role"`
	Content    any    `json:"content"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// CompressResponse is the response from headroom's POST /v1/compress.
type CompressResponse struct {
	Messages          []Message `json:"messages"`
	TokensBefore      int       `json:"tokens_before"`
	TokensAfter       int       `json:"tokens_after"`
	TokensSaved       int       `json:"tokens_saved"`
	CompressionRatio  float64   `json:"compression_ratio"`
	TransformsApplied []string  `json:"transforms_applied"`
	CCRHashes         []string  `json:"ccr_hashes"`
}

// Client communicates with the headroom Python proxy over HTTP.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a headroom Client from the given config.
func NewClient(cfg Config) *Client {
	return &Client{
		baseURL: cfg.BaseURL(),
		httpClient: &http.Client{
			Timeout: cfg.CompressTimeout,
		},
	}
}

// NewClientWithURL creates a headroom Client for the given base URL.
func NewClientWithURL(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// Compress sends messages to headroom for compression.
func (c *Client) Compress(ctx context.Context, messages []Message, model string) (*CompressResponse, error) {
	reqBody := CompressRequest{
		Messages: messages,
		Model:    model,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("headroom: marshal compress request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/compress", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("headroom: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("headroom: compress request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("headroom: read compress response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("headroom: compress returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var result CompressResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("headroom: unmarshal compress response: %w", err)
	}
	return &result, nil
}

// Healthy checks whether headroom's /health endpoint responds successfully.
func (c *Client) Healthy(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
