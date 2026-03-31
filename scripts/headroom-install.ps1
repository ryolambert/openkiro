#Requires -Version 5.1
# headroom-install.ps1 — install the headroom-ai Python compression proxy.
#
# Usage:
#   .\scripts\headroom-install.ps1              Install headroom-ai[proxy]
#   .\scripts\headroom-install.ps1 -Full        Install headroom-ai[all]
#   .\scripts\headroom-install.ps1 -Check       Check if headroom is installed

param(
    [switch]$Full,
    [switch]$Check,
    [switch]$Help
)

$ErrorActionPreference = 'Stop'

$Package = if ($Full) { 'headroom-ai[all]' } else { 'headroom-ai[proxy]' }

# ── Help ──────────────────────────────────────────────────────────────────────
if ($Help) {
    Write-Host @"
Usage: .\scripts\headroom-install.ps1 [-Full] [-Check]
  -Full   Install headroom-ai[all] (includes evals, ML compression)
  -Check  Check if headroom is already installed
"@
    exit 0
}

# ── Check mode ────────────────────────────────────────────────────────────────
if ($Check) {
    if (Get-Command headroom -ErrorAction SilentlyContinue) {
        Write-Host "headroom is installed"
        exit 0
    } else {
        Write-Host "headroom is NOT installed"
        exit 1
    }
}

# ── Python detection ──────────────────────────────────────────────────────────
$Python = $null
foreach ($candidate in @('python3', 'python')) {
    if (Get-Command $candidate -ErrorAction SilentlyContinue) {
        $Python = $candidate
        break
    }
}

if (-not $Python) {
    Write-Error 'Python 3.10+ is required but not found. Install from https://www.python.org/downloads/'
    exit 1
}

# Verify Python version >= 3.10
$pyVer = & $Python -c "import sys; print(f'{sys.version_info.major}.{sys.version_info.minor}')"
$parts = $pyVer.Split('.')
$pyMajor = [int]$parts[0]
$pyMinor = [int]$parts[1]

if ($pyMajor -lt 3 -or ($pyMajor -eq 3 -and $pyMinor -lt 10)) {
    Write-Error "Python >= 3.10 required (found $pyVer)"
    exit 1
}

Write-Host "Using Python $pyVer ($Python)"

# ── Verify pip is available via the interpreter ───────────────────────────────
$PipCheck = & $Python -m pip --version 2>&1
if ($LASTEXITCODE -ne 0) {
    Write-Error "pip not available for $Python. Install pip first."
    exit 1
}

# ── Install ───────────────────────────────────────────────────────────────────
Write-Host "Installing $Package..."
& $Python -m pip install $Package

# ── Verify ────────────────────────────────────────────────────────────────────
if (Get-Command headroom -ErrorAction SilentlyContinue) {
    Write-Host ''
    Write-Host 'headroom installed successfully'
    Write-Host ''
    Write-Host 'Quick start:'
    Write-Host '  headroom proxy                    # Start compression proxy on :8787'
    Write-Host '  headroom proxy --port 9000        # Custom port'
    Write-Host ''
    Write-Host 'Use with openkiro:'
    Write-Host '  openkiro server                   # Starts proxy on :1234 (uses headroom if available)'
} else {
    Write-Host ''
    Write-Host 'headroom was installed but is not on PATH.'
    Write-Host 'You may need to add your Python Scripts directory to PATH.'
}
