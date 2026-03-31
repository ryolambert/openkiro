package headroom_test

import (
	"context"
	"testing"

	"github.com/ryolambert/openkiro/internal/headroom"
)

func TestNewManager(t *testing.T) {
	cfg := headroom.DefaultConfig()
	mgr := headroom.NewManager(cfg)
	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}
	if mgr.Running() {
		t.Error("manager should not be running after creation")
	}
}

func TestManager_PythonAvailable(t *testing.T) {
	cfg := headroom.DefaultConfig()
	mgr := headroom.NewManager(cfg)

	// In CI there's usually python3 available; if not, this test just
	// verifies the method does not panic and returns a boolean.
	_ = mgr.PythonAvailable()
}

func TestManager_PythonAvailable_CustomBin(t *testing.T) {
	cfg := headroom.DefaultConfig()
	cfg.PythonBin = "nonexistent-python-binary"
	mgr := headroom.NewManager(cfg)

	// With a nonexistent binary, PythonAvailable may still return true if
	// "python" fallback exists, but must not panic.
	_ = mgr.PythonAvailable()
}

func TestManager_Installed(t *testing.T) {
	cfg := headroom.DefaultConfig()
	mgr := headroom.NewManager(cfg)

	// headroom is almost certainly not installed in CI, but the method
	// must not panic.
	_ = mgr.Installed()
}

func TestManager_StopWhenNotRunning(t *testing.T) {
	cfg := headroom.DefaultConfig()
	mgr := headroom.NewManager(cfg)
	// Stopping a not-running manager should be a no-op.
	if err := mgr.Stop(); err != nil {
		t.Fatalf("unexpected error stopping idle manager: %v", err)
	}
}

func TestManager_StopMultipleTimes(t *testing.T) {
	cfg := headroom.DefaultConfig()
	mgr := headroom.NewManager(cfg)
	// Calling Stop() multiple times should be safe.
	for i := 0; i < 3; i++ {
		if err := mgr.Stop(); err != nil {
			t.Fatalf("Stop() call %d: unexpected error: %v", i, err)
		}
	}
}

func TestManager_RunningWhenNotStarted(t *testing.T) {
	cfg := headroom.DefaultConfig()
	mgr := headroom.NewManager(cfg)
	if mgr.Running() {
		t.Error("Running() should be false before Start()")
	}
}

func TestManager_StartWithoutBinary(t *testing.T) {
	cfg := headroom.DefaultConfig()
	mgr := headroom.NewManager(cfg)

	// headroom binary is not installed in CI, so Start should fail with
	// a descriptive error.
	err := mgr.Start(context.Background())
	if err == nil {
		// If headroom is somehow installed, stop it and skip.
		mgr.Stop()
		t.Skip("headroom binary is available, skipping binary-not-found test")
	}
	// Should not be running after a failed start.
	if mgr.Running() {
		t.Error("manager should not be running after failed Start()")
	}
}

func TestManager_Install_NoPip(t *testing.T) {
	t.Skip("skipped: Install uses real pip; requires dependency injection or stubbing")
}

func TestManager_PipCommand(t *testing.T) {
	t.Skip("skipped: Install uses real pip; requires dependency injection or stubbing")
}
