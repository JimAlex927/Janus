# Build the reviewed Admin Console, refresh the Go embed, then produce a
# small, static Janus binary. Override JANUS_OUTPUT/GOOS/GOARCH as needed.
# Mirrors scripts/build-app.sh for Windows hosts.
$ErrorActionPreference = 'Stop'

$Root     = Split-Path -Parent (Split-Path -Parent $PSCommandPath)
$Frontend = Join-Path $Root 'frontend'
$Dist     = Join-Path $Frontend 'dist'
$Embed    = Join-Path $Root 'internal\admin\ui'

$Goos   = if ($env:GOOS)   { $env:GOOS }   else { (go env GOOS) }
$Goarch = if ($env:GOARCH) { $env:GOARCH } else { (go env GOARCH) }
$Output = if ($env:JANUS_OUTPUT) { $env:JANUS_OUTPUT } else { Join-Path $Root 'bin\janus' }
$UiBase = if ($env:JANUS_UI_BASE_URL) { $env:JANUS_UI_BASE_URL } else { '' }

function Fail([string]$Message) {
    Write-Error $Message
    exit 1
}

foreach ($tool in 'node', 'npm', 'go') {
    if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) { Fail "$tool is required" }
}

if ($UiBase -eq '/') {
    $UiBase = ''
} elseif ($UiBase -ne '') {
    if (-not $UiBase.StartsWith('/')) { Fail 'JANUS_UI_BASE_URL must be empty or an absolute URL path such as /janus' }
    if ($UiBase -match '[^A-Za-z0-9._/-]') { Fail 'JANUS_UI_BASE_URL must be empty or an absolute URL path such as /janus' }
    $UiBase = $UiBase.TrimEnd('/')
    if ($UiBase.Contains('//')) { Fail 'JANUS_UI_BASE_URL must not contain empty path segments' }
}

if ($Goos -eq 'windows' -and $Output -notlike '*.exe') {
    $Output = "$Output.exe"
}

Write-Host '==> building Admin Console'
if ($env:JANUS_INSTALL_DEPS -eq '1' -or -not (Test-Path (Join-Path $Frontend 'node_modules'))) {
    Push-Location $Frontend
    try { npm ci } finally { Pop-Location }
    if ($LASTEXITCODE -ne 0) { Fail 'npm ci failed' }
}
Push-Location $Frontend
try {
    $env:JANUS_UI_BASE_URL = $UiBase
    npm run build
} finally {
    Pop-Location
}
if ($LASTEXITCODE -ne 0) { Fail 'frontend build failed' }

if (-not (Test-Path (Join-Path $Dist 'index.html'))) { Fail 'frontend build did not produce dist/index.html' }
if (-not (Test-Path (Join-Path $Dist 'assets')))     { Fail 'frontend build did not produce dist/assets' }

Write-Host '==> synchronizing embedded Admin Console'
New-Item -ItemType Directory -Force (Join-Path $Embed 'assets') | Out-Null
Remove-Item (Join-Path $Embed 'assets\*') -Force -ErrorAction SilentlyContinue
Copy-Item (Join-Path $Dist 'index.html') (Join-Path $Embed 'index.html') -Force
Copy-Item (Join-Path $Dist 'assets\*') (Join-Path $Embed 'assets') -Force
& (Join-Path $Root 'scripts\verify-embedded-ui.ps1')

$OutputDir = Split-Path -Parent $Output
if ($OutputDir) { New-Item -ItemType Directory -Force $OutputDir | Out-Null }

Write-Host "==> building $Goos/$Goarch -> $Output"
Push-Location $Root
try {
    $env:CGO_ENABLED = '0'
    $env:GOOS        = $Goos
    $env:GOARCH      = $Goarch
    go build -trimpath -buildvcs=false -ldflags "-s -w -buildid= -X janus/internal/admin.uiBaseURL=$UiBase" -o $Output ./cmd/janus
} finally {
    Pop-Location
}
if ($LASTEXITCODE -ne 0) { Fail 'go build failed' }

if ($env:JANUS_UPX -eq '1') {
    if (-not (Get-Command upx -ErrorAction SilentlyContinue)) { Fail 'JANUS_UPX=1 requires upx in PATH' }
    Write-Host '==> applying optional UPX compression'
    upx --best --lzma $Output
    if ($LASTEXITCODE -ne 0) { Fail 'upx compression failed' }
}

if (-not (Test-Path $Output)) { Fail "Go build did not produce $Output" }

$hash = (Get-FileHash $Output -Algorithm SHA256).Hash.ToLowerInvariant()
Write-Host "sha256  $hash  $Output"
Write-Host ("built {0} ({1} bytes)" -f $Output, (Get-Item $Output).Length)
