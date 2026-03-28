package sandbox

import (
	"strings"
	"testing"
)

// ---- AgentConfig tests ----

func TestAgentConfig_UsesBridgeNetwork(t *testing.T) {
	cfg := AgentConfig()
	if cfg.NetworkMode != "bridge" {
		t.Errorf("AgentConfig NetworkMode = %q, want %q", cfg.NetworkMode, "bridge")
	}
}

func TestAgentConfig_PreservesSecurityConstraints(t *testing.T) {
	cfg := AgentConfig()
	if !cfg.ReadOnlyRoot {
		t.Error("AgentConfig should keep ReadOnlyRoot=true")
	}
	if cfg.UID != DefaultUID {
		t.Errorf("AgentConfig UID = %q, want %q", cfg.UID, DefaultUID)
	}
	if cfg.MemoryMB != DefaultMemoryMB {
		t.Errorf("AgentConfig MemoryMB = %d, want %d", cfg.MemoryMB, DefaultMemoryMB)
	}
	if cfg.CPUPercent != DefaultCPUPercent {
		t.Errorf("AgentConfig CPUPercent = %f, want %f", cfg.CPUPercent, DefaultCPUPercent)
	}
}

// ---- ClaudeCodeConfig tests ----

func TestClaudeCodeConfig_SetsAnthropicBaseURL(t *testing.T) {
	cfg := ClaudeCodeConfig()
	assertEnvContains(t, cfg.Env, "ANTHROPIC_BASE_URL=http://127.0.0.1:1234")
}

func TestClaudeCodeConfig_SetsAPIKey(t *testing.T) {
	cfg := ClaudeCodeConfig()
	assertEnvContains(t, cfg.Env, "ANTHROPIC_API_KEY=openkiro-proxy")
}

func TestClaudeCodeConfig_SetsDisableAutoupdater(t *testing.T) {
	cfg := ClaudeCodeConfig()
	assertEnvContains(t, cfg.Env, "DISABLE_AUTOUPDATER=1")
}

func TestClaudeCodeConfig_SetsNodeNoWarnings(t *testing.T) {
	cfg := ClaudeCodeConfig()
	assertEnvContains(t, cfg.Env, "NODE_NO_WARNINGS=1")
}

func TestClaudeCodeConfig_UsesBridgeNetwork(t *testing.T) {
	cfg := ClaudeCodeConfig()
	if cfg.NetworkMode != "bridge" {
		t.Errorf("ClaudeCodeConfig NetworkMode = %q, want bridge", cfg.NetworkMode)
	}
}

func TestClaudeCodeConfig_UsesAgentImage(t *testing.T) {
	cfg := ClaudeCodeConfig()
	if cfg.Image != AgentImageName {
		t.Errorf("ClaudeCodeConfig Image = %q, want %q", cfg.Image, AgentImageName)
	}
}

// ---- KiroConfig tests ----

func TestKiroConfig_SetsAnthropicBaseURL(t *testing.T) {
	cfg := KiroConfig()
	assertEnvContains(t, cfg.Env, "ANTHROPIC_BASE_URL=http://127.0.0.1:1234")
}

func TestKiroConfig_SetsKiroProxy(t *testing.T) {
	cfg := KiroConfig()
	assertEnvContains(t, cfg.Env, "KIRO_PROXY=openkiro")
}

func TestKiroConfig_UsesBridgeNetwork(t *testing.T) {
	cfg := KiroConfig()
	if cfg.NetworkMode != "bridge" {
		t.Errorf("KiroConfig NetworkMode = %q, want bridge", cfg.NetworkMode)
	}
}

func TestKiroConfig_PreservesReadOnlyRoot(t *testing.T) {
	cfg := KiroConfig()
	if !cfg.ReadOnlyRoot {
		t.Error("KiroConfig should preserve ReadOnlyRoot=true")
	}
}

// ---- AgentImageName test ----

func TestAgentImageName(t *testing.T) {
	if AgentImageName == "" {
		t.Error("AgentImageName must not be empty")
	}
	if !strings.Contains(AgentImageName, "sandbox") {
		t.Errorf("AgentImageName %q should contain 'sandbox'", AgentImageName)
	}
}

// ---- helper ----

func assertEnvContains(t *testing.T, env []string, entry string) {
	t.Helper()
	for _, e := range env {
		if e == entry {
			return
		}
	}
	t.Errorf("env %v: missing entry %q", env, entry)
}
