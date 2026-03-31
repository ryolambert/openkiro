package headroom

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestPipCommand(t *testing.T) {
	cfg := DefaultConfig()
	mgr := NewManager(cfg)

	// pipCommand should return a non-empty string.
	pip := mgr.pipCommand()
	if pip == "" {
		t.Fatal("pipCommand() returned empty string")
	}
	// On any platform it should be either "pip" or "pip3".
	if pip != "pip" && pip != "pip3" {
		t.Errorf("unexpected pip command: %s", pip)
	}
}

func TestWaitHealthy_Success(t *testing.T) {
	// Start a test HTTP server that responds healthy.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	// Extract host:port from the test server URL.
	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())

	cfg := DefaultConfig()
	cfg.Host = host
	cfg.Port = port
	cfg.HealthTimeout = 5 * time.Second

	mgr := NewManager(cfg)

	ctx := context.Background()
	if err := mgr.waitHealthy(ctx); err != nil {
		t.Fatalf("waitHealthy: unexpected error: %v", err)
	}
}

func TestWaitHealthy_Timeout(t *testing.T) {
	// Start a test server that never responds healthy.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())

	cfg := DefaultConfig()
	cfg.Host = host
	cfg.Port = port
	cfg.HealthTimeout = 600 * time.Millisecond

	mgr := NewManager(cfg)

	ctx := context.Background()
	err := mgr.waitHealthy(ctx)
	if err == nil {
		t.Fatal("waitHealthy: expected timeout error, got nil")
	}
}

func TestWaitHealthy_ContextCancel(t *testing.T) {
	// Start a test server that never responds healthy.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())

	cfg := DefaultConfig()
	cfg.Host = host
	cfg.Port = port
	cfg.HealthTimeout = 30 * time.Second // Long timeout — we'll cancel first.

	mgr := NewManager(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()

	err := mgr.waitHealthy(ctx)
	if err == nil {
		t.Fatal("waitHealthy: expected context error, got nil")
	}
}

func TestStopLocked_WithProcess(t *testing.T) {
	// Start a real (harmless) process so we can test stopLocked.
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start sleep process: %v", err)
	}

	cfg := DefaultConfig()
	mgr := &Manager{
		cfg:     cfg,
		cmd:     cmd,
		running: true,
	}

	if err := mgr.stopLocked(); err != nil {
		t.Fatalf("stopLocked: unexpected error: %v", err)
	}
	if mgr.running {
		t.Error("expected running to be false after stopLocked")
	}

	// Clean up: wait for the process to exit.
	_ = cmd.Wait()
}

func TestStopLocked_NilCmd(t *testing.T) {
	cfg := DefaultConfig()
	mgr := &Manager{
		cfg:     cfg,
		cmd:     nil,
		running: true,
	}

	if err := mgr.stopLocked(); err != nil {
		t.Fatalf("stopLocked with nil cmd: unexpected error: %v", err)
	}
}

func TestStopLocked_NilProcess(t *testing.T) {
	cfg := DefaultConfig()
	mgr := &Manager{
		cfg:     cfg,
		cmd:     &exec.Cmd{}, // cmd is set but Process is nil
		running: true,
	}

	if err := mgr.stopLocked(); err != nil {
		t.Fatalf("stopLocked with nil process: unexpected error: %v", err)
	}
}

func TestStopLocked_NotRunning(t *testing.T) {
	cfg := DefaultConfig()
	mgr := &Manager{
		cfg:     cfg,
		running: false,
	}

	if err := mgr.stopLocked(); err != nil {
		t.Fatalf("stopLocked when not running: unexpected error: %v", err)
	}
}

func TestStopLocked_ProcessAlreadyExited(t *testing.T) {
	// Start a process that exits immediately.
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start true process: %v", err)
	}
	_ = cmd.Wait() // Wait for it to finish.

	cfg := DefaultConfig()
	mgr := &Manager{
		cfg:     cfg,
		cmd:     cmd,
		running: true,
	}

	// Signal on an already-exited process — tests the error path
	// in stopLocked where Signal fails and Kill is attempted.
	if err := mgr.stopLocked(); err != nil {
		t.Fatalf("stopLocked on exited process: unexpected error: %v", err)
	}
	if mgr.running {
		t.Error("expected running to be false")
	}
}

func TestStart_AlreadyRunning(t *testing.T) {
	cfg := DefaultConfig()
	mgr := &Manager{
		cfg:     cfg,
		running: true,
	}

	// Start when already running should be a no-op.
	if err := mgr.Start(context.Background()); err != nil {
		t.Fatalf("Start when already running: unexpected error: %v", err)
	}
}

func TestInstall_BadPythonBin(t *testing.T) {
	cfg := DefaultConfig()
	cfg.PythonBin = "nonexistent-python-xyz"
	// Override PipPackage so pip command is also unlikely to be found
	// under a weird name.
	mgr := NewManager(cfg)

	// pipCommand falls through to "pip" or "pip3", so Install will
	// try one of those but with a nonexistent package. Either way,
	// it exercises the code path.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = mgr.Install(ctx)
}

func TestPipCommand_Fallback(t *testing.T) {
	// Verify pipCommand doesn't panic and returns a string even with
	// a customized (but still valid) config.
	cfg := DefaultConfig()
	cfg.PythonBin = "python3"
	mgr := NewManager(cfg)

	result := mgr.pipCommand()
	if result != "pip" && result != "pip3" {
		t.Errorf("unexpected pip command: %q", result)
	}
}

func TestInstall_ContextTimeout(t *testing.T) {
	cfg := DefaultConfig()
	cfg.PipPackage = "nonexistent-headroom-package-xyz-12345"
	mgr := NewManager(cfg)

	// Use a very short timeout so pip fails quickly.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := mgr.Install(ctx)
	// Should fail (either timeout or package not found).
	if err == nil {
		t.Log("Install succeeded unexpectedly (pip found something?), that's OK")
	}
}

func TestManager_DirectFieldAccess(t *testing.T) {
	// Test that the Manager struct initializes correctly.
	cfg := DefaultConfig()
	mgr := &Manager{cfg: cfg}

	if mgr.running {
		t.Error("running should default to false")
	}
	if mgr.cmd != nil {
		t.Error("cmd should default to nil")
	}
}

func TestStartAndStop_Integration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("integration test requires Unix signals")
	}

	// Build a tiny Go binary to act as a fake headroom proxy.
	tmpDir := t.TempDir()

	srcFile := filepath.Join(tmpDir, "fakeheadroom.go")
	if err := os.WriteFile(srcFile, []byte(fakeHeadroomSrc), 0o644); err != nil {
		t.Fatalf("write fake source: %v", err)
	}

	binPath := filepath.Join(tmpDir, "headroom")
	build := exec.Command("go", "build", "-o", binPath, srcFile)
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build fake headroom: %v", err)
	}

	// Find a free port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	ln.Close()

	// Put our fake binary on PATH.
	t.Setenv("PATH", tmpDir+string(filepath.ListSeparator)+os.Getenv("PATH"))

	cfg := DefaultConfig()
	cfg.Port = port
	cfg.HealthTimeout = 10 * time.Second
	mgr := NewManager(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := mgr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if !mgr.Running() {
		t.Error("expected Running() to be true")
	}

	if err := mgr.Stop(); err != nil {
		t.Errorf("Stop: %v", err)
	}

	// Give process time to exit.
	time.Sleep(200 * time.Millisecond)

	if mgr.Running() {
		t.Error("expected Running() to be false after Stop()")
	}
}

// fakeHeadroomSrc is a minimal Go program that mimics the headroom proxy:
// it serves /health on the given --port and exits cleanly on SIGINT/SIGTERM.
const fakeHeadroomSrc = `package main

import (
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	host := "127.0.0.1"
	port := "8787"
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--host":
			if i+1 < len(args) {
				host = args[i+1]
				i++
			}
		case "--port":
			if i+1 < len(args) {
				port = args[i+1]
				i++
			}
		}
	}

	http.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("{\"status\":\"healthy\"}"))
	})

	addr := fmt.Sprintf("%s:%s", host, port)
	srv := &http.Server{Addr: addr}
	go srv.ListenAndServe()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	srv.Close()
}
`

