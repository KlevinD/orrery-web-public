param(
  [string]$Target = "x86_64-pc-windows-msvc",
  [switch]$SkipTauri
)

$ErrorActionPreference = 'Stop'

$Root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$WebRoot = Join-Path $Root 'web'

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

& (Join-Path $Root 'scripts\build-sidecar.ps1') -Target $Target

if ($SkipTauri) {
  Write-Host "Building web assets"
  & (Resolve-Tool 'npm') --prefix $WebRoot run build
} else {
  Write-Host "Building Tauri desktop bundle"
  Push-Location $Root
  try {
    & (Resolve-Tool 'npm') --prefix $WebRoot exec tauri -- build --config src-tauri/tauri.conf.json
  } finally {
    Pop-Location
  }
}
