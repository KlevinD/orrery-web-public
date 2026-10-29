#!/usr/bin/env bash
set -euo pipefail

TARGET=""

usage() {
  cat <<'EOF'
Usage: scripts/build-sidecar.sh [--target TARGET]

Builds the Go API sidecar into src-tauri/binaries.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --target|-t)
      TARGET="${2:?--target requires a value}"
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

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TAURI_ROOT="$ROOT/src-tauri"
BINARIES_ROOT="$TAURI_ROOT/binaries"

bash "$ROOT/scripts/check-context-graph-production-gate.sh"

if [[ -z "$TARGET" ]]; then
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  machine="$(uname -m)"
  case "$os:$machine" in
    linux:x86_64|linux:amd64) TARGET="x86_64-unknown-linux-gnu" ;;
    linux:aarch64|linux:arm64) TARGET="aarch64-unknown-linux-gnu" ;;
    darwin:x86_64|darwin:amd64) TARGET="x86_64-apple-darwin" ;;
    darwin:aarch64|darwin:arm64) TARGET="aarch64-apple-darwin" ;;
    *) TARGET="${machine}-unknown-${os}" ;;
  esac
fi

command -v go >/dev/null 2>&1 || { echo "Required tool not found on PATH: go" >&2; exit 127; }

mkdir -p "$BINARIES_ROOT"

EXE_SUFFIX=""
if [[ "$TARGET" == *windows* ]]; then
  EXE_SUFFIX=".exe"
fi

SERVER_OUT="$BINARIES_ROOT/orrery-server-$TARGET$EXE_SUFFIX"
echo "Building Go sidecar: $SERVER_OUT"
(
  cd "$ROOT"
  go build -o "$SERVER_OUT" ./cmd/server
)

# Tauri externalBin resolves to the packaged binary base name during dev.
LOCAL_OUT="$BINARIES_ROOT/orrery-server$EXE_SUFFIX"
if [[ "$LOCAL_OUT" != "$SERVER_OUT" ]]; then
  cp "$SERVER_OUT" "$LOCAL_OUT"
fi
