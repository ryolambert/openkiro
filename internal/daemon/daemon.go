package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/ryolambert/openkiro/internal/proxy"
)

// LogDir returns the platform-appropriate log directory for openkiro.
func LogDir() (string, error) {
	var dir string
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("logDir: %w", err)
		}
		dir = filepath.Join(home, "Library", "Logs", "openkiro")
	case "windows":
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			return "", fmt.Errorf("logDir: LOCALAPPDATA not set")
		}
		dir = filepath.Join(local, "openkiro", "logs")
	default:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("logDir: %w", err)
		}
		dir = filepath.Join(home, ".local", "state", "openkiro")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("logDir: mkdir %q: %w", dir, err)
	}
	return dir, nil
}

// PidFilePath returns the path to the PID file.
func PidFilePath() (string, error) {
	dir, err := LogDir()
	if err != nil {
		return "", fmt.Errorf("pidFilePath: %w", err)
	}
	return filepath.Join(dir, "openkiro.pid"), nil
}

// LogFilePath returns the path to the log file.
func LogFilePath() (string, error) {
	dir, err := LogDir()
	if err != nil {
		return "", fmt.Errorf("logFilePath: %w", err)
	}
	return filepath.Join(dir, "openkiro.log"), nil
}

// WritePID writes the PID to the PID file.
func WritePID(pid int) error {
	p, err := PidFilePath()
	if err != nil {
		return fmt.Errorf("writePID: %w", err)
	}
	if err := os.WriteFile(p, []byte(strconv.Itoa(pid)), 0o644); err != nil {
		return fmt.Errorf("writePID: %w", err)
	}
	return nil
}

// ReadPID reads the PID from the PID file.
func ReadPID() (int, error) {
	p, err := PidFilePath()
	if err != nil {
		return 0, fmt.Errorf("readPID: %w", err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return 0, fmt.Errorf("readPID: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("readPID: invalid pid: %w", err)
	}
	return pid, nil
}

// RemovePID removes the PID file.
func RemovePID() error {
	p, err := PidFilePath()
	if err != nil {
		return fmt.Errorf("removePID: %w", err)
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removePID: %w", err)
	}
	return nil
}

// IsRunning checks if a process with the given PID is running.
func IsRunning(pid int) bool {
	if pid == os.Getpid() {
		return true
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// CleanStalePID removes a stale PID file if the process is not running.
func CleanStalePID() error {
	pid, err := ReadPID()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("cleanStalePID: %w", err)
	}
	if IsRunning(pid) {
		return fmt.Errorf("cleanStalePID: already running with PID %d", pid)
	}
	return RemovePID()
}

// SelfPath returns the absolute path to the current binary.
func SelfPath() (string, error) {
	// Prefer the installed binary from PATH (stable across go run / go install).
	if p, err := exec.LookPath("openkiro"); err == nil {
		return p, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("selfPath: %w", err)
	}
	real, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("selfPath: %w", err)
	}
	return real, nil
}

// LaunchdPlistPath returns the path to the launchd plist file.
func LaunchdPlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("launchdPlistPath: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", proxy.LaunchdLabel+".plist"), nil
}

// GeneratePlist returns a launchd plist XML for the proxy.
func GeneratePlist(binaryPath, port, logPath string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>server</string>
		<string>--port</string>
		<string>%s</string>
	</array>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
	<key>KeepAlive</key>
	<true/>
	<key>RunAtLoad</key>
	<false/>
</dict>
</plist>
`, proxy.LaunchdLabel, binaryPath, port, logPath, logPath)
}

// ResolvePort returns the port to use: flag > env > default.
func ResolvePort(flagValue string) (string, error) {
	port := proxy.DefaultPort
	if flagValue != "" {
		port = flagValue
	} else if envPort := os.Getenv("OPENKIRO_PORT"); envPort != "" {
		port = envPort
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("invalid port %q: must be 1-65535", port)
	}
	return port, nil
}

// ParsePortFlag extracts --port value from os.Args[2:].
func ParsePortFlag() string {
	for i := 2; i < len(os.Args); i++ {
		if os.Args[i] == "--port" && i+1 < len(os.Args) {
			return os.Args[i+1]
		}
	}
	return ""
}

// FileExists checks if a file exists.
func FileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// LegacyClaudeConfigKey returns the legacy config key.
func LegacyClaudeConfigKey() string {
	return strings.Join([]string{"kiro", "2cc"}, "")
}

// SetClaude updates the Claude configuration file.
func SetClaude() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("Failed to get user home directory: %v\n", err)
		os.Exit(1)
	}

	claudeJsonPath := filepath.Join(homeDir, ".claude.json")
	ok, _ := FileExists(claudeJsonPath)
	if !ok {
		fmt.Println("Claude configuration file not found, please check if Claude Code is installed")
		fmt.Println("npm install -g @anthropic-ai/claude-code")
		os.Exit(1)
	}

	data, err := os.ReadFile(claudeJsonPath)
	if err != nil {
		fmt.Printf("Failed to read Claude file: %v\n", err)
		os.Exit(1)
	}

	var jsonData map[string]interface{}
	if err := json.Unmarshal(data, &jsonData); err != nil {
		fmt.Printf("Failed to parse JSON file: %v\n", err)
		os.Exit(1)
	}

	jsonData["hasCompletedOnboarding"] = true
	jsonData["openkiro"] = true
	delete(jsonData, "kirolink")
	delete(jsonData, LegacyClaudeConfigKey())

	newJson, err := json.MarshalIndent(jsonData, "", "  ")
	if err != nil {
		fmt.Printf("Failed to generate JSON file: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(claudeJsonPath, newJson, 0644); err != nil {
		fmt.Printf("Failed to write JSON file: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Claude configuration file updated")
}
