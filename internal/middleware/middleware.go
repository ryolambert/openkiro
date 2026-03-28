// Package middleware defines the Middleware interface and Chain used to
// intercept and transform requests and responses in the openkiro proxy.
//
// All future proxy features — compression, memory injection, tool optimisation
// — must implement the Middleware interface so they can be composed together
// via a Chain without modifying server.go.
package middleware

import (
	"fmt"

	"github.com/ryolambert/openkiro/internal/proxy"
)

// Middleware is the contract every proxy middleware must satisfy.
//
// ProcessRequest is called before the request is forwarded to CodeWhisperer.
// ProcessResponse is called on the raw bytes returned by CodeWhisperer before
// they are sent back to the client.
// Name returns a short human-readable identifier used in logs and metrics.
type Middleware interface {
	ProcessRequest(req *proxy.AnthropicRequest) (*proxy.AnthropicRequest, error)
	ProcessResponse(resp []byte) ([]byte, error)
	Name() string
}

// Chain executes an ordered sequence of Middleware instances.
// Middlewares are applied in the order they were added.
type Chain struct {
	middlewares []Middleware
}

// Add appends m to the end of the chain.
func (c *Chain) Add(m Middleware) {
	c.middlewares = append(c.middlewares, m)
}

// ProcessRequest passes req through every middleware in order.
// The output of each middleware becomes the input of the next.
// If any middleware returns an error the chain stops immediately.
func (c *Chain) ProcessRequest(req *proxy.AnthropicRequest) (*proxy.AnthropicRequest, error) {
	current := req
	for _, m := range c.middlewares {
		next, err := m.ProcessRequest(current)
		if err != nil {
			return nil, fmt.Errorf("middleware %q ProcessRequest: %w", m.Name(), err)
		}
		current = next
	}
	return current, nil
}

// ProcessResponse passes resp through every middleware in order.
// The output of each middleware becomes the input of the next.
// If any middleware returns an error the chain stops immediately.
func (c *Chain) ProcessResponse(resp []byte) ([]byte, error) {
	current := resp
	for _, m := range c.middlewares {
		next, err := m.ProcessResponse(current)
		if err != nil {
			return nil, fmt.Errorf("middleware %q ProcessResponse: %w", m.Name(), err)
		}
		current = next
	}
	return current, nil
}

// NoopMiddleware is a pass-through middleware that leaves requests and
// responses unmodified. It is useful as a starting point when writing new
// middlewares and as a safe default in tests.
type NoopMiddleware struct{}

// ProcessRequest returns req unchanged.
func (n *NoopMiddleware) ProcessRequest(req *proxy.AnthropicRequest) (*proxy.AnthropicRequest, error) {
	return req, nil
}

// ProcessResponse returns resp unchanged.
func (n *NoopMiddleware) ProcessResponse(resp []byte) ([]byte, error) {
	return resp, nil
}

// Name returns "noop".
func (n *NoopMiddleware) Name() string { return "noop" }
