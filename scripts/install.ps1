#Requires -Version 5.1
# install.ps1 — install openkiro on Windows
$ErrorActionPreference = 'Stop'

$MinGoMajor = 1
$MinGoMinor = 23
$Pkg = 'github.com/ryolambert/openkiro@latest'

# ── Go detection / install ────────────────────────────────────────────────────
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    if (Get-Command winget -ErrorAction SilentlyContinue) {
        Write-Host 'Go not found — installing via winget...'
        winget install GoLang.Go --silent --accept-package-agreements --accept-source-agreements
        # Refresh PATH from registry
        $env:PATH = [System.Environment]::GetEnvironmentVariable('PATH', 'Machine') + ';' +
                    [System.Environment]::GetEnvironmentVariable('PATH', 'User')
    } else {
        Write-Error 'Go not found. Install from https://go.dev/dl/'
        exit 1
    }
}

# ── Version check ─────────────────────────────────────────────────────────────
$goVerStr = (go version) -replace '.*go(\d+\.\d+).*', '$1'
$parts = $goVerStr.Split('.')
$goMajor = [int]$parts[0]
$goMinor = [int]$parts[1]
if ($goMajor -lt $MinGoMajor -or ($goMajor -eq $MinGoMajor -and $goMinor -lt $MinGoMinor)) {
    Write-Error "Go >= $MinGoMajor.$MinGoMinor required (found $goVerStr)"
    exit 1
}

# ── Install ───────────────────────────────────────────────────────────────────
Write-Host "Installing $Pkg..."
go install $Pkg

# ── PATH setup ────────────────────────────────────────────────────────────────
$GoBin = (go env GOPATH) + '\bin'
$userPath = [System.Environment]::GetEnvironmentVariable('PATH', 'User')
if ($null -eq $userPath) { $userPath = '' }
if ($userPath -notlike "*$GoBin*") {
    $newPath = if ($userPath) { "$userPath;$GoBin" } else { $GoBin }
    [System.Environment]::SetEnvironmentVariable('PATH', $newPath, 'User')
    $env:PATH += ";$GoBin"
    Write-Host "Added $GoBin to user PATH"
}

Write-Host 'openkiro installed successfully. Open a new terminal to use it.'
