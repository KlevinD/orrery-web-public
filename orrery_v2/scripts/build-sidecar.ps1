param(
  [string]$Target = "x86_64-pc-windows-msvc"
)

$ErrorActionPreference = 'Stop'

$Root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$TauriRoot = Join-Path $Root 'src-tauri'
$BinariesRoot = Join-Path $TauriRoot 'binaries'

function Resolve-Tool([string]$Name) {
  if ($Name -eq 'npm') {
    $Cmd = Get-Command 'npm.cmd' -ErrorAction SilentlyContinue
    if ($Cmd) {
      return $Cmd.Source
    }
  }
  $Command = Get-Command $Name -ErrorAction Stop
  if ($Command.Path) {
    return $Command.Path
  }
  return $Command.Source
}

& (Join-Path $Root 'scripts\check-context-graph-production-gate.ps1')

New-Item -ItemType Directory -Force -Path $BinariesRoot | Out-Null

$ExeSuffix = if ($Target -like '*windows*') { '.exe' } else { '' }
$ServerOut = Join-Path $BinariesRoot "orrery-server-$Target$ExeSuffix"

Write-Host "Building Go sidecar: $ServerOut"
Push-Location $Root
try {
  & (Resolve-Tool 'go') build -o $ServerOut ./cmd/server
} finally {
  Pop-Location
}
