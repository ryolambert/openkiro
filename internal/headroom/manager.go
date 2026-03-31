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
	done    chan struct{} // closed when the process exits
}

// NewManager creates a Manager with the given configuration.
func NewManager(cfg Config) *Manager {
	return &Manager{cfg: cfg}
}

// Install installs the headroom-ai Python package using pip via the configured
// Python interpreter (`cfg.PythonBin -m pip install`). This ensures the package
// is installed into the correct interpreter environment.
func (m *Manager) Install(ctx context.Context) error {
	pythonBin := m.pythonBin()
	//nolint:gosec // pythonBin and cfg.PipPackage are developer-configured, not user input.
	cmd := exec.CommandContext(ctx, pythonBin, "-m", "pip", "install", m.cfg.PipPackage)
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
	m.done = make(chan struct{})

	// Monitor the process in the background and flip running=false on exit.
	go func() {
		_ = cmd.Wait()
		m.mu.Lock()
		m.running = false
		m.mu.Unlock()
		close(m.done)
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

// Stop gracefully stops the headroom proxy process and waits for it to exit.
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
	// Wait for the process to exit (with a timeout) before flipping the flag.
	// We must release the mutex before waiting, because the background
	// goroutine from Start() needs it to set running=false and close done.
	done := m.done
	m.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = m.cmd.Process.Kill()
		}
	}
	m.mu.Lock()
	m.running = false
	return nil
}

// Running returns whether the headroom proxy process is currently running.
func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// waitHealthy polls /health until success or timeout. Each individual health
// check uses a short per-request timeout (1s or remaining deadline, whichever
// is smaller) so a hanging connection cannot block past HealthTimeout.
func (m *Manager) waitHealthy(ctx context.Context) error {
	deadline := time.Now().Add(m.cfg.HealthTimeout)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	const healthCheckTimeout = 1 * time.Second

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			remaining := time.Until(deadline)
			if remaining <= 0 {
				return fmt.Errorf("headroom: proxy did not become healthy within %v", m.cfg.HealthTimeout)
			}
			// Use the shorter of healthCheckTimeout and remaining.
			perCall := healthCheckTimeout
			if remaining < perCall {
				perCall = remaining
			}
			callCtx, callCancel := context.WithTimeout(ctx, perCall)
			healthy := NewClient(m.cfg).Healthy(callCtx)
			callCancel()
			if healthy {
				return nil
			}
		}
	}
}

// pythonBin returns the Python interpreter to use. It prefers cfg.PythonBin
// and falls back to "python" on non-Windows systems if needed.
func (m *Manager) pythonBin() string {
	if _, err := exec.LookPath(m.cfg.PythonBin); err == nil {
		return m.cfg.PythonBin
	}
	if runtime.GOOS != "windows" {
		if _, err := exec.LookPath("python"); err == nil {
			return "python"
		}
	}
	// Return configured bin even if not found — exec will produce a clear error.
	return m.cfg.PythonBin
}
