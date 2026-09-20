$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent (Split-Path -Parent $PSCommandPath)
& node (Join-Path $Root 'scripts/build-app.mjs')
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
