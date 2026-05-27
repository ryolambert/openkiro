package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/ryolambert/openkiro/internal/token"
)

// ListAvailableProfilesResponse is the response from the
// AmazonCodeWhispererService.ListAvailableProfiles Coral RPC.
type ListAvailableProfilesResponse struct {
	Profiles []struct {
		Type        string `json:"__type,omitempty"`
		Arn         string `json:"arn"`
		ProfileName string `json:"profileName"`
	} `json:"profiles"`
}

// profileCache caches the resolved profileArn so we only call ListAvailableProfiles once.
var (
	profileCacheMu  sync.RWMutex
	profileCacheArn string
	profileCacheAt  time.Time
)

const profileCacheTTL = 30 * time.Minute

// FetchProfileArn calls AmazonCodeWhispererService.ListAvailableProfiles and
// returns the first profile ARN. The result is cached for profileCacheTTL.
//
// If the call fails, the cached value (even if stale) is returned. If there
// is no cached value either, an empty string is returned.
func FetchProfileArn(ctx context.Context, accessToken string) (string, error) {
	profileCacheMu.RLock()
	if profileCacheArn != "" && time.Since(profileCacheAt) < profileCacheTTL {
		arn := profileCacheArn
		profileCacheMu.RUnlock()
		return arn, nil
	}
	profileCacheMu.RUnlock()

	body := []byte(`{"maxResults":10}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		CodeWhispererRuntimeURL, bytes.NewReader(body))
	if err != nil {
		return profileCacheArn, fmt.Errorf("create profiles request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", CoralContentType)
	req.Header.Set("X-Amz-Target", CoralTargetListProfiles)
	req.Header.Set("User-Agent", KiroUserAgent)

	resp, err := token.GetUpstreamClient().Do(req)
	if err != nil {
		return profileCacheArn, fmt.Errorf("list profiles request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return profileCacheArn, fmt.Errorf("read profiles response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return profileCacheArn, fmt.Errorf("list profiles status %d: %s", resp.StatusCode, string(respBody))
	}

	var parsed ListAvailableProfilesResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return profileCacheArn, fmt.Errorf("parse profiles response: %w", err)
	}
	if len(parsed.Profiles) == 0 {
		return profileCacheArn, fmt.Errorf("no profiles returned")
	}

	arn := parsed.Profiles[0].Arn
	profileCacheMu.Lock()
	profileCacheArn = arn
	profileCacheAt = time.Now()
	profileCacheMu.Unlock()

	log.Printf("resolved profileArn: %s (profile %q)", arn, parsed.Profiles[0].ProfileName)
	return arn, nil
}

// ResolveProfileArn returns the profileArn to use, in this priority order:
//  1. KIRO_PROFILE_ARN env var (manual override)
//  2. Cached value from prior ListAvailableProfiles call
//  3. Live ListAvailableProfiles fetch (caches result)
//  4. Empty string (caller may fall back to ProfileArnIAM constant)
func ResolveProfileArn(ctx context.Context, accessToken string) string {
	if arn := GetProfileArn(); arn != "" {
		return arn
	}
	profileCacheMu.RLock()
	if profileCacheArn != "" {
		arn := profileCacheArn
		profileCacheMu.RUnlock()
		return arn
	}
	profileCacheMu.RUnlock()

	arn, err := FetchProfileArn(ctx, accessToken)
	if err != nil {
		log.Printf("profileArn fetch failed: %v — falling back", err)
	}
	return arn
}
