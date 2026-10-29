param()

$ErrorActionPreference = 'Stop'

$Root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)

function Resolve-Tool([string]$Name) {
  $Command = Get-Command $Name -ErrorAction Stop
  if ($Command.Path) {
    return $Command.Path
  }
  return $Command.Source
}

Write-Host 'Checking Orrery Web/API context graph production gate'
$GateExitCode = 1
Push-Location $Root
try {
  & (Resolve-Tool 'go') test ./internal/app/ontology -run 'TestProductionCoverage(Gate|AuditReport|Required0618Modules|ExemptionReport)' -v -count=1
  $GateExitCode = $LASTEXITCODE
} finally {
  Pop-Location
}
exit $GateExitCode
