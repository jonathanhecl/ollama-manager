#!/usr/bin/env pwsh
param([switch]$Release)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$projectDir = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location -Path $projectDir

$outputName = "ollama-manager.exe"
$buildFlags = @()

if ($Release) {
    $buildFlags += @("-ldflags", "-s -w")
    Write-Host "Building RELEASE binary..." -ForegroundColor Cyan
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
