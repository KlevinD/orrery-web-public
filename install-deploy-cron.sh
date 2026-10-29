#!/usr/bin/env bash
set -Eeuo pipefail

printf '%s\n' \
  'Automatic deployment has been retired; no cron job was installed.' \
  'Use reviewed manual deployment: orrery_v2/docs/operations/manual-deployment.md' >&2
exit 1
