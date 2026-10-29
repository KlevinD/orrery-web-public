#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

command -v go >/dev/null 2>&1 || { echo "Required tool not found on PATH: go" >&2; exit 127; }

echo "Checking Orrery Web/API context graph production gate"
(
  cd "$ROOT"
  go test ./internal/app/ontology -run 'TestProductionCoverage(Gate|AuditReport|Required0618Modules|ExemptionReport)' -v -count=1
)
