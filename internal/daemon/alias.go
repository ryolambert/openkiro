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

// GenerateBashAlias returns a bash/zsh function snippet for the given alias name.
func GenerateBashAlias(aliasName, openkiroPath, port string) string {
	return fmt.Sprintf(`%s
%s() {
  if ! curl -sf "http://localhost:%s/health" >/dev/null 2>&1; then
    %s start
    sleep 1
  fi
  eval "$(%s env --port %s)"
  claude "$@"
}
%s
`, aliasMarkerBegin, aliasName, port, openkiroPath, openkiroPath, port, aliasMarkerEnd)
}

// GeneratePowerShellAlias returns a PowerShell function snippet.
func GeneratePowerShellAlias(aliasName, openkiroPath, port string) string {
	return fmt.Sprintf(`%s
function %s {
  try { Invoke-RestMethod -Uri "http://localhost:%s/health" -ErrorAction Stop | Out-Null } catch { & %s start; Start-Sleep 1 }
  & %s env --port %s | ForEach-Object { if ($_ -match '^(\w+)=(.*)$') { [Environment]::SetEnvironmentVariable($Matches[1], $Matches[2], 'Process') } }
  & claude @args
}
%s
`, psMarkerBegin, aliasName, port, openkiroPath, openkiroPath, port, psMarkerEnd)
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
for /f "tokens=1,* delims==" %%%%a in ('%s env --port %s') do set "%%%%a=%%%%b"
claude %%*
`, aliasMarkerBegin, port, openkiroPath, openkiroPath, port)
}

var (
	psMarkerBegin = aliasMarkerBegin
	psMarkerEnd   = aliasMarkerEnd
)

// ShellConfigPath returns the config file path for the given shell type.
func ShellConfigPath(shellType string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("shell config path: %w", err)
	}
	switch shellType {
	case "bash":
		return filepath.Join(home, ".bashrc"), nil
	case "zsh":
		return filepath.Join(home, ".zshrc"), nil
	case "powershell":
		if runtime.GOOS == "windows" {
			docs := filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1")
			return docs, nil
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

// InstallAlias appends the snippet to the config file, avoiding duplicates.
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
	if HasAliasMarker(string(existing)) {
		return configPath, fmt.Errorf("alias already installed in %s (remove the %s block first)", configPath, aliasMarkerBegin)
	}
	f, err := os.OpenFile(configPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("open config: %w", err)
	}
	if _, err := fmt.Fprintf(f, "\n%s", snippet); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("write config: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close config: %w", err)
	}
	return configPath, nil
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

// GenerateAliases generates all alias snippets for the given shell and names.
func GenerateAliases(shellType, openkiroPath, port string, names []string) string {
	var b strings.Builder
	for _, name := range names {
		switch shellType {
		case "bash", "zsh":
			b.WriteString(GenerateBashAlias(name, openkiroPath, port))
		case "powershell":
			b.WriteString(GeneratePowerShellAlias(name, openkiroPath, port))
		case "cmd":
			b.WriteString(GenerateCmdBat(name, openkiroPath, port))
		}
	}
	return b.String()
}
