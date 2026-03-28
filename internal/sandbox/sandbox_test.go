package sandbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// fakeRunner implements DockerRunner for unit tests.
// It records every call made to Run and returns pre-configured outputs or errors.
type fakeRunner struct {
	calls  [][]string          // all recorded argument slices
	output map[string][]byte   // per-subcommand stdout
	errs   map[string]error    // per-subcommand errors
	hook   func(args []string) // optional callback invoked on every call
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{
		output: make(map[string][]byte),
		errs:   make(map[string]error),
	}
}

func (f *fakeRunner) setOutput(subcmd string, out []byte) {
	f.output[subcmd] = out
}

func (f *fakeRunner) setError(subcmd string, err error) {
	f.errs[subcmd] = err
}

func (f *fakeRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	if f.hook != nil {
		f.hook(args)
	}
	subcmd := ""
	if len(args) > 0 {
		subcmd = args[0]
	}
	if err, ok := f.errs[subcmd]; ok {
		return nil, err
	}
	if out, ok := f.output[subcmd]; ok {
		return out, nil
	}
	// Default: return a fake container ID for "create", empty for others.
	if subcmd == "create" {
		return []byte("fake-container-id"), nil
	}
	return []byte{}, nil
}

func (f *fakeRunner) callsForSubcmd(subcmd string) [][]string {
	var result [][]string
	for _, call := range f.calls {
		if len(call) > 0 && call[0] == subcmd {
			result = append(result, call)
		}
	}
	return result
}

// ---- DefaultConfig tests ----

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Image != DefaultImage {
		t.Errorf("Image = %q, want %q", cfg.Image, DefaultImage)
	}
	if !cfg.ReadOnlyRoot {
		t.Error("ReadOnlyRoot should be true")
	}
	if cfg.NetworkMode != DefaultNetworkMode {
		t.Errorf("NetworkMode = %q, want %q", cfg.NetworkMode, DefaultNetworkMode)
	}
	if cfg.UID != DefaultUID {
		t.Errorf("UID = %q, want %q", cfg.UID, DefaultUID)
	}
	if cfg.MemoryMB != DefaultMemoryMB {
		t.Errorf("MemoryMB = %d, want %d", cfg.MemoryMB, DefaultMemoryMB)
	}
	if cfg.CPUPercent != DefaultCPUPercent {
		t.Errorf("CPUPercent = %f, want %f", cfg.CPUPercent, DefaultCPUPercent)
	}
	if cfg.IdleTimeout != DefaultIdleTimeout {
		t.Errorf("IdleTimeout = %v, want %v", cfg.IdleTimeout, DefaultIdleTimeout)
	}
}

// ---- State.String tests ----

func TestStateString(t *testing.T) {
	cases := []struct {
		state State
		want  string
	}{
		{StateCreating, "creating"},
		{StateRunning, "running"},
		{StateStopped, "stopped"},
		{StateDestroyed, "destroyed"},
		{StateFailed, "failed"},
		{State(99), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.state.String(); got != tc.want {
			t.Errorf("State(%d).String() = %q, want %q", tc.state, got, tc.want)
		}
	}
}

// ---- Manager.Create tests ----

func TestCreate_Success(t *testing.T) {
	runner := newFakeRunner()
	runner.setOutput("create", []byte("abc123\n"))
	mgr := newManagerWithRunner(runner)

	cfg := DefaultConfig()
	cfg.WorkspaceDir = "/tmp/ws"

	sb, err := mgr.Create(context.Background(), "test-1", cfg)
	if err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}
	if sb.ID != "test-1" {
		t.Errorf("ID = %q, want %q", sb.ID, "test-1")
	}
	if sb.ContainerID != "abc123" {
		t.Errorf("ContainerID = %q, want %q", sb.ContainerID, "abc123")
	}
	if sb.State() != StateCreating {
		t.Errorf("State = %v, want %v", sb.State(), StateCreating)
	}

	// Verify docker create was called exactly once.
	creates := runner.callsForSubcmd("create")
	if len(creates) != 1 {
		t.Fatalf("docker create called %d times, want 1", len(creates))
	}

	args := creates[0]
	assertContainsArg(t, args, "--user", DefaultUID)
	assertContainsArg(t, args, "--network", DefaultNetworkMode)
	assertContainsFlag(t, args, "--read-only")
	assertContainsArg(t, args, "--security-opt", "no-new-privileges")
	assertContainsArg(t, args, "--cap-drop", "ALL")

	// Verify workspace bind-mount.
	assertContainsArg(t, args, "--volume", "/tmp/ws:/workspace:rw")

	// Verify sandbox label.
	assertContainsArg(t, args, "--label", "openkiro.sandbox=true")
	assertContainsArg(t, args, "--label", "openkiro.sandbox.id=test-1")
}

func TestCreate_AppliesDefaultsWhenZero(t *testing.T) {
	runner := newFakeRunner()
	mgr := newManagerWithRunner(runner)

	// Pass an empty config — all fields should be filled with defaults.
	sb, err := mgr.Create(context.Background(), "defaults-test", Config{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sb.Config.Image != DefaultImage {
		t.Errorf("Image = %q, want %q", sb.Config.Image, DefaultImage)
	}
	if sb.Config.NetworkMode != DefaultNetworkMode {
		t.Errorf("NetworkMode = %q, want %q", sb.Config.NetworkMode, DefaultNetworkMode)
	}
	if sb.Config.UID != DefaultUID {
		t.Errorf("UID = %q, want %q", sb.Config.UID, DefaultUID)
	}
	if sb.Config.MemoryMB != DefaultMemoryMB {
		t.Errorf("MemoryMB = %d, want %d", sb.Config.MemoryMB, DefaultMemoryMB)
	}
	if sb.Config.CPUPercent != DefaultCPUPercent {
		t.Errorf("CPUPercent = %f, want %f", sb.Config.CPUPercent, DefaultCPUPercent)
	}
}

func TestCreate_DuplicateID(t *testing.T) {
	runner := newFakeRunner()
	mgr := newManagerWithRunner(runner)

	_, err := mgr.Create(context.Background(), "dup", DefaultConfig())
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err = mgr.Create(context.Background(), "dup", DefaultConfig())
	if err == nil {
		t.Fatal("second Create with same ID should return an error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %q, want 'already exists'", err.Error())
	}
}

func TestCreate_DockerError(t *testing.T) {
	runner := newFakeRunner()
	runner.setError("create", errors.New("image not found"))
	mgr := newManagerWithRunner(runner)

	_, err := mgr.Create(context.Background(), "fail", DefaultConfig())
	if err == nil {
		t.Fatal("expected error from docker create failure")
	}
}

// ---- buildCreateArgs security constraint tests ----

func TestBuildCreateArgs_SecurityConstraints(t *testing.T) {
	cfg := Config{
		Image:        "my-image:latest",
		UID:          "1000:1000",
		NetworkMode:  "none",
		MemoryMB:     256,
		CPUPercent:   25.0,
		ReadOnlyRoot: true,
	}
	args := buildCreateArgs("sec-test", cfg)

	assertContainsFlag(t, args, "--read-only")
	assertContainsArg(t, args, "--cap-drop", "ALL")
	assertContainsArg(t, args, "--security-opt", "no-new-privileges")
	assertContainsArg(t, args, "--network", "none")
	assertContainsArg(t, args, "--user", "1000:1000")
}

func TestBuildCreateArgs_NoReadOnly(t *testing.T) {
	cfg := Config{
		Image:        "my-image",
		UID:          "0:0",
		NetworkMode:  "bridge",
		MemoryMB:     128,
		CPUPercent:   10.0,
		ReadOnlyRoot: false,
	}
	args := buildCreateArgs("rw-test", cfg)
	for _, a := range args {
		if a == "--read-only" {
			t.Error("--read-only should not appear when ReadOnlyRoot=false")
		}
	}
}

func TestBuildCreateArgs_WorkspaceMount(t *testing.T) {
	cfg := Config{
		Image:        "img",
		UID:          "1000:1000",
		NetworkMode:  "none",
		MemoryMB:     128,
		CPUPercent:   10.0,
		WorkspaceDir: "/home/user/project",
	}
	args := buildCreateArgs("ws-test", cfg)
	assertContainsArg(t, args, "--volume", "/home/user/project:/workspace:rw")
}

func TestBuildCreateArgs_NoWorkspaceMount(t *testing.T) {
	cfg := Config{
		Image:       "img",
		UID:         "1000:1000",
		NetworkMode: "none",
		MemoryMB:    128,
		CPUPercent:  10.0,
	}
	args := buildCreateArgs("no-ws", cfg)
	for i, a := range args {
		if a == "--volume" {
			t.Errorf("--volume should not appear without WorkspaceDir (found at index %d, value %s)", i+1, args[i+1])
		}
	}
}

func TestBuildCreateArgs_CustomLabels(t *testing.T) {
	cfg := Config{
		Image:       "img",
		UID:         "1000:1000",
		NetworkMode: "none",
		MemoryMB:    128,
		CPUPercent:  10.0,
		Labels: map[string]string{
			"project": "alpha",
			"env":     "ci",
		},
	}
	args := buildCreateArgs("label-test", cfg)
	assertContainsArg(t, args, "--label", "env=ci")
	assertContainsArg(t, args, "--label", "project=alpha")
}

func TestBuildCreateArgs_EnvVars(t *testing.T) {
	cfg := Config{
		Image:       "img",
		UID:         "1000:1000",
		NetworkMode: "none",
		MemoryMB:    128,
		CPUPercent:  10.0,
		Env:         []string{"FOO=bar", "BAZ=qux"},
	}
	args := buildCreateArgs("env-test", cfg)
	assertContainsArg(t, args, "--env", "FOO=bar")
	assertContainsArg(t, args, "--env", "BAZ=qux")
}

// ---- Manager.Start tests ----

func TestStart_Success(t *testing.T) {
	runner := newFakeRunner()
	mgr := newManagerWithRunner(runner)

	sb, _ := mgr.Create(context.Background(), "s1", DefaultConfig())
	if err := mgr.Start(context.Background(), "s1"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if sb.State() != StateRunning {
		t.Errorf("state = %v, want running", sb.State())
	}
	starts := runner.callsForSubcmd("start")
	if len(starts) != 1 {
		t.Fatalf("docker start called %d times, want 1", len(starts))
	}
}

func TestStart_AlreadyRunning(t *testing.T) {
	runner := newFakeRunner()
	mgr := newManagerWithRunner(runner)

	_, _ = mgr.Create(context.Background(), "s2", DefaultConfig())
	_ = mgr.Start(context.Background(), "s2")
	_ = mgr.Start(context.Background(), "s2") // second Start is a no-op

	starts := runner.callsForSubcmd("start")
	if len(starts) != 1 {
		t.Errorf("docker start called %d times, want 1 (idempotent)", len(starts))
	}
}

func TestStart_NotFound(t *testing.T) {
	mgr := newManagerWithRunner(newFakeRunner())
	if err := mgr.Start(context.Background(), "missing"); err == nil {
		t.Fatal("expected error for missing sandbox")
	}
}

func TestStart_DockerError(t *testing.T) {
	runner := newFakeRunner()
	runner.setError("start", errors.New("container not found"))
	mgr := newManagerWithRunner(runner)

	_, _ = mgr.Create(context.Background(), "fail-start", DefaultConfig())
	err := mgr.Start(context.Background(), "fail-start")
	if err == nil {
		t.Fatal("expected error from docker start failure")
	}
	state, _ := mgr.Status("fail-start")
	if state != StateFailed {
		t.Errorf("state after failed start = %v, want failed", state)
	}
}

// ---- Manager.Stop tests ----

func TestStop_Success(t *testing.T) {
	runner := newFakeRunner()
	mgr := newManagerWithRunner(runner)

	_, _ = mgr.Create(context.Background(), "stop1", DefaultConfig())
	_ = mgr.Start(context.Background(), "stop1")
	if err := mgr.Stop(context.Background(), "stop1"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	state, _ := mgr.Status("stop1")
	if state != StateStopped {
		t.Errorf("state = %v, want stopped", state)
	}
}

func TestStop_Idempotent(t *testing.T) {
	runner := newFakeRunner()
	mgr := newManagerWithRunner(runner)

	_, _ = mgr.Create(context.Background(), "stop-idem", DefaultConfig())
	_ = mgr.Start(context.Background(), "stop-idem")
	_ = mgr.Stop(context.Background(), "stop-idem")
	_ = mgr.Stop(context.Background(), "stop-idem") // second Stop is a no-op

	stops := runner.callsForSubcmd("stop")
	if len(stops) != 1 {
		t.Errorf("docker stop called %d times, want 1", len(stops))
	}
}

func TestStop_NotFound(t *testing.T) {
	mgr := newManagerWithRunner(newFakeRunner())
	if err := mgr.Stop(context.Background(), "ghost"); err == nil {
		t.Fatal("expected error for missing sandbox")
	}
}

// ---- Manager.Destroy tests ----

func TestDestroy_Success(t *testing.T) {
	runner := newFakeRunner()
	mgr := newManagerWithRunner(runner)

	_, _ = mgr.Create(context.Background(), "d1", DefaultConfig())
	_ = mgr.Start(context.Background(), "d1")
	if err := mgr.Destroy(context.Background(), "d1"); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	// Sandbox should be gone from the manager.
	if _, err := mgr.Status("d1"); err == nil {
		t.Error("expected 'not found' after destroy")
	}
	rms := runner.callsForSubcmd("rm")
	if len(rms) != 1 {
		t.Fatalf("docker rm called %d times, want 1", len(rms))
	}
}

func TestDestroy_StopsRunningContainer(t *testing.T) {
	runner := newFakeRunner()
	mgr := newManagerWithRunner(runner)

	_, _ = mgr.Create(context.Background(), "d-running", DefaultConfig())
	_ = mgr.Start(context.Background(), "d-running")
	_ = mgr.Destroy(context.Background(), "d-running")

	// Should have issued a "stop" before "rm".
	stops := runner.callsForSubcmd("stop")
	if len(stops) == 0 {
		t.Error("expected docker stop to be called before rm")
	}
}

func TestDestroy_NotFound(t *testing.T) {
	mgr := newManagerWithRunner(newFakeRunner())
	if err := mgr.Destroy(context.Background(), "ghost"); err == nil {
		t.Fatal("expected error for missing sandbox")
	}
}

func TestDestroyAll(t *testing.T) {
	runner := newFakeRunner()
	mgr := newManagerWithRunner(runner)

	for _, id := range []string{"a", "b", "c"} {
		_, _ = mgr.Create(context.Background(), id, DefaultConfig())
	}

	if err := mgr.DestroyAll(context.Background()); err != nil {
		t.Fatalf("DestroyAll: %v", err)
	}
	if got := mgr.List(); len(got) != 0 {
		t.Errorf("List after DestroyAll = %d sandboxes, want 0", len(got))
	}
}

// ---- Manager.Status / List tests ----

func TestStatus(t *testing.T) {
	runner := newFakeRunner()
	mgr := newManagerWithRunner(runner)

	_, _ = mgr.Create(context.Background(), "st1", DefaultConfig())
	state, err := mgr.Status("st1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if state != StateCreating {
		t.Errorf("state = %v, want creating", state)
	}
}

func TestStatus_NotFound(t *testing.T) {
	mgr := newManagerWithRunner(newFakeRunner())
	_, err := mgr.Status("nope")
	if err == nil {
		t.Fatal("expected error for missing sandbox")
	}
}

func TestList(t *testing.T) {
	runner := newFakeRunner()
	mgr := newManagerWithRunner(runner)

	if got := mgr.List(); len(got) != 0 {
		t.Errorf("initial List() = %d, want 0", len(got))
	}

	for i := 0; i < 3; i++ {
		_, _ = mgr.Create(context.Background(), fmt.Sprintf("sb%d", i), DefaultConfig())
	}
	if got := mgr.List(); len(got) != 3 {
		t.Errorf("List() = %d sandboxes, want 3", len(got))
	}
}

// ---- Manager.Inspect tests ----

func TestInspect_Success(t *testing.T) {
	runner := newFakeRunner()
	runner.setOutput("inspect", []byte(`{"Id":"abc123","State":{"Status":"running"}}`))
	mgr := newManagerWithRunner(runner)

	_, _ = mgr.Create(context.Background(), "insp1", DefaultConfig())
	info, err := mgr.Inspect(context.Background(), "insp1")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if info["Id"] != "abc123" {
		t.Errorf("Id = %v, want abc123", info["Id"])
	}
}

func TestInspect_NotFound(t *testing.T) {
	mgr := newManagerWithRunner(newFakeRunner())
	_, err := mgr.Inspect(context.Background(), "ghost")
	if err == nil {
		t.Fatal("expected error for missing sandbox")
	}
}

func TestInspect_ParseError(t *testing.T) {
	runner := newFakeRunner()
	runner.setOutput("inspect", []byte("not valid json"))
	mgr := newManagerWithRunner(runner)

	_, _ = mgr.Create(context.Background(), "bad-json", DefaultConfig())
	_, err := mgr.Inspect(context.Background(), "bad-json")
	if err == nil {
		t.Fatal("expected parse error for invalid JSON")
	}
}

// ---- Auto-heal tests ----

func TestHealAll_RestartsFailed(t *testing.T) {
	runner := newFakeRunner()
	mgr := newManagerWithRunner(runner)

	_, _ = mgr.Create(context.Background(), "heal1", DefaultConfig())
	sb := mgr.get("heal1")
	sb.mu.Lock()
	sb.setState(StateFailed)
	sb.mu.Unlock()

	mgr.healAll(context.Background())

	// healAll should have called Start, transitioning to Running.
	state, _ := mgr.Status("heal1")
	if state != StateRunning {
		t.Errorf("state after heal = %v, want running", state)
	}
}

func TestHealAll_DestroysIdleSandbox(t *testing.T) {
	runner := newFakeRunner()
	mgr := newManagerWithRunner(runner)

	cfg := DefaultConfig()
	cfg.IdleTimeout = 1 * time.Millisecond // tiny timeout for the test

	_, _ = mgr.Create(context.Background(), "idle1", cfg)
	_ = mgr.Start(context.Background(), "idle1")

	// Make the last-activity timestamp look old.
	sb := mgr.get("idle1")
	sb.mu.Lock()
	sb.LastActivityAt = time.Now().Add(-2 * time.Millisecond)
	sb.mu.Unlock()

	mgr.healAll(context.Background())

	// The idle sandbox should have been destroyed.
	if _, err := mgr.Status("idle1"); err == nil {
		t.Error("idle sandbox should have been destroyed by healAll")
	}
}

func TestHealAll_SkipsNonFailedNonRunning(t *testing.T) {
	runner := newFakeRunner()
	mgr := newManagerWithRunner(runner)

	_, _ = mgr.Create(context.Background(), "creating1", DefaultConfig())
	// State is StateCreating — healAll should leave it alone.

	before := len(runner.calls)
	mgr.healAll(context.Background())
	after := len(runner.calls)

	if after != before {
		t.Errorf("healAll made %d unexpected docker calls for a creating sandbox", after-before)
	}
}

// ---- Sandbox.Touch tests ----

func TestTouch(t *testing.T) {
	sb := &Sandbox{
		ID:             "touch-test",
		LastActivityAt: time.Now().Add(-time.Hour),
	}
	before := sb.LastActivityAt
	sb.Touch()
	if !sb.LastActivityAt.After(before) {
		t.Error("Touch should update LastActivityAt to a later time")
	}
}

// ---- helpers ----

// assertContainsArg checks that args contains flag followed immediately by value.
func assertContainsArg(t *testing.T, args []string, flag, value string) {
	t.Helper()
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag && args[i+1] == value {
			return
		}
	}
	t.Errorf("args %v: expected %q %q pair", args, flag, value)
}

// assertContainsFlag checks that args contains a standalone flag.
func assertContainsFlag(t *testing.T, args []string, flag string) {
	t.Helper()
	for _, a := range args {
		if a == flag {
			return
		}
	}
	t.Errorf("args %v: expected flag %q", args, flag)
}
