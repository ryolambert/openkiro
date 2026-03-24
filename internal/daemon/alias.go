package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	aliasMarkerBegin = "# openkiro-alias-begin"
	aliasMarkerEnd   = "# openkiro-alias-end"
)

// DefaultAliasNames returns the default alias names.
func DefaultAliasNames() []string {
	return []string{"okcc", "oklaude"}
}

// DetectShell returns the current shell type: bash, zsh, powershell, or cmd.
func DetectShell() string {
	if runtime.GOOS == "windows" {
		return "powershell"
	}
	sh := os.Getenv("SHELL")
	if strings.Contains(sh, "zsh") {
		return "zsh"
	}
	return "bash"
}

// GenerateBashAlias returns a bash/zsh function snippet with markers (single alias, for display).
func GenerateBashAlias(aliasName, openkiroPath, port string) string {
	return fmt.Sprintf("%s\n%s\n%s\n", aliasMarkerBegin, bashAliasBody(aliasName, openkiroPath, port), aliasMarkerEnd)
}

func bashAliasBody(aliasName, openkiroPath, port string) string {
	return fmt.Sprintf(`%s() {
  if ! curl -sf "http://localhost:%s/health" >/dev/null 2>&1; then
    %s start
    sleep 1
  fi
  ANTHROPIC_BASE_URL="http://localhost:%s" ANTHROPIC_API_KEY="$(%s token)" claude "$@"
}`, aliasName, port, openkiroPath, port, openkiroPath)
}

// GeneratePowerShellAlias returns a PowerShell function snippet with markers (single alias, for display).
func GeneratePowerShellAlias(aliasName, openkiroPath, port string) string {
	return fmt.Sprintf("%s\n%s\n%s\n", aliasMarkerBegin, powerShellAliasBody(aliasName, openkiroPath, port), aliasMarkerEnd)
}

func powerShellAliasBody(aliasName, openkiroPath, port string) string {
	return fmt.Sprintf(`function %s {
  try { Invoke-RestMethod -Uri "http://localhost:%s/health" -ErrorAction Stop | Out-Null } catch { & %s start; Start-Sleep 1 }
  $env:ANTHROPIC_BASE_URL = "http://localhost:%s"
  $env:ANTHROPIC_API_KEY = (& %s token)
  & claude @args
}`, aliasName, port, openkiroPath, port, openkiroPath)
}

// GenerateCmdBat returns a .bat wrapper script content.
func GenerateCmdBat(aliasName, openkiroPath, port string) string {
	return fmt.Sprintf(`@echo off
REM %s
curl -sf "http://localhost:%s/health" >nul 2>&1
if errorlevel 1 (
  "%s" start
  timeout /t 1 /nobreak >nul
)
for /f "delims=" %%%%t in ('"%s" token') do set "ANTHROPIC_API_KEY=%%%%t"
set "ANTHROPIC_BASE_URL=http://localhost:%s"
claude %%*
`, aliasMarkerBegin, port, openkiroPath, openkiroPath, port)
}

var (
	psMarkerBegin = aliasMarkerBegin
	psMarkerEnd   = aliasMarkerEnd
)

// ShellConfigPath returns the config file path for the given shell type.
// Prefers ~/.bash_aliases / ~/.zsh_aliases if they exist.
func ShellConfigPath(shellType string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("shell config path: %w", err)
	}
	switch shellType {
	case "bash":
		if p := filepath.Join(home, ".bash_aliases"); pathExists(p) {
			return p, nil
		}
		return filepath.Join(home, ".bashrc"), nil
	case "zsh":
		if p := filepath.Join(home, ".zsh_aliases"); pathExists(p) {
			return p, nil
		}
		return filepath.Join(home, ".zshrc"), nil
	case "powershell":
		if runtime.GOOS == "windows" {
			return filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1"), nil
		}
		return filepath.Join(home, ".config", "powershell", "Microsoft.PowerShell_profile.ps1"), nil
	case "cmd":
		dir, err := CredentialsDir()
		if err != nil {
			return "", err
		}
		return dir, nil
	default:
		return "", fmt.Errorf("unsupported shell: %s", shellType)
	}
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// CredentialsDir returns ~/.openkiro/ (duplicated here to avoid import cycle).
func CredentialsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("credentials dir: %w", err)
	}
	return filepath.Join(home, ".openkiro"), nil
}

// HasAliasMarker checks if content already contains the openkiro alias marker.
func HasAliasMarker(content string) bool {
	return strings.Contains(content, aliasMarkerBegin)
}

// InstallAlias writes the snippet to the config file, replacing any existing openkiro block.
func InstallAlias(shellType, snippet string) (string, error) {
	if shellType == "cmd" {
		return installCmdBat(snippet)
	}
	configPath, err := ShellConfigPath(shellType)
	if err != nil {
		return "", err
	}
	existing, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("read config: %w", err)
	}
	content := string(existing)
	if HasAliasMarker(content) {
		content = replaceAliasBlock(content, snippet)
	} else {
		content += "\n" + snippet
	}
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write config: %w", err)
	}
	return configPath, nil
}

// replaceAliasBlock replaces the existing marker block with the new snippet.
func replaceAliasBlock(content, snippet string) string {
	start := strings.Index(content, aliasMarkerBegin)
	end := strings.Index(content, aliasMarkerEnd)
	if start == -1 || end == -1 {
		return content + "\n" + snippet
	}
	return content[:start] + snippet + content[end+len(aliasMarkerEnd):]
}

func installCmdBat(snippet string) (string, error) {
	dir, err := CredentialsDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}
	batPath := filepath.Join(dir, "okcc.bat")
	if err := os.WriteFile(batPath, []byte(snippet), 0o644); err != nil {
		return "", fmt.Errorf("write bat: %w", err)
	}
	return batPath, nil
}

// GenerateAliases generates a single marker block with env source line + all alias functions.
func GenerateAliases(shellType, openkiroPath, port string, names []string) string {
	var b strings.Builder
	b.WriteString(aliasMarkerBegin + "\n")
	switch shellType {
	case "bash", "zsh":
		b.WriteString("[ -f ~/.openkiro/env.sh ] && . ~/.openkiro/env.sh\n")
		for _, name := range names {
			b.WriteString(bashAliasBody(name, openkiroPath, port) + "\n")
		}
	case "powershell":
		b.WriteString("$_envFile = Join-Path $HOME '.openkiro/env.ps1'; if (Test-Path $_envFile) { . $_envFile }\n")
		for _, name := range names {
			b.WriteString(powerShellAliasBody(name, openkiroPath, port) + "\n")
		}
	case "cmd":
		for _, name := range names {
			b.WriteString(GenerateCmdBat(name, openkiroPath, port))
		}
	}
	b.WriteString(aliasMarkerEnd + "\n")
	return b.String()
}
