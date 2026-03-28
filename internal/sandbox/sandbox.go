// Package sandbox manages the lifecycle of ephemeral Docker containers used as
// isolated agent execution environments (sandbox microVMs).
//
// Each sandbox is a short-lived Docker container created with strict security
// constraints: non-root user (UID 1000), read-only root filesystem,
// no network access, and resource caps. A workspace directory on the host can
// be bind-mounted into /workspace for persistent file access.
//
// The Manager provides Create/Start/Stop/Destroy operations and an optional
// auto-heal loop that restarts failed containers and reaps idle ones.
package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

// Default configuration values.
const (
	DefaultImage       = "openkiro-sandbox:latest"
	DefaultNetworkMode = "none"
	DefaultUID         = "1000:1000"
	DefaultMemoryMB    = 512
	DefaultCPUPercent  = 50.0
	DefaultIdleTimeout = 30 * time.Minute
	HealInterval       = 30 * time.Second
)

// State represents the lifecycle state of a sandbox container.
type State int

const (
	StateCreating  State = iota // container is being created
	StateRunning                // container is running
	StateStopped                // container is stopped but not removed
	StateDestroyed              // container has been removed
	StateFailed                 // container entered an error state
)

// String returns a human-readable representation of the state.
func (s State) String() string {
	switch s {
	case StateCreating:
		return "creating"
	case StateRunning:
		return "running"
	case StateStopped:
		return "stopped"
	case StateDestroyed:
		return "destroyed"
	case StateFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// Config holds the configuration for a sandbox container.
type Config struct {
	// Image is the Docker image to run (default: openkiro-sandbox:latest).
	Image string
	// WorkspaceDir is the host path to bind-mount as /workspace inside the container.
	// An empty value means no workspace is mounted.
	WorkspaceDir string
	// ReadOnlyRoot makes the container root filesystem read-only.
	ReadOnlyRoot bool
	// NetworkMode controls container networking ("none" disables all networking).
	NetworkMode string
	// UID sets the container user and group in "uid:gid" format (e.g. "1000:1000").
	UID string
	// MemoryMB is the memory limit in mebibytes (0 means use DefaultMemoryMB).
	MemoryMB int
	// CPUPercent is the CPU usage cap as a percentage 0-100 (0 means DefaultCPUPercent).
	CPUPercent float64
	// IdleTimeout is the duration after which an idle running sandbox is destroyed.
	// 0 means use DefaultIdleTimeout.
	IdleTimeout time.Duration
	// Env contains additional KEY=VALUE environment variables for the container.
	Env []string
	// Labels are additional Docker labels applied to the container.
	Labels map[string]string
}

// DefaultConfig returns a Config with secure defaults.
func DefaultConfig() Config {
	return Config{
		Image:        DefaultImage,
		ReadOnlyRoot: true,
		NetworkMode:  DefaultNetworkMode,
		UID:          DefaultUID,
		MemoryMB:     DefaultMemoryMB,
		CPUPercent:   DefaultCPUPercent,
		IdleTimeout:  DefaultIdleTimeout,
	}
}

// Sandbox represents a single ephemeral isolated Docker container.
type Sandbox struct {
	mu             sync.RWMutex
	ID             string
	ContainerID    string
	Config         Config
	state          State
	CreatedAt      time.Time
	LastActivityAt time.Time
}

// State returns the current sandbox state (thread-safe).
func (s *Sandbox) State() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

// Touch records activity on the sandbox, resetting the idle timer.
func (s *Sandbox) Touch() {
	s.mu.Lock()
	s.LastActivityAt = time.Now()
	s.mu.Unlock()
}

// setState sets the internal state (caller must hold s.mu write lock).
func (s *Sandbox) setState(state State) {
	s.state = state
}

// DockerRunner abstracts Docker CLI execution for testability.
type DockerRunner interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

// cliRunner executes Docker commands via the host docker binary.
type cliRunner struct{}

func (r *cliRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("docker %s: %w: %s", args[0], err, strings.TrimSpace(errBuf.String()))
	}
	return out.Bytes(), nil
}

// Manager manages the lifecycle of multiple sandbox containers.
type Manager struct {
	mu        sync.RWMutex
	sandboxes map[string]*Sandbox
	docker    DockerRunner
}

// NewManager creates a Manager that uses the host Docker binary.
func NewManager() *Manager {
	return newManagerWithRunner(&cliRunner{})
}

// newManagerWithRunner creates a Manager with a custom DockerRunner (for testing).
func newManagerWithRunner(runner DockerRunner) *Manager {
	return &Manager{
		sandboxes: make(map[string]*Sandbox),
		docker:    runner,
	}
}

// Create creates a new sandbox container with the given configuration.
// The container is not started; call Start to run it.
func (m *Manager) Create(ctx context.Context, id string, cfg Config) (*Sandbox, error) {
	if cfg.Image == "" {
		cfg.Image = DefaultImage
	}
	if cfg.NetworkMode == "" {
		cfg.NetworkMode = DefaultNetworkMode
	}
	if cfg.UID == "" {
		cfg.UID = DefaultUID
	}
	if cfg.MemoryMB <= 0 {
		cfg.MemoryMB = DefaultMemoryMB
	}
	if cfg.CPUPercent <= 0 {
		cfg.CPUPercent = DefaultCPUPercent
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = DefaultIdleTimeout
	}

	m.mu.RLock()
	_, exists := m.sandboxes[id]
	m.mu.RUnlock()
	if exists {
		return nil, fmt.Errorf("sandbox %s: already exists", id)
	}

	args := buildCreateArgs(id, cfg)
	out, err := m.docker.Run(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("sandbox create %s: %w", id, err)
	}

	containerID := string(bytes.TrimSpace(out))
	sb := &Sandbox{
		ID:             id,
		ContainerID:    containerID,
		Config:         cfg,
		state:          StateCreating,
		CreatedAt:      time.Now(),
		LastActivityAt: time.Now(),
	}

	m.mu.Lock()
	m.sandboxes[id] = sb
	m.mu.Unlock()

	return sb, nil
}

// buildCreateArgs constructs the docker create argument list for a sandbox.
func buildCreateArgs(id string, cfg Config) []string {
	args := []string{
		"create",
		"--name", fmt.Sprintf("openkiro-sandbox-%s", id),
		"--user", cfg.UID,
		"--network", cfg.NetworkMode,
		"--memory", fmt.Sprintf("%dm", cfg.MemoryMB),
		"--cpus", fmt.Sprintf("%.2f", cfg.CPUPercent/100.0),
		"--label", "openkiro.sandbox=true",
		"--label", fmt.Sprintf("openkiro.sandbox.id=%s", id),
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
	}

	if cfg.ReadOnlyRoot {
		args = append(args, "--read-only")
	}

	if cfg.WorkspaceDir != "" {
		args = append(args, "--volume", fmt.Sprintf("%s:/workspace:rw", cfg.WorkspaceDir))
	}

	for _, env := range cfg.Env {
		args = append(args, "--env", env)
	}

	// Sort labels for deterministic output (aids testing).
	labelKeys := make([]string, 0, len(cfg.Labels))
	for k := range cfg.Labels {
		labelKeys = append(labelKeys, k)
	}
	sort.Strings(labelKeys)
	for _, k := range labelKeys {
		args = append(args, "--label", fmt.Sprintf("%s=%s", k, cfg.Labels[k]))
	}

	args = append(args, cfg.Image)
	return args
}

// Start starts an existing (created or stopped) sandbox container.
// If the sandbox is already running, Start is a no-op.
func (m *Manager) Start(ctx context.Context, id string) error {
	sb := m.get(id)
	if sb == nil {
		return fmt.Errorf("sandbox %s: not found", id)
	}

	sb.mu.Lock()
	defer sb.mu.Unlock()

	if sb.state == StateRunning {
		return nil
	}
	if sb.state == StateDestroyed {
		return fmt.Errorf("sandbox %s: already destroyed", id)
	}

	if _, err := m.docker.Run(ctx, "start", sb.ContainerID); err != nil {
		sb.setState(StateFailed)
		return fmt.Errorf("sandbox start %s: %w", id, err)
	}

	sb.setState(StateRunning)
	sb.LastActivityAt = time.Now()
	return nil
}

// Stop stops a running sandbox container without removing it.
// If the sandbox is already stopped or destroyed, Stop is a no-op.
func (m *Manager) Stop(ctx context.Context, id string) error {
	sb := m.get(id)
	if sb == nil {
		return fmt.Errorf("sandbox %s: not found", id)
	}

	sb.mu.Lock()
	defer sb.mu.Unlock()

	if sb.state == StateStopped || sb.state == StateDestroyed {
		return nil
	}

	if _, err := m.docker.Run(ctx, "stop", sb.ContainerID); err != nil {
		return fmt.Errorf("sandbox stop %s: %w", id, err)
	}

	sb.setState(StateStopped)
	return nil
}

// Destroy stops (if running) and removes a sandbox container.
func (m *Manager) Destroy(ctx context.Context, id string) error {
	sb := m.get(id)
	if sb == nil {
		return fmt.Errorf("sandbox %s: not found", id)
	}

	sb.mu.Lock()
	if sb.state == StateRunning {
		// Best-effort stop before removal; ignore errors.
		_, _ = m.docker.Run(ctx, "stop", sb.ContainerID)
	}
	sb.setState(StateDestroyed)
	containerID := sb.ContainerID
	sb.mu.Unlock()

	if _, err := m.docker.Run(ctx, "rm", "--force", containerID); err != nil {
		return fmt.Errorf("sandbox destroy %s: %w", id, err)
	}

	m.mu.Lock()
	delete(m.sandboxes, id)
	m.mu.Unlock()

	return nil
}

// Status returns the current State of a sandbox.
func (m *Manager) Status(id string) (State, error) {
	sb := m.get(id)
	if sb == nil {
		return StateFailed, fmt.Errorf("sandbox %s: not found", id)
	}
	return sb.State(), nil
}

// Inspect returns the raw docker inspect JSON for a sandbox container.
func (m *Manager) Inspect(ctx context.Context, id string) (map[string]any, error) {
	sb := m.get(id)
	if sb == nil {
		return nil, fmt.Errorf("sandbox %s: not found", id)
	}

	out, err := m.docker.Run(ctx, "inspect", "--format", "{{json .}}", sb.ContainerID)
	if err != nil {
		return nil, fmt.Errorf("sandbox inspect %s: %w", id, err)
	}

	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("sandbox inspect %s: parse: %w", id, err)
	}
	return result, nil
}

// List returns snapshots of all currently tracked sandboxes.
func (m *Manager) List() []SandboxInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]SandboxInfo, 0, len(m.sandboxes))
	for _, sb := range m.sandboxes {
		sb.mu.RLock()
		result = append(result, SandboxInfo{
			ID:             sb.ID,
			ContainerID:    sb.ContainerID,
			State:          sb.state,
			Image:          sb.Config.Image,
			CreatedAt:      sb.CreatedAt,
			LastActivityAt: sb.LastActivityAt,
		})
		sb.mu.RUnlock()
	}
	return result
}

// SandboxInfo is a point-in-time snapshot of a sandbox (no locking required).
type SandboxInfo struct {
	ID             string
	ContainerID    string
	State          State
	Image          string
	CreatedAt      time.Time
	LastActivityAt time.Time
}

// DestroyAll stops and removes all tracked sandbox containers.
func (m *Manager) DestroyAll(ctx context.Context) error {
	m.mu.RLock()
	ids := make([]string, 0, len(m.sandboxes))
	for id := range m.sandboxes {
		ids = append(ids, id)
	}
	m.mu.RUnlock()

	var errs []string
	for _, id := range ids {
		if err := m.Destroy(ctx, id); err != nil {
			errs = append(errs, err.Error())
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("destroy all: %s", strings.Join(errs, "; "))
	}
	return nil
}

// StartAutoHeal starts a background goroutine that periodically checks all
// sandboxes: failed ones are restarted, idle ones are destroyed.
// It runs until ctx is cancelled.
func (m *Manager) StartAutoHeal(ctx context.Context) {
	ticker := time.NewTicker(HealInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.healAll(ctx)
		}
	}
}

// healAll iterates all sandboxes and takes corrective action where needed.
func (m *Manager) healAll(ctx context.Context) {
	m.mu.RLock()
	ids := make([]string, 0, len(m.sandboxes))
	for id := range m.sandboxes {
		ids = append(ids, id)
	}
	m.mu.RUnlock()

	for _, id := range ids {
		sb := m.get(id)
		if sb == nil {
			continue
		}

		sb.mu.RLock()
		state := sb.state
		idleTimeout := sb.Config.IdleTimeout
		lastActivity := sb.LastActivityAt
		sb.mu.RUnlock()

		switch state {
		case StateFailed:
			_ = m.Start(ctx, id)
		case StateRunning:
			if idleTimeout > 0 && time.Since(lastActivity) > idleTimeout {
				_ = m.Destroy(ctx, id)
			}
		}
	}
}

// get retrieves a sandbox by ID (thread-safe).
func (m *Manager) get(id string) *Sandbox {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sandboxes[id]
}
