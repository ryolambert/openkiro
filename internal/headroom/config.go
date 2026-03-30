// Package headroom provides lifecycle management and an HTTP client for the
// headroom-ai Python compression proxy (https://github.com/chopratejas/headroom).
//
// The headroom proxy is installed via pip and run as a sidecar process alongside
// the openkiro Go proxy. Requests are compressed via headroom's /v1/compress
// endpoint before being forwarded to AWS CodeWhisperer.
package headroom

import (
	"fmt"
	"time"
)

// Config holds the settings needed to install, start, and communicate with
// the headroom Python proxy.
type Config struct {
	// Host the headroom proxy binds to (default "127.0.0.1").
	Host string
	// Port the headroom proxy listens on (default "8787").
	Port string
	// PipPackage is the pip package specifier to install (default "headroom-ai[proxy]").
	PipPackage string
	// PythonBin is the name or path of the Python interpreter (default "python3").
	PythonBin string
	// HealthTimeout is how long to wait for headroom to become healthy after start.
	HealthTimeout time.Duration
	// CompressTimeout is the HTTP timeout for each /v1/compress call.
	CompressTimeout time.Duration
	// ExtraArgs are additional CLI arguments passed to `headroom proxy`.
	ExtraArgs []string
	// Enabled controls whether the headroom middleware is active. When false
	// the middleware is a no-op passthrough.
	Enabled bool
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		Host:            "127.0.0.1",
		Port:            "8787",
		PipPackage:      "headroom-ai[proxy]",
		PythonBin:       "python3",
		HealthTimeout:   30 * time.Second,
		CompressTimeout: 10 * time.Second,
		Enabled:         true,
	}
}

// BaseURL returns the base URL of the headroom proxy (e.g. "http://127.0.0.1:8787").
func (c Config) BaseURL() string {
	return fmt.Sprintf("http://%s:%s", c.Host, c.Port)
}
