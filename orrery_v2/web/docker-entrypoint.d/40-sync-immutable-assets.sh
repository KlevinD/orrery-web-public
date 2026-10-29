#!/bin/sh
set -eu

source_dir=${ORRERY_WEB_IMMUTABLE_SOURCE_DIR:-/opt/orrery-web-immutable}
target_dir=${ORRERY_WEB_IMMUTABLE_TARGET_DIR:-/usr/share/nginx/html/_app/immutable}
retention_days=${ORRERY_WEB_ASSET_RETENTION_DAYS:-7}

case "$retention_days" in
  ''|*[!0-9]*)
    echo "ORRERY_WEB_ASSET_RETENTION_DAYS must be a non-negative integer" >&2
    exit 1
    ;;
esac

if [ ! -d "$source_dir" ]; then
  echo "Immutable web asset source is missing: $source_dir" >&2
  exit 1
fi

mkdir -p "$target_dir"

# Keep recent releases so open browser sessions can still import their hashed chunks.
find "$target_dir" -type f -mtime "+$retention_days" -delete
find "$target_dir" -depth -mindepth 1 -type d -empty -delete
cp -a "$source_dir/." "$target_dir/"
