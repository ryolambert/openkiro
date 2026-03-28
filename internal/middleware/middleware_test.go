package middleware_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ryolambert/openkiro/internal/middleware"
	"github.com/ryolambert/openkiro/internal/proxy"
)

// ── RED: failing test written before implementation ─────────────────────────
//
// This section intentionally documents what we expect from a no-op middleware
// before any implementation exists. In a pure TDD flow you would first write
// these tests, see them fail ("red"), then create middleware.go to make them
// pass ("green"), and finally refactor.

// ── GREEN: NoopMiddleware passes requests and responses through unchanged ────

func TestNoopMiddleware_ProcessRequest_returnsRequestUnchanged(t *testing.T) {
	m := &middleware.NoopMiddleware{}

	req := &proxy.AnthropicRequest{Model: "claude-sonnet-4-5", MaxTokens: 1024}
	got, err := m.ProcessRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != req {
		t.Error("expected ProcessRequest to return the same pointer")
	}
	if got.Model != "claude-sonnet-4-5" {
		t.Errorf("expected model %q, got %q", "claude-sonnet-4-5", got.Model)
	}
}

func TestNoopMiddleware_ProcessResponse_returnsBytesUnchanged(t *testing.T) {
	m := &middleware.NoopMiddleware{}

	resp := []byte(`{"type":"message"}`)
	got, err := m.ProcessResponse(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != string(resp) {
		t.Errorf("expected %q, got %q", resp, got)
	}
}

func TestNoopMiddleware_Name(t *testing.T) {
	m := &middleware.NoopMiddleware{}
	if m.Name() != "noop" {
		t.Errorf("expected name %q, got %q", "noop", m.Name())
	}
}

// ── REFACTOR: table-driven tests for Chain ───────────────────────────────────

func TestChain_ProcessRequest_tabledriven(t *testing.T) {
	tests := []struct {
		name        string
		middlewares []middleware.Middleware
		input       *proxy.AnthropicRequest
		wantModel   string
		wantErr     bool
	}{
		{
			name:        "empty chain returns request unchanged",
			middlewares: nil,
			input:       &proxy.AnthropicRequest{Model: "haiku"},
			wantModel:   "haiku",
		},
		{
			name:        "single noop preserves request",
			middlewares: []middleware.Middleware{&middleware.NoopMiddleware{}},
			input:       &proxy.AnthropicRequest{Model: "sonnet"},
			wantModel:   "sonnet",
		},
		{
			name: "mutating middleware changes model",
			middlewares: []middleware.Middleware{
				&modelOverrideMiddleware{override: "opus"},
			},
			input:     &proxy.AnthropicRequest{Model: "haiku"},
			wantModel: "opus",
		},
		{
			name: "error middleware propagates error",
			middlewares: []middleware.Middleware{
				&alwaysErrorMiddleware{msg: "boom"},
			},
			input:   &proxy.AnthropicRequest{Model: "haiku"},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var chain middleware.Chain
			for _, m := range tc.middlewares {
				chain.Add(m)
			}

			got, err := chain.ProcessRequest(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Model != tc.wantModel {
				t.Errorf("expected model %q, got %q", tc.wantModel, got.Model)
			}
		})
	}
}

func TestChain_ProcessResponse_tabledriven(t *testing.T) {
	tests := []struct {
		name        string
		middlewares []middleware.Middleware
		input       []byte
		want        string
		wantErr     bool
	}{
		{
			name:  "empty chain returns bytes unchanged",
			input: []byte(`{"ok":true}`),
			want:  `{"ok":true}`,
		},
		{
			name:        "single noop preserves bytes",
			middlewares: []middleware.Middleware{&middleware.NoopMiddleware{}},
			input:       []byte(`hello`),
			want:        `hello`,
		},
		{
			name: "transform middleware changes bytes",
			middlewares: []middleware.Middleware{
				&appendSuffixMiddleware{suffix: "!"},
			},
			input: []byte(`hello`),
			want:  `hello!`,
		},
		{
			name: "error middleware propagates error",
			middlewares: []middleware.Middleware{
				&alwaysErrorMiddleware{msg: "response error"},
			},
			input:   []byte(`anything`),
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var chain middleware.Chain
			for _, m := range tc.middlewares {
				chain.Add(m)
			}

			got, err := chain.ProcessResponse(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestChain_ErrorWrapsMiddlewareName(t *testing.T) {
	var chain middleware.Chain
	chain.Add(&alwaysErrorMiddleware{msg: "sentinel"})

	_, err := chain.ProcessRequest(&proxy.AnthropicRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "always-error") {
		t.Errorf("expected error to mention middleware name, got: %v", err)
	}
	if !strings.Contains(err.Error(), "sentinel") {
		t.Errorf("expected error to contain original message, got: %v", err)
	}
}

// ── helpers: test-only middleware implementations ────────────────────────────

// modelOverrideMiddleware replaces the request model with a fixed value.
type modelOverrideMiddleware struct {
	override string
}

func (m *modelOverrideMiddleware) ProcessRequest(req *proxy.AnthropicRequest) (*proxy.AnthropicRequest, error) {
	out := *req
	out.Model = m.override
	return &out, nil
}
func (m *modelOverrideMiddleware) ProcessResponse(resp []byte) ([]byte, error) { return resp, nil }
func (m *modelOverrideMiddleware) Name() string                                { return "model-override" }

// appendSuffixMiddleware appends a suffix to response bytes.
type appendSuffixMiddleware struct {
	suffix string
}

func (a *appendSuffixMiddleware) ProcessRequest(req *proxy.AnthropicRequest) (*proxy.AnthropicRequest, error) {
	return req, nil
}
func (a *appendSuffixMiddleware) ProcessResponse(resp []byte) ([]byte, error) {
	return append(resp, []byte(a.suffix)...), nil
}
func (a *appendSuffixMiddleware) Name() string { return "append-suffix" }

// alwaysErrorMiddleware returns an error on every call.
type alwaysErrorMiddleware struct {
	msg string
}

func (e *alwaysErrorMiddleware) ProcessRequest(_ *proxy.AnthropicRequest) (*proxy.AnthropicRequest, error) {
	return nil, errors.New(e.msg)
}
func (e *alwaysErrorMiddleware) ProcessResponse(_ []byte) ([]byte, error) {
	return nil, errors.New(e.msg)
}
func (e *alwaysErrorMiddleware) Name() string { return "always-error" }
