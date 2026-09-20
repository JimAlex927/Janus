# Compare the reviewed Vite output with the tracked Go embed. This prevents a
# source-only frontend change from silently shipping an older Admin Console.
$ErrorActionPreference = 'Stop'

$Root  = Split-Path -Parent (Split-Path -Parent $PSCommandPath)
$Dist  = Join-Path $Root 'frontend\dist'
$Embed = Join-Path $Root 'internal\admin\ui'

function Fail([string]$Message) {
    Write-Error $Message
    exit 1
}

if (-not (Test-Path (Join-Path $Dist 'index.html')))  { Fail 'frontend/dist/index.html is missing' }
if (-not (Test-Path (Join-Path $Embed 'index.html'))) { Fail 'internal/admin/ui/index.html is missing' }

if ((Get-FileHash (Join-Path $Dist 'index.html')).Hash -ne (Get-FileHash (Join-Path $Embed 'index.html')).Hash) {
    Fail 'frontend/dist/index.html differs from internal/admin/ui/index.html'
}

$distAssets  = Get-ChildItem (Join-Path $Dist 'assets')  -File | Sort-Object Name
$embedAssets = Get-ChildItem (Join-Path $Embed 'assets') -File | Sort-Object Name
$distNames  = @($distAssets  | ForEach-Object Name)
$embedNames = @($embedAssets | ForEach-Object Name)
if (($distNames -join "`n") -cne ($embedNames -join "`n")) {
    Fail 'embedded assets do not match frontend/dist/assets file list'
}

foreach ($asset in $distAssets) {
    $embedded = Join-Path $Embed ('assets\' + $asset.Name)
    if ((Get-FileHash $asset.FullName).Hash -ne (Get-FileHash $embedded).Hash) {
        Fail "embedded asset $($asset.Name) differs from frontend/dist"
    }
}

Write-Host 'embedded Admin Console matches frontend/dist'
