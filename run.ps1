#!/usr/bin/env pwsh
param([switch]$Release)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$projectDir = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location -Path $projectDir

# Ensure we build for native Windows even if a previous build script set GOOS/GOARCH
$env:GOOS = "windows"
if (-not $env:GOARCH -or $env:GOARCH -eq "arm64") {
    $env:GOARCH = "amd64"
}

$outputName = "ollama-manager.exe"
$buildFlags = @()

$appVersion = ""
try { $appVersion = (git describe --tags --abbrev=0 2>$null).Trim() } catch {}
$versionFlags = ""
if (-not [string]::IsNullOrEmpty($appVersion)) {
    $versionFlags = " -X 'main.appVersion=$appVersion'"
}

if ($Release) {
    $buildFlags += @("-ldflags", "-s -w$versionFlags")
    Write-Host "Building RELEASE binary ($appVersion)..." -ForegroundColor Cyan
} elseif (-not [string]::IsNullOrEmpty($versionFlags)) {
    $buildFlags += @("-ldflags", $versionFlags.Trim())
    Write-Host "Building DEBUG binary ($appVersion)..." -ForegroundColor Cyan
} else {
    Write-Host "Building DEBUG binary..." -ForegroundColor Cyan
}

Write-Host "go build $($buildFlags -join ' ') -o $outputName ." -ForegroundColor DarkGray
& go build @buildFlags -o $outputName .

if ($LASTEXITCODE -ne 0) {
    Write-Host "Build failed." -ForegroundColor Red
    exit 1
}

Write-Host "Build succeeded. Starting $outputName..." -ForegroundColor Green
& "./$outputName"
