package daemon

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestGenerateBashAlias(t *testing.T) {
	tests := []struct {
		name      string
		aliasName string
		binPath   string
		port      string
		wantSubs  []string
	}{
		{
			"default okcc",
			"okcc", "openkiro", "1234",
			[]string{"okcc()", "curl -sf", "localhost:1234/health", "openkiro start", `ANTHROPIC_BASE_URL="http://localhost:1234"`, `ANTHROPIC_API_KEY="$(openkiro token)"`, `claude "$@"`},
		},
		{
			"custom name and port",
			"myclaud", "/usr/local/bin/openkiro", "9000",
			[]string{"myclaud()", "localhost:9000/health", `ANTHROPIC_BASE_URL="http://localhost:9000"`, `/usr/local/bin/openkiro token`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateBashAlias(tt.aliasName, tt.binPath, tt.port)
			for _, sub := range tt.wantSubs {
				if !strings.Contains(got, sub) {
					t.Errorf("output missing %q\ngot:\n%s", sub, got)
				}
			}
		})
	}
}

func TestGeneratePowerShellAlias(t *testing.T) {
	got := GeneratePowerShellAlias("okcc", "openkiro", "1234")
	for _, sub := range []string{"function okcc", "Invoke-RestMethod", "localhost:1234/health", "openkiro start", "claude @args"} {
		if !strings.Contains(got, sub) {
			t.Errorf("output missing %q\ngot:\n%s", sub, got)
		}
	}
}

func TestGenerateCmdBat(t *testing.T) {
	got := GenerateCmdBat("okcc", "openkiro", "1234")
	for _, sub := range []string{"@echo off", "localhost:1234/health", "openkiro", "claude"} {
		if !strings.Contains(got, sub) {
			t.Errorf("output missing %q\ngot:\n%s", sub, got)
		}
	}
}

func TestDefaultAliasNames(t *testing.T) {
	names := DefaultAliasNames()
	if len(names) != 2 || names[0] != "okcc" || names[1] != "oklaude" {
		t.Errorf("DefaultAliasNames() = %v, want [okcc oklaude]", names)
	}
}

func TestGenerateAliasesMultipleNames(t *testing.T) {
	got := GenerateAliases("bash", "openkiro", "1234", []string{"okcc", "oklaude"})
	if !strings.Contains(got, "okcc()") || !strings.Contains(got, "oklaude()") {
		t.Errorf("expected both aliases in output:\n%s", got)
	}
}

func TestHasAliasMarker(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"present", "some stuff\n# openkiro-alias-begin\nfoo\n# openkiro-alias-end\n", true},
		{"absent", "some stuff\n", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasAliasMarker(tt.content); got != tt.want {
				t.Errorf("HasAliasMarker() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInstallAliasReplacesExisting(t *testing.T) {
	existing := "# existing config\n# openkiro-alias-begin\nokcc() { echo old; }\n# openkiro-alias-end\n"
	newSnippet := "# openkiro-alias-begin\nokcc() { echo new; }\n# openkiro-alias-end\n"
	result := replaceAliasBlock(existing, newSnippet)
	if strings.Contains(result, "echo old") {
		t.Error("old alias block should have been replaced")
	}
	if !strings.Contains(result, "echo new") {
		t.Error("new alias block should be present")
	}
	if strings.Count(result, aliasMarkerBegin) != 1 {
		t.Errorf("expected exactly one marker block, got:\n%s", result)
	}
}

func TestDetectShell(t *testing.T) {
	// Save and restore
	orig := os.Getenv("SHELL")
	defer func() { os.Setenv("SHELL", orig) }()

	os.Setenv("SHELL", "/bin/zsh")
	wantZsh := "zsh"
	wantBash := "bash"
	if runtime.GOOS == "windows" {
		wantZsh = "powershell"
		wantBash = "powershell"
	}
	if got := DetectShell(); got != wantZsh {
		t.Errorf("DetectShell() = %q, want %s", got, wantZsh)
	}
	os.Setenv("SHELL", "/bin/bash")
	if got := DetectShell(); got != wantBash {
		t.Errorf("DetectShell() = %q, want %s", got, wantBash)
	}
}
