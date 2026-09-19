#!/usr/bin/env pwsh

[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

# Build the reviewed Admin Console, refresh the Go embed, then produce a
# small, static Windows binary. Override environment variables as needed.
$root = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$frontend = Join-Path $root "frontend"
$dist = Join-Path $frontend "dist"
$embed = Join-Path $root "internal\admin\ui"
$goos = if ($env:GOOS) { $env:GOOS } else { "windows" }
$goarch = if ($env:GOARCH) { $env:GOARCH } else { "amd64" }
$output = if ($env:JANUS_OUTPUT) { $env:JANUS_OUTPUT } else { Join-Path $root "bin\janus.exe" }

function Require-Command([string]$Name) {
  if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) { throw "$Name is required" }
}

Require-Command "node"
Require-Command "npm"
Require-Command "go"

if ($goos -eq "windows" -and -not $output.EndsWith(".exe", [StringComparison]::OrdinalIgnoreCase)) { $output = "$output.exe" }

Write-Host "==> building Admin Console"
$nodeModules = Join-Path $frontend "node_modules"
if (($env:JANUS_INSTALL_DEPS -eq "1") -or -not (Test-Path $nodeModules)) {
  Push-Location $frontend
  try { npm ci } finally { Pop-Location }
}
Push-Location $frontend
try { npm run build } finally { Pop-Location }

if (-not (Test-Path (Join-Path $dist "index.html"))) { throw "frontend build did not produce dist/index.html" }
$distAssets = Join-Path $dist "assets"
if (-not (Test-Path $distAssets)) { throw "frontend build did not produce dist/assets" }

Write-Host "==> synchronizing embedded Admin Console"
$embedAssets = Join-Path $embed "assets"
New-Item -ItemType Directory -Force -Path $embedAssets | Out-Null
Get-ChildItem -Path $embedAssets -File -Force -ErrorAction SilentlyContinue | Remove-Item -Force
Copy-Item (Join-Path $dist "index.html") (Join-Path $embed "index.html") -Force
Copy-Item (Join-Path $distAssets "*") $embedAssets -Force

function Get-Hash([string]$Path) { return (Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash }

$distFiles = @(Get-ChildItem -Path $distAssets -File | Sort-Object Name)
$embedFiles = @(Get-ChildItem -Path $embedAssets -File | Sort-Object Name)
if (($distFiles.Name -join "`n") -ne ($embedFiles.Name -join "`n")) { throw "embedded asset names differ from frontend/dist" }
if ((Get-Hash (Join-Path $dist "index.html")) -ne (Get-Hash (Join-Path $embed "index.html"))) { throw "embedded index.html differs from frontend/dist" }
foreach ($file in $distFiles) {
  if ((Get-Hash $file.FullName) -ne (Get-Hash (Join-Path $embedAssets $file.Name))) { throw "embedded asset differs: $($file.Name)" }
}
Write-Host "embedded Admin Console matches frontend/dist"

$outputDir = Split-Path -Parent $output
if ([string]::IsNullOrWhiteSpace($outputDir)) { $outputDir = "." }
New-Item -ItemType Directory -Force -Path $outputDir | Out-Null
Write-Host "==> building $goos/$goarch -> $output"
$previousCgo = $env:CGO_ENABLED
$previousGoos = $env:GOOS
$previousGoarch = $env:GOARCH
$env:CGO_ENABLED = "0"
$env:GOOS = $goos
$env:GOARCH = $goarch
try {
  & go build -trimpath -buildvcs=false '-ldflags=-s -w -buildid=' -o $output ./cmd/janus
} finally {
  if ($null -eq $previousCgo) { Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue } else { $env:CGO_ENABLED = $previousCgo }
  if ($null -eq $previousGoos) { Remove-Item Env:GOOS -ErrorAction SilentlyContinue } else { $env:GOOS = $previousGoos }
  if ($null -eq $previousGoarch) { Remove-Item Env:GOARCH -ErrorAction SilentlyContinue } else { $env:GOARCH = $previousGoarch }
}

if ($env:JANUS_UPX -eq "1") {
  Require-Command "upx"
  Write-Host "==> applying optional UPX compression"
  & upx --best --lzma $output
}

if (-not (Test-Path $output)) { throw "Go build did not produce $output" }
$hash = Get-Hash $output
$bytes = (Get-Item $output).Length
Write-Host "SHA-256 $hash"
Write-Host "built $output ($bytes bytes)"
