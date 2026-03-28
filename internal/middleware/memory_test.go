package middleware_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ryolambert/openkiro/internal/middleware"
	"github.com/ryolambert/openkiro/internal/proxy"
)

// ── RED: expectations written before implementation ──────────────────────────

func TestMemoryMiddleware_Name(t *testing.T) {
	m := &middleware.MemoryMiddleware{Store: &middleware.InMemoryStore{}}
	if got := m.Name(); got != "memory" {
		t.Errorf("expected %q, got %q", "memory", got)
	}
}

// ── GREEN: InMemoryStore stores and retrieves entries ────────────────────────

func TestInMemoryStore_StoreAndQuery(t *testing.T) {
	s := &middleware.InMemoryStore{}

	entry := middleware.MemoryEntry{
		Key:       "fact",
		Value:     "Go is great",
		Timestamp: time.Now(),
		Score:     0.9,
	}
	if err := s.Store(entry); err != nil {
		t.Fatalf("unexpected store error: %v", err)
	}

	entries, err := s.Query("anything")
	if err != nil {
		t.Fatalf("unexpected query error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Key != "fact" || entries[0].Value != "Go is great" {
		t.Errorf("unexpected entry: %+v", entries[0])
	}
}

func TestInMemoryStore_QueryEmpty(t *testing.T) {
	s := &middleware.InMemoryStore{}
	entries, err := s.Query("nothing here")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

// ── REFACTOR: MemoryMiddleware injection tests ───────────────────────────────

func TestMemoryMiddleware_InjectsMemoryBlock_prependsToFirstSystem(t *testing.T) {
	s := &middleware.InMemoryStore{}
	_ = s.Store(middleware.MemoryEntry{Key: "tip", Value: "always test first"})

	m := &middleware.MemoryMiddleware{Store: s}
	req := &proxy.AnthropicRequest{
		System:   []proxy.AnthropicSystemMessage{{Type: "text", Text: "original system"}},
		Messages: []proxy.AnthropicRequestMessage{{Role: "user", Content: "hi"}},
	}
	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.System) == 0 {
		t.Fatal("expected system messages to be present")
	}
	text := got.System[0].Text
	if !strings.Contains(text, "<memory>") {
		t.Errorf("expected <memory> block, got: %q", text)
	}
	if !strings.Contains(text, "always test first") {
		t.Errorf("expected memory value in system prompt, got: %q", text)
	}
	if !strings.Contains(text, "original system") {
		t.Errorf("expected original system text preserved, got: %q", text)
	}
}

func TestMemoryMiddleware_CreatesSystemMessage_whenNone(t *testing.T) {
	s := &middleware.InMemoryStore{}
	_ = s.Store(middleware.MemoryEntry{Key: "k", Value: "v"})

	m := &middleware.MemoryMiddleware{Store: s}
	req := &proxy.AnthropicRequest{
		Messages: []proxy.AnthropicRequestMessage{{Role: "user", Content: "hello"}},
	}
	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.System) == 0 {
		t.Fatal("expected a system message to be created")
	}
	if !strings.Contains(got.System[0].Text, "<memory>") {
		t.Errorf("expected memory block in new system message, got: %q", got.System[0].Text)
	}
}

func TestMemoryMiddleware_NilStore_passThrough(t *testing.T) {
	m := &middleware.MemoryMiddleware{Store: nil}
	req := &proxy.AnthropicRequest{
		Messages: []proxy.AnthropicRequestMessage{{Role: "user", Content: "hello"}},
	}
	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != req {
		t.Error("expected same pointer when Store is nil")
	}
}

func TestMemoryMiddleware_MaxEntries_limitsInjection(t *testing.T) {
	s := &middleware.InMemoryStore{}
	for i := 0; i < 5; i++ {
		_ = s.Store(middleware.MemoryEntry{Key: "k", Value: "entry"})
	}

	m := &middleware.MemoryMiddleware{Store: s, MaxEntries: 2}
	req := &proxy.AnthropicRequest{
		Messages: []proxy.AnthropicRequestMessage{{Role: "user", Content: "hello"}},
	}
	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only 2 entries should be present in the injected block.
	text := got.System[0].Text
	count := strings.Count(text, "- [k]")
	if count != 2 {
		t.Errorf("expected 2 memory lines, got %d in %q", count, text)
	}
}

func TestMemoryMiddleware_MaxBytes_limitsInjection(t *testing.T) {
	s := &middleware.InMemoryStore{}
	// Each entry value is 20 chars.
	for i := 0; i < 10; i++ {
		_ = s.Store(middleware.MemoryEntry{Key: "k", Value: "12345678901234567890"})
	}

	// Set a very small byte budget that can only fit one or two entries.
	m := &middleware.MemoryMiddleware{Store: s, MaxBytes: 60}
	req := &proxy.AnthropicRequest{
		Messages: []proxy.AnthropicRequestMessage{{Role: "user", Content: "hello"}},
	}
	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := got.System[0].Text
	// Should have fewer than 10 entries.
	count := strings.Count(text, "- [k]")
	if count >= 10 {
		t.Errorf("expected fewer than 10 entries with MaxBytes=60, got %d", count)
	}
}

func TestMemoryMiddleware_EmptyStore_noInjection(t *testing.T) {
	s := &middleware.InMemoryStore{}
	m := &middleware.MemoryMiddleware{Store: s}
	req := &proxy.AnthropicRequest{
		System:   []proxy.AnthropicSystemMessage{{Type: "text", Text: "system"}},
		Messages: []proxy.AnthropicRequestMessage{{Role: "user", Content: "hello"}},
	}
	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// System should be unmodified.
	if got.System[0].Text != "system" {
		t.Errorf("expected unchanged system text, got %q", got.System[0].Text)
	}
}

func TestMemoryMiddleware_ProcessResponse_passThrough(t *testing.T) {
	m := &middleware.MemoryMiddleware{Store: &middleware.InMemoryStore{}}
	resp := []byte(`{"type":"message"}`)
	got, err := m.ProcessResponse(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != string(resp) {
		t.Errorf("expected pass-through, got %q", got)
	}
}

func TestMemoryMiddleware_ExtractFromResponse_storesMemory(t *testing.T) {
	s := &middleware.InMemoryStore{}
	m := &middleware.MemoryMiddleware{Store: s, ExtractFromResponse: true}

	resp := []byte(`Here is a fact. <remember key="lang">Go is compiled</remember> End.`)
	_, err := m.ProcessResponse(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entries, err := s.Query("")
	if err != nil {
		t.Fatalf("unexpected query error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 stored entry, got %d", len(entries))
	}
	if entries[0].Key != "lang" || entries[0].Value != "Go is compiled" {
		t.Errorf("unexpected stored entry: %+v", entries[0])
	}
}

func TestMemoryMiddleware_ExtractFromResponse_disabled_doesNotStore(t *testing.T) {
	s := &middleware.InMemoryStore{}
	m := &middleware.MemoryMiddleware{Store: s, ExtractFromResponse: false}

	resp := []byte(`<remember key="lang">Go is compiled</remember>`)
	_, err := m.ProcessResponse(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entries, err := s.Query("")
	if err != nil {
		t.Fatalf("unexpected query error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected no stored entries when disabled, got %d", len(entries))
	}
}
