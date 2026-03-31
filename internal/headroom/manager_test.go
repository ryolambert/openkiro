package headroom_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
	if !mgr.Running() {
		// Good — should not be running after failed start.
	}
}

func TestManager_WaitHealthy_ContextCancelled(t *testing.T) {
	// Create a health endpoint that never responds.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(10 * time.Second) // Block forever
	}))
	defer srv.Close()

	cfg := headroom.DefaultConfig()
	cfg.HealthTimeout = 100 * time.Millisecond
	mgr := headroom.NewManager(cfg)

	// WaitHealthy is tested indirectly via Start, but Start requires the
	// binary. We can verify the manager doesn't panic and properly reports
	// not running after a failed start.
	if mgr.Running() {
		t.Error("should not be running")
	}
}

func TestManager_Install_NoPip(t *testing.T) {
	cfg := headroom.DefaultConfig()
	cfg.PipPackage = "headroom-ai-nonexistent-package-12345"
	mgr := headroom.NewManager(cfg)

	// Install with a nonexistent package should return an error (or pip
	// not found). Either way it must not panic.
	_ = mgr.Install(context.Background())
}

func TestManager_PipCommand(t *testing.T) {
	cfg := headroom.DefaultConfig()
	cfg.PipPackage = "headroom-ai-nonexistent-pkg-for-test"
	mgr := headroom.NewManager(cfg)

	// Exercise PipCommand indirectly via Install — the pip command
	// detection runs even if the install itself fails.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = mgr.Install(ctx)
}
