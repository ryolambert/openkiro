package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolvePort(t *testing.T) {
	tests := []struct {
		name    string
		flag    string
		env     string
		want    string
		wantErr bool
	}{
		{"default", "", "", "1234", false},
		{"flag wins", "5678", "", "5678", false},
		{"env wins over default", "", "9999", "9999", false},
		{"flag wins over env", "5678", "9999", "5678", false},
		{"invalid non-numeric", "abc", "", "", true},
		{"port zero", "0", "", "", true},
		{"port too high", "70000", "", "", true},
		{"valid boundary low", "1", "", "1", false},
		{"valid boundary high", "65535", "", "65535", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env != "" {
				t.Setenv("OPENKIRO_PORT", tt.env)
			} else {
				t.Setenv("OPENKIRO_PORT", "")
			}
			got, err := ResolvePort(tt.flag)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for flag=%q env=%q, got %q", tt.flag, tt.env, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLogDir(t *testing.T) {
	dir, err := LogDir()
	if err != nil {
		t.Fatalf("LogDir() error: %v", err)
	}
	if dir == "" {
		t.Fatal("LogDir() returned empty string")
	}
	if !filepath.IsAbs(dir) {
		t.Errorf("LogDir() returned relative path: %s", dir)
	}
	switch runtime.GOOS {
	case "darwin":
		if !strings.Contains(dir, filepath.Join("Library", "Logs", "openkiro")) {
			t.Errorf("darwin: expected Library/Logs/openkiro in %s", dir)
		}
	case "windows":
		if !strings.Contains(dir, filepath.Join("openkiro", "logs")) {
			t.Errorf("windows: expected openkiro\\logs in %s", dir)
		}
	default:
		if !strings.Contains(dir, filepath.Join(".local", "state", "openkiro")) {
			t.Errorf("linux: expected .local/state/openkiro in %s", dir)
		}
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("LogDir() directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("LogDir() path is not a directory: %s", dir)
	}
}

func TestPidFilePath(t *testing.T) {
	p, err := PidFilePath()
	if err != nil {
		t.Fatalf("PidFilePath() error: %v", err)
	}
	if filepath.Base(p) != "openkiro.pid" {
		t.Errorf("PidFilePath() base = %q, want openkiro.pid", filepath.Base(p))
	}
	if !filepath.IsAbs(p) {
		t.Errorf("PidFilePath() returned relative path: %s", p)
	}
}

func TestLogFilePath(t *testing.T) {
	p, err := LogFilePath()
	if err != nil {
		t.Fatalf("LogFilePath() error: %v", err)
	}
	if filepath.Base(p) != "openkiro.log" {
		t.Errorf("LogFilePath() base = %q, want openkiro.log", filepath.Base(p))
	}
	if !filepath.IsAbs(p) {
		t.Errorf("LogFilePath() returned relative path: %s", p)
	}
}

func TestWriteAndReadPID(t *testing.T) {
	pid := os.Getpid()
	if err := WritePID(pid); err != nil {
		t.Fatalf("WritePID: %v", err)
	}
	t.Cleanup(func() { RemovePID() })

	got, err := ReadPID()
	if err != nil {
		t.Fatalf("ReadPID: %v", err)
	}
	if got != pid {
		t.Errorf("ReadPID() = %d, want %d", got, pid)
	}
}

func TestRemovePID(t *testing.T) {
	if err := WritePID(12345); err != nil {
		t.Fatalf("WritePID: %v", err)
	}
	if err := RemovePID(); err != nil {
		t.Fatalf("RemovePID: %v", err)
	}
	p, _ := PidFilePath()
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("PID file still exists after RemovePID")
	}
}

func TestRemovePIDNonExistent(t *testing.T) {
	RemovePID()
	if err := RemovePID(); err != nil {
		t.Errorf("RemovePID on non-existent file: %v", err)
	}
}

func TestIsRunning(t *testing.T) {
	tests := []struct {
		name string
		pid  int
		want bool
	}{
		{"own process", os.Getpid(), true},
		{"bogus PID", 99999999, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsRunning(tt.pid); got != tt.want {
				t.Errorf("IsRunning(%d) = %v, want %v", tt.pid, got, tt.want)
			}
		})
	}
}

func TestCleanStalePID(t *testing.T) {
	if err := WritePID(99999999); err != nil {
		t.Fatalf("WritePID: %v", err)
	}
	if err := CleanStalePID(); err != nil {
		t.Fatalf("CleanStalePID: %v", err)
	}
	p, _ := PidFilePath()
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("stale PID file not cleaned up")
	}
}

func TestCleanStalePIDRunning(t *testing.T) {
	if err := WritePID(os.Getpid()); err != nil {
		t.Fatalf("WritePID: %v", err)
	}
	t.Cleanup(func() { RemovePID() })

	err := CleanStalePID()
	if err == nil {
		t.Fatal("CleanStalePID should error when process is running")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSelfPath(t *testing.T) {
	p, err := SelfPath()
	if err != nil {
		t.Fatalf("SelfPath: %v", err)
	}
	if p == "" {
		t.Fatal("SelfPath returned empty")
	}
	if !filepath.IsAbs(p) {
		t.Errorf("SelfPath not absolute: %s", p)
	}
}

func TestLaunchdPlistPath(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin only")
	}
	p, err := LaunchdPlistPath()
	if err != nil {
		t.Fatalf("LaunchdPlistPath: %v", err)
	}
	if filepath.Base(p) != "com.openkiro.proxy.plist" {
		t.Errorf("unexpected basename: %s", filepath.Base(p))
	}
	if !strings.Contains(p, "LaunchAgents") {
		t.Errorf("expected LaunchAgents in path: %s", p)
	}
}

func TestGeneratePlist(t *testing.T) {
	xml := GeneratePlist("/usr/local/bin/openkiro", "1234", "/tmp/openkiro.log")
	checks := []string{
		"com.openkiro.proxy",
		"/usr/local/bin/openkiro",
		"1234",
		"/tmp/openkiro.log",
		"<key>KeepAlive</key>",
		"<true/>",
		"<key>RunAtLoad</key>",
		"<false/>",
	}
	for _, c := range checks {
		if !strings.Contains(xml, c) {
			t.Errorf("plist missing %q", c)
		}
	}
}

func TestParsePortFlag(t *testing.T) {
	orig := os.Args
	t.Cleanup(func() { os.Args = orig })

	os.Args = []string{"openkiro", "start", "--port", "5678"}
	if got := ParsePortFlag(); got != "5678" {
		t.Errorf("ParsePortFlag = %q, want 5678", got)
	}

	os.Args = []string{"openkiro", "start"}
	if got := ParsePortFlag(); got != "" {
		t.Errorf("ParsePortFlag = %q, want empty", got)
	}
}

func TestSetClaudeUpdatesClaudeConfig(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	claudeConfigPath := filepath.Join(tempHome, ".claude.json")
	initial := map[string]any{
		"hasCompletedOnboarding": false,
		"kirolink":               true,
		LegacyClaudeConfigKey():  true,
		"theme":                  "dark",
	}
	data, err := json.Marshal(initial)
	if err != nil {
		t.Fatalf("marshal initial claude config: %v", err)
	}
	if err := os.WriteFile(claudeConfigPath, data, 0o644); err != nil {
		t.Fatalf("write initial claude config: %v", err)
	}

	SetClaude()

	updatedData, err := os.ReadFile(claudeConfigPath)
	if err != nil {
		t.Fatalf("read updated claude config: %v", err)
	}

	var updated map[string]any
	if err := json.Unmarshal(updatedData, &updated); err != nil {
		t.Fatalf("unmarshal updated claude config: %v", err)
	}

	if got := updated["hasCompletedOnboarding"]; got != true {
		t.Fatalf("expected hasCompletedOnboarding=true, got %#v", got)
	}
	if got := updated["openkiro"]; got != true {
		t.Fatalf("expected openkiro=true, got %#v", got)
	}
	if _, ok := updated["kirolink"]; ok {
		t.Fatalf("expected legacy kirolink key to be removed during config update")
	}
	if _, ok := updated[LegacyClaudeConfigKey()]; ok {
		t.Fatalf("expected legacy helper key to be removed during config update")
	}
	if got := updated["theme"]; got != "dark" {
		t.Fatalf("expected unrelated config to be preserved, got %#v", got)
	}
}
