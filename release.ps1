#!/usr/bin/env pwsh
#requires -Version 5.1
<#
.SYNOPSIS
    Compiles binaries locally, tags the code, pushes commits & tags, and creates a GitHub Release uploading the assets.
.EXAMPLE
    ./release.ps1 v0.1.0
#>
param(
    [Parameter(Mandatory=$true, Position=0)]
    [string]$Version
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

# 1. Validate version format (e.g., v1.0.0)
if ($Version -notmatch '^v\d+\.\d+\.\d+(-[a-zA-Z0-9.]+)?$') {
    Write-Host "Error: La versión debe tener el formato vX.Y.Z (ej. v1.0.0)" -ForegroundColor Red
    exit 1
}

# 2. Check for Git repository
if (-not (Test-Path .git)) {
    Write-Host "Error: Este script debe ser ejecutado en la raíz del repositorio de Git." -ForegroundColor Red
    exit 1
}

# 3. Check for uncommitted changes
$status = git status --porcelain
if ($status) {
    Write-Host "Error: Hay cambios locales sin commitear en el repositorio:" -ForegroundColor Red
    Write-Host $status -ForegroundColor Yellow
    Write-Host "Por favor, haz commit o stash antes de continuar." -ForegroundColor Red
    exit 1
}

# 4. Get current branch
$branch = (git branch --show-current).Trim()
if ([string]::IsNullOrEmpty($branch)) {
    Write-Host "Error: No se pudo determinar la rama actual (¿estás en estado HEAD separado?)." -ForegroundColor Red
    exit 1
}

# 5. Check if tag already exists locally
$tagExists = git tag -l $Version
if ($tagExists) {
    Write-Host "Error: El tag '$Version' ya existe localmente." -ForegroundColor Red
    exit 1
}

# 6. Parse Owner and Repo from remote origin URL
$remoteUrl = (git remote get-url origin).Trim()
if ($remoteUrl -match 'github\.com[:/]([^/]+)/([^/.]+?)(\.git)?$') {
    $owner = $Matches[1]
    $repo = $Matches[2]
} else {
    Write-Host "Error: No se pudo determinar el propietario/repositorio de GitHub desde la URL de remote origin: $remoteUrl" -ForegroundColor Red
    exit 1
}

# 7. Get GitHub Personal Access Token (PAT)
$token = $env:GITHUB_TOKEN
if ([string]::IsNullOrEmpty($token)) {
    Write-Host "GitHub Personal Access Token (GITHUB_TOKEN) no detectado en el entorno." -ForegroundColor Yellow
    Write-Host "Por favor, introduce tu Token de GitHub (con permisos de lectura/escritura de repositorios/releases):" -ForegroundColor Yellow
    $token = Read-Host -AsSecureString
    if (-not $token) {
        Write-Host "Error: Se requiere un token de GitHub para subir el release." -ForegroundColor Red
        exit 1
    }
    # Convert SecureString to plain string for API requests
    $BSTR = [System.Runtime.InteropServices.Marshal]::SecureStringToBSTR($token)
    $token = [System.Runtime.InteropServices.Marshal]::PtrToStringAuto($BSTR)
}

Write-Host "=============================================" -ForegroundColor Cyan
Write-Host "   PREPARANDO LANZAMIENTO LOCAL DE VERSION   " -ForegroundColor Cyan
Write-Host "=============================================" -ForegroundColor Cyan
Write-Host "Versión:        $Version" -ForegroundColor Gray
Write-Host "Repositorio:    $owner/$repo" -ForegroundColor Gray
Write-Host "Rama origen:    $branch" -ForegroundColor Gray
Write-Host "=============================================" -ForegroundColor Cyan
Write-Host ""

$confirmation = Read-Host "¿Quieres continuar con la compilación local y subida del Release a GitHub? (y/n)"
if ($confirmation -ne "y" -and $confirmation -ne "si" -and $confirmation -ne "yes") {
    Write-Host "Operación cancelada." -ForegroundColor Yellow
    exit 0
}

# 8. Run local compilation and packaging
Write-Host "`n[1/4] Compilando binarios locales multiplataforma..." -ForegroundColor Cyan
$buildScript = Join-Path $PSScriptRoot "build-all.ps1"
if (-not (Test-Path $buildScript)) {
    Write-Host "Error: No se encontró el script de compilación local '$buildScript'." -ForegroundColor Red
    exit 1
}

& $buildScript -Version $Version
if ($LASTEXITCODE -ne 0) {
    Write-Host "Error: La compilación de binarios locales falló." -ForegroundColor Red
    exit 1
}

# 9. Create Git tag and push commits/tags
Write-Host "`n[2/4] Creando tag local y empujando a GitHub..." -ForegroundColor Cyan
try {
    Write-Host "Creando tag local $Version..." -ForegroundColor DarkGray
    git tag $Version
    
    Write-Host "Subiendo commits de la rama '$branch' a origin..." -ForegroundColor DarkGray
    git push origin $branch
    
    Write-Host "Subiendo tag '$Version' a origin..." -ForegroundColor DarkGray
    git push origin $Version
} catch {
    Write-Host "Error: Falló alguna operación de git. Abortando la creación del release en GitHub." -ForegroundColor Red
    # Intenta borrar el tag local para evitar inconsistencias si falló la subida
    git tag -d $Version 2>&1 | Out-Null
    exit 1
}

# 10. Create Release in GitHub via API
Write-Host "`n[3/4] Creando Release en la API de GitHub..." -ForegroundColor Cyan
$releaseUrl = "https://api.github.com/repos/$owner/$repo/releases"
$headers = @{
    "Authorization"        = "Bearer $token"
    "Accept"               = "application/vnd.github+json"
    "X-GitHub-Api-Version" = "2022-11-28"
}

$releaseBody = @{
    tag_name               = $Version
    target_commitish       = $branch
    name                   = "Release $Version"
    body                   = "Release $Version generado y subido automáticamente por release.ps1"
    draft                  = $false
    prerelease             = $false
    generate_release_notes = $true
} | ConvertTo-Json

try {
    $response = Invoke-RestMethod -Uri $releaseUrl -Method Post -Headers $headers -Body $releaseBody -ContentType "application/json; charset=utf-8"
    $uploadUrlTemplate = $response.upload_url
    $htmlUrl = $response.html_url
    # Clean the template tag {?name,label}
    $uploadUrlBase = $uploadUrlTemplate -replace '\{.*?\}', ''
    Write-Host "Release creado exitosamente en GitHub: $htmlUrl" -ForegroundColor Green
} catch {
    Write-Host "Error al crear el release en GitHub: $_" -ForegroundColor Red
    if ($_.Exception.Response) {
        $reader = New-Object System.IO.StreamReader($_.Exception.Response.GetResponseStream())
        $responseBody = $reader.ReadToEnd()
        Write-Host "Detalle del error de la API de GitHub: $responseBody" -ForegroundColor Red
    }
    Write-Host "Por favor, borra el tag de git remoto si deseas reintentar: git push origin --delete $Version" -ForegroundColor Yellow
    exit 1
}

# 11. Upload packages as release assets
Write-Host "`n[4/4] Subiendo binarios empaquetados como assets..." -ForegroundColor Cyan
$distDir = Join-Path $PSScriptRoot "dist"
$assets = Get-ChildItem -Path $distDir -File

if ($assets.Count -eq 0) {
    Write-Host "Error: No se encontraron archivos empaquetados en la carpeta dist/." -ForegroundColor Red
    exit 1
}

foreach ($asset in $assets) {
    $fileName = $asset.Name
    $filePath = $asset.FullName
    Write-Host "Subiendo asset: $fileName..." -ForegroundColor Cyan

    $uploadUrl = "${uploadUrlBase}?name=$fileName"
    
    $uploadHeaders = @{
        "Authorization"        = "Bearer $token"
        "Accept"               = "application/vnd.github+json"
        "X-GitHub-Api-Version" = "2022-11-28"
        "Content-Type"         = "application/octet-stream"
    }

    try {
        $uploadResponse = Invoke-RestMethod -Uri $uploadUrl -Method Post -Headers $uploadHeaders -InFile $filePath
        Write-Host "¡Subida exitosa: $fileName!" -ForegroundColor Green
    } catch {
        Write-Host "Error al subir $fileName: $_" -ForegroundColor Red
        if ($_.Exception.Response) {
            $reader = New-Object System.IO.StreamReader($_.Exception.Response.GetResponseStream())
            $responseBody = $reader.ReadToEnd()
            Write-Host "Detalle del error de subida: $responseBody" -ForegroundColor Red
        }
    }
}

Write-Host "`n¡Proceso de lanzamiento completado!" -ForegroundColor Green
Write-Host "Tu release ya está disponible en: $htmlUrl" -ForegroundColor Green
