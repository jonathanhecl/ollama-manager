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
    [string]$Output = "ollama-manager"
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
$ldflags = "-s -w -X 'main.buildTime=$buildTime'"

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