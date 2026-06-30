#!/usr/bin/env pwsh
#requires -Version 5.1
<#
.SYNOPSIS
    Compiles and packages ollama-manager for all major operating systems and architectures.
.EXAMPLE
    ./build-all.ps1
    ./build-all.ps1 -Version v1.0.0
#>
param(
    [string]$Version = ""
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

# Verify Go is installed
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Write-Host "Error: Go no está instalado o no está en el PATH." -ForegroundColor Red
    exit 1
}

$distDir = Join-Path $PSScriptRoot "dist"
if (Test-Path $distDir) {
    Write-Host "Limpiando directorio dist/ anterior..." -ForegroundColor DarkGray
    Remove-Item -Recurse -Force $distDir
}
New-Item -ItemType Directory -Path $distDir | Out-Null

$buildTime = (Get-Date -Format "yyyy-MM-dd HH:mm:ss")
$ldflags = "-s -w -X 'main.buildTime=$buildTime'"

# Target matrix: OS, Arch, Packaging Format
$targets = @(
    @{ OS = "windows"; Arch = "amd64"; Format = "zip" },
    @{ OS = "windows"; Arch = "arm64"; Format = "zip" },
    @{ OS = "linux";   Arch = "amd64"; Format = "targz" },
    @{ OS = "linux";   Arch = "arm64"; Format = "targz" },
    @{ OS = "darwin";  Arch = "amd64"; Format = "targz" },
    @{ OS = "darwin";  Arch = "arm64"; Format = "targz" }
)

Write-Host "Iniciando compilación multiplataforma..." -ForegroundColor Cyan

foreach ($target in $targets) {
    $os = $target.OS
    $arch = $target.Arch
    $format = $target.Format
    
    # Executable name inside the compressed package (always generic ollama-manager)
    $binaryName = if ($os -eq "windows") { "ollama-manager.exe" } else { "ollama-manager" }
    
    # Compressed file name: e.g. ollama-manager-v1.0.0-windows-amd64.zip
    $packBase = "ollama-manager"
    if (-not [string]::IsNullOrEmpty($Version)) {
        $packBase += "-$Version"
    }
    $packName = "$packBase-$os-$arch"
    if ($format -eq "zip") {
        $packName += ".zip"
    } else {
        $packName += ".tar.gz"
    }
    
    $packPath = Join-Path $distDir $packName

    Write-Host "Compilando para $os ($arch)..." -ForegroundColor Cyan

    # Create temporary build folder to isolate the compiled binary with generic name
    $tempDir = Join-Path $distDir "temp_${os}_$arch"
    if (Test-Path $tempDir) {
        Remove-Item -Recurse -Force $tempDir
    }
    New-Item -ItemType Directory -Path $tempDir | Out-Null
    
    $binaryPath = Join-Path $tempDir $binaryName

    $env:CGO_ENABLED = "0"
    $env:GOOS      = $os
    $env:GOARCH    = $arch

    $goArgs = @(
        "-trimpath"
        "-ldflags=$ldflags"
        "-o", $binaryPath
        "."
    )

    # Run build
    & go build @goArgs

    if ($LASTEXITCODE -ne 0) {
        Write-Host "Error: Compilación fallida para $os/$arch" -ForegroundColor Red
        Remove-Item -Recurse -Force $tempDir
        exit 1
    }

    # Packaging
    Write-Host "Empaquetando en $packName (binario interno: $binaryName)..." -ForegroundColor DarkGray
    if ($format -eq "zip") {
        # Use Compress-Archive directly on the binary file
        Compress-Archive -Path $binaryPath -DestinationPath $packPath -Force
    } else {
        # Linux/macOS packages: use tar.exe if available, otherwise Compress-Archive as .zip as fallback
        if (Get-Command tar -ErrorAction SilentlyContinue) {
            Push-Location $tempDir
            & tar.exe -czf $packPath $binaryName
            Pop-Location
        } else {
            Write-Host "Advertencia: tar.exe no encontrado. Empaquetando como ZIP en su lugar." -ForegroundColor Yellow
            $fallbackPackName = $packName -replace '\.tar\.gz$', '.zip'
            $fallbackPackPath = Join-Path $distDir $fallbackPackName
            Compress-Archive -Path $binaryPath -DestinationPath $fallbackPackPath -Force
        }
    }

    # Clean up the temporary directory
    Remove-Item -Recurse -Force $tempDir
}

Write-Host ""
Write-Host "Compilación y empaquetado completados exitosamente. Archivos en dist/:" -ForegroundColor Green
Get-ChildItem $distDir | Select-Object Name, Length | Format-Table
