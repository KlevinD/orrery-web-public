#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SOURCE_ROOT="$ROOT/../../_refs/agency-agents-zh"
OUTPUT_ZIP="$ROOT/data/imports/agency-agents-zh-orrery-talent-market.zip"

usage() {
  cat <<'EOF'
Usage: scripts/build-agency-agents-import.sh [--source-root DIR] [--output-zip FILE]

Builds an Orrery Talent Market import ZIP from jnMetaCode/agency-agents-zh Markdown files.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --source-root|-s)
      SOURCE_ROOT="${2:?--source-root requires a value}"
      shift 2
      ;;
    --output-zip|-o)
      OUTPUT_ZIP="${2:?--output-zip requires a value}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

python3 "$ROOT/scripts/agency_agents_import.py" \
  --mode build \
  --source-root "$SOURCE_ROOT" \
  --output-zip "$OUTPUT_ZIP"
