// Package middleware defines the Middleware interface and Chain used to
// intercept and transform requests and responses in the openkiro proxy.
package middleware

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ryolambert/openkiro/internal/proxy"
)

// MemoryEntry holds a single recalled memory item.
//
// Inspired by rtk-ai/icm memory recall:
// https://github.com/rtk-ai/icm
type MemoryEntry struct {
	// Key is a short identifier or tag for the memory.
	Key string
	// Value is the remembered content.
	Value string
	// Timestamp records when the entry was stored.
	Timestamp time.Time
	// Score is an optional relevance score (higher is more relevant).
	Score float64
}

// MemoryStore is the interface that backing stores must satisfy.
//
// Query returns the most relevant entries for the given context string.
// Store persists a new entry into the backing store.
type MemoryStore interface {
	Query(context string) ([]MemoryEntry, error)
	Store(entry MemoryEntry) error
}

// InMemoryStore is a simple in-process implementation of MemoryStore backed
// by a slice. It is intended for testing and lightweight use cases where
// persistence is not required.
type InMemoryStore struct {
	mu      sync.RWMutex
	entries []MemoryEntry
}

// Store appends entry to the in-memory backing slice.
func (s *InMemoryStore) Store(entry MemoryEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, entry)
	return nil
}

// Query returns all stored entries (no filtering is applied in the in-memory
// implementation). A nil context is handled gracefully.
func (s *InMemoryStore) Query(_ string) ([]MemoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.entries) == 0 {
		return nil, nil
	}
	out := make([]MemoryEntry, len(s.entries))
	copy(out, s.entries)
	return out, nil
}

// MemoryMiddleware injects relevant memory context into each request's system
// prompt and optionally extracts new memories from assistant responses.
//
// Inspired by rtk-ai/icm memory recall:
// https://github.com/rtk-ai/icm
type MemoryMiddleware struct {
	// Store is the backing memory store used for queries and writes.
	Store MemoryStore
	// MaxEntries is the maximum number of memory entries to inject per request.
	// 0 means unlimited.
	MaxEntries int
	// MaxBytes is the maximum total byte size of injected memory text.
	// 0 means unlimited.
	MaxBytes int
	// ExtractFromResponse controls whether the middleware attempts to extract
	// and persist new memories from assistant responses.
	ExtractFromResponse bool
}

// Name returns "memory".
func (m *MemoryMiddleware) Name() string { return "memory" }

// ProcessRequest queries the MemoryStore and injects a <memory>…</memory>
// block at the front of the first system message (creating one if necessary).
func (m *MemoryMiddleware) ProcessRequest(req *proxy.AnthropicRequest) (*proxy.AnthropicRequest, error) {
	if m.Store == nil {
		return req, nil
	}

	// Build a query context from the last user message.
	queryCtx := lastUserText(req)
	entries, err := m.Store.Query(queryCtx)
	if err != nil {
		return nil, fmt.Errorf("memory store query: %w", err)
	}
	if len(entries) == 0 {
		return req, nil
	}

	// Respect MaxEntries limit.
	if m.MaxEntries > 0 && len(entries) > m.MaxEntries {
		entries = entries[:m.MaxEntries]
	}

	block := buildMemoryBlock(entries, m.MaxBytes)
	if block == "" {
		return req, nil
	}

	memoryDebugLogf("[memory] injecting %d entries (%d bytes)", len(entries), len(block))

	out := *req
	if len(out.System) == 0 {
		out.System = []proxy.AnthropicSystemMessage{
			{Type: "text", Text: block},
		}
	} else {
		updated := make([]proxy.AnthropicSystemMessage, len(out.System))
		copy(updated, out.System)
		updated[0] = proxy.AnthropicSystemMessage{
			Type: out.System[0].Type,
			Text: block + "\n\n" + out.System[0].Text,
		}
		out.System = updated
	}

	return &out, nil
}

// ProcessResponse optionally extracts new memories from assistant text and
// persists them into the MemoryStore. The response bytes are always returned
// unchanged.
func (m *MemoryMiddleware) ProcessResponse(resp []byte) ([]byte, error) {
	if m.Store == nil || !m.ExtractFromResponse {
		return resp, nil
	}

	text := string(resp)
	if extracted := extractMemoryBlocks(text); len(extracted) > 0 {
		for _, e := range extracted {
			if storeErr := m.Store.Store(e); storeErr != nil {
				// Log but do not fail the response pipeline.
				memoryDebugLogf("[memory] store error: %v", storeErr)
			}
		}
		memoryDebugLogf("[memory] stored %d extracted entries", len(extracted))
	}

	return resp, nil
}

// buildMemoryBlock formats the entries into a <memory>…</memory> XML block.
// If MaxBytes is > 0, entries are included until the byte budget is exhausted.
func buildMemoryBlock(entries []MemoryEntry, maxBytes int) string {
	var sb strings.Builder
	sb.WriteString("<memory>\n")
	used := len("<memory>\n") + len("</memory>")
	for _, e := range entries {
		line := fmt.Sprintf("- [%s] %s\n", e.Key, e.Value)
		if maxBytes > 0 && used+len(line) > maxBytes {
			break
		}
		sb.WriteString(line)
		used += len(line)
	}
	sb.WriteString("</memory>")
	result := sb.String()
	// Return empty if no entries were written.
	if result == "<memory>\n</memory>" {
		return ""
	}
	return result
}

// lastUserText extracts the text of the most recent user message for use as a
// memory query context. Returns an empty string if no user message is found.
func lastUserText(req *proxy.AnthropicRequest) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			return proxy.GetMessageContent(req.Messages[i].Content)
		}
	}
	return ""
}

// extractMemoryBlocks scans a response string for <remember key="…">…</remember>
// tags and converts them into MemoryEntry values. This is a lightweight
// heuristic extractor; a production system would use a proper XML parser.
func extractMemoryBlocks(text string) []MemoryEntry {
	var entries []MemoryEntry
	remaining := text
	for {
		start := strings.Index(remaining, "<remember")
		if start == -1 {
			break
		}
		end := strings.Index(remaining[start:], "</remember>")
		if end == -1 {
			break
		}
		tag := remaining[start : start+end+len("</remember>")]

		key := extractAttr(tag, "key")
		if key == "" {
			key = "memory"
		}
		inner := extractInner(tag, "remember")
		if inner != "" {
			entries = append(entries, MemoryEntry{
				Key:       key,
				Value:     strings.TrimSpace(inner),
				Timestamp: time.Now(),
				Score:     1.0,
			})
		}
		remaining = remaining[start+end+len("</remember>"):]
	}
	return entries
}

// extractAttr extracts attribute value from a simple XML-like tag string.
// The attribute must be in the form attr="value" where value contains no
// unescaped double-quotes. Returns an empty string if the attribute is not
// found or is malformed.
func extractAttr(tag, attr string) string {
	needle := attr + `="`
	idx := strings.Index(tag, needle)
	if idx == -1 {
		return ""
	}
	rest := tag[idx+len(needle):]
	end := strings.IndexByte(rest, '"')
	if end == -1 {
		return ""
	}
	value := rest[:end]
	// Reject values that contain characters that should be escaped in XML
	// attributes to prevent simple injection via malformed input.
	if strings.ContainsAny(value, "<>&") {
		return ""
	}
	return value
}

// extractInner extracts the inner text between opening and closing tags.
func extractInner(tag, element string) string {
	open := ">"
	close := "</" + element + ">"
	start := strings.Index(tag, open)
	if start == -1 {
		return ""
	}
	inner := tag[start+1:]
	end := strings.Index(inner, close)
	if end == -1 {
		return inner
	}
	return inner[:end]
}

// memoryDebugLogf mirrors the debugLogf pattern from proxy/request.go.
func memoryDebugLogf(format string, args ...any) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OPENKIRO_DEBUG"))) {
	case "1", "true", "yes", "on", "debug":
		fmt.Fprintf(os.Stderr, format+"\n", args...)
	}
}
