#!/usr/bin/env bash
set -euo pipefail

TARGET=""
SKIP_TAURI=0

usage() {
  cat <<'EOF'
Usage: scripts/build-desktop.sh [--target TARGET] [--skip-tauri]

Builds the Go sidecar, then either builds web assets or the Tauri desktop bundle.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --target|-t)
      TARGET="${2:?--target requires a value}"
      shift 2
      ;;
    --skip-tauri)
      SKIP_TAURI=1
      shift
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

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WEB_ROOT="$ROOT/web"

build_args=()
if [[ -n "$TARGET" ]]; then
  build_args+=(--target "$TARGET")
fi
bash "$ROOT/scripts/build-sidecar.sh" "${build_args[@]}"

if [[ "$SKIP_TAURI" -eq 1 ]]; then
  echo "Building web assets"
  npm --prefix "$WEB_ROOT" run build
else
  echo "Building Tauri desktop bundle"
  (
    cd "$ROOT"
    npm --prefix "$WEB_ROOT" exec tauri -- build --config src-tauri/tauri.conf.json
  )
fi
