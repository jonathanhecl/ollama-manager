#!/usr/bin/env pwsh
#requires -Version 7
<#
.SYNOPSIS
    Cross-compiles ollama-manager for macOS (Apple Silicon by default).
.EXAMPLE
    ./build-mac.ps1
    ./build-mac.ps1 -Arch amd64 -Output ./bin/ollama-manager-mac
#>
param(
    [string]$Arch = "arm64",
    [string]$Output = "ollama-manager",
    [string]$Version = ""
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

# Verify Go is installed
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Write-Host "Error: Go is not installed or not in PATH." -ForegroundColor Red
    exit 1
}

$env:CGO_ENABLED = "0"
$env:GOOS      = "darwin"
$env:GOARCH    = $Arch

$buildTime = (Get-Date).ToString("yyyy-MM-dd HH:mm:ss")
$appVersion = $Version
if ([string]::IsNullOrEmpty($appVersion)) {
    try { $appVersion = (git describe --tags --abbrev=0 2>$null).Trim() } catch {}
}
$ldflags = "-s -w -X 'main.buildTime=$buildTime'"
if (-not [string]::IsNullOrEmpty($appVersion)) {
    $ldflags += " -X 'main.appVersion=$appVersion'"
}

Write-Host "Building ollama-manager for macOS ($Arch)..." -ForegroundColor Cyan
Write-Host "  GOOS    = $env:GOOS" -ForegroundColor DarkGray
Write-Host "  GOARCH  = $env:GOARCH" -ForegroundColor DarkGray
Write-Host "  Output  = $Output" -ForegroundColor DarkGray
Write-Host "  LDFLAGS = $ldflags" -ForegroundColor DarkGray
Write-Host ""

$goArgs = @(
    "-trimpath"
    "-ldflags=$ldflags"
    "-o", $Output
    "."
)
& go build @goArgs

if ($LASTEXITCODE -ne 0) {
    Write-Host "Build failed." -ForegroundColor Red
    exit 1
}

Write-Host "Build succeeded: $Output" -ForegroundColor Green

# Verify the produced binary really targets the requested architecture.
# Catches mistakes like cross-checking out the wrong -Arch for the Mac
# (an Intel binary fails on Apple Silicon without Rosetta with
# "bad CPU type in executable", and vice versa).
$buildInfo = & go version -m $Output 2>&1
if ($LASTEXITCODE -ne 0) {
    Write-Host "Warning: could not inspect '$Output' with 'go version -m'." -ForegroundColor Yellow
} elseif (-not ($buildInfo | Select-String -Pattern "GOARCH=$Arch\b" -Quiet)) {
    $found = ($buildInfo | Select-String -Pattern 'GOARCH=\S+' | Select-Object -First 1).Matches.Value
    Write-Host "Error: '$Output' reports $found, but -Arch $Arch was requested. Aborting to avoid shipping the wrong CPU type." -ForegroundColor Red
    exit 1
}

if ($Arch -eq "arm64") {
    Write-Host "Target: Apple Silicon Macs (M1/M2/M3/M4). Will NOT run on Intel Macs." -ForegroundColor DarkGray
} else {
    Write-Host "Target: Intel Macs. Apple Silicon Macs need Rosetta 2 installed or they fail with 'bad CPU type in executable'." -ForegroundColor Yellow
}