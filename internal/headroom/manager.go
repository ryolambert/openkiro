package headroom

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

// Manager handles the lifecycle of the headroom Python proxy process.
type Manager struct {
	cfg Config

	mu      sync.Mutex
	cmd     *exec.Cmd
	running bool
}

// NewManager creates a Manager with the given configuration.
func NewManager(cfg Config) *Manager {
	return &Manager{cfg: cfg}
}

// Install installs the headroom-ai Python package using pip. It returns an
// error if Python or pip is not available.
func (m *Manager) Install(ctx context.Context) error {
	pip := m.pipCommand()
	//nolint:gosec // pip and cfg.PipPackage are developer-configured, not user input.
	cmd := exec.CommandContext(ctx, pip, "install", m.cfg.PipPackage)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("headroom: pip install failed: %w", err)
	}
	return nil
}

// Installed returns true if the `headroom` CLI is available on PATH.
func (m *Manager) Installed() bool {
	_, err := exec.LookPath("headroom")
	return err == nil
}

// PythonAvailable returns true if the configured Python interpreter is on PATH.
func (m *Manager) PythonAvailable() bool {
	_, err := exec.LookPath(m.cfg.PythonBin)
	if err != nil && runtime.GOOS != "windows" {
		// Fallback: try "python" if "python3" wasn't found.
		_, err = exec.LookPath("python")
	}
	return err == nil
}

// Start launches the headroom proxy as a background process and waits for it
// to become healthy. If already running, Start is a no-op.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return nil
	}

	headroomBin, err := exec.LookPath("headroom")
	if err != nil {
		return fmt.Errorf("headroom: binary not found on PATH — install with: pip install %q", m.cfg.PipPackage)
	}

	args := []string{"proxy", "--host", m.cfg.Host, "--port", m.cfg.Port}
	args = append(args, m.cfg.ExtraArgs...)

	//nolint:gosec // headroomBin resolved from LookPath, args from developer config.
	cmd := exec.CommandContext(ctx, headroomBin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("headroom: failed to start proxy: %w", err)
	}

	m.cmd = cmd
	m.running = true

	// Wait for healthcheck in a separate goroutine-friendly manner.
	go func() {
		_ = cmd.Wait()
		m.mu.Lock()
		m.running = false
		m.mu.Unlock()
	}()

	if err := m.waitHealthy(ctx); err != nil {
		if stopErr := m.stopLocked(); stopErr != nil {
			log.Printf("headroom: failed to stop after health-check failure: %v", stopErr)
		}
		return err
	}

	log.Printf("headroom proxy started on %s", m.cfg.BaseURL())
	return nil
}

// Stop gracefully stops the headroom proxy process.
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopLocked()
}

func (m *Manager) stopLocked() error {
	if !m.running || m.cmd == nil || m.cmd.Process == nil {
		return nil
	}
	if err := m.cmd.Process.Signal(os.Interrupt); err != nil {
		// If interrupt fails (e.g. Windows), kill forcefully.
		if killErr := m.cmd.Process.Kill(); killErr != nil {
			log.Printf("headroom: failed to kill process: %v (interrupt error: %v)", killErr, err)
		}
	}
	m.running = false
	return nil
}

// Running returns whether the headroom proxy process is currently running.
func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// waitHealthy polls /health until success or timeout.
func (m *Manager) waitHealthy(ctx context.Context) error {
	client := NewClient(m.cfg)

	deadline := time.Now().Add(m.cfg.HealthTimeout)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("headroom: proxy did not become healthy within %v", m.cfg.HealthTimeout)
			}
			if client.Healthy(ctx) {
				return nil
			}
		}
	}
}

// pipCommand returns the pip binary name appropriate for the platform.
func (m *Manager) pipCommand() string {
	if runtime.GOOS == "windows" {
		return "pip"
	}
	// Prefer pip3 on Unix; fall back to pip.
	if _, err := exec.LookPath("pip3"); err == nil {
		return "pip3"
	}
	return "pip"
}
