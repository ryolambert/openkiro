package headroom_test

import (
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
