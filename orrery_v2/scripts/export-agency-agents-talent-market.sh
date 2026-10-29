#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SOURCE_DIR="$ROOT/../../_refs/agency-agents-zh"
OUTPUT_DIR="$ROOT/data/imports"
OUTPUT_NAME="agency-agents-zh-orrery-talent-market.zip"

usage() {
  cat <<'EOF'
Usage: scripts/export-agency-agents-talent-market.sh [--source-dir DIR] [--output-dir DIR] [--output-name NAME]

Exports jnMetaCode/agency-agents-zh Markdown agents into an Orrery Talent Market ZIP.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --source-dir|-s)
      SOURCE_DIR="${2:?--source-dir requires a value}"
      shift 2
      ;;
    --output-dir|-o)
      OUTPUT_DIR="${2:?--output-dir requires a value}"
      shift 2
      ;;
    --output-name|-n)
      OUTPUT_NAME="${2:?--output-name requires a value}"
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

mkdir -p "$OUTPUT_DIR"
python3 "$ROOT/scripts/agency_agents_import.py" \
  --mode export \
  --source-root "$SOURCE_DIR" \
  --output-zip "$OUTPUT_DIR/$OUTPUT_NAME"
