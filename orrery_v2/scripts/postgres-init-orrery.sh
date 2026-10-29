#!/usr/bin/env bash
set -euo pipefail

default_databases="orrery_core,orrery_audit,orrery_events,orrery_jobs,orrery_runtime,orrery_observability"
databases="${ORRERY_POSTGRES_DATABASES:-$default_databases}"

trim() {
  local value="$1"
  value="${value#"${value%%[![:space:]]*}"}"
  value="${value%"${value##*[![:space:]]}"}"
  printf '%s' "$value"
}

if [[ ! "${POSTGRES_USER}" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
  echo "Invalid POSTGRES_USER: ${POSTGRES_USER}" >&2
  exit 1
fi

IFS=',' read -ra database_names <<< "$databases"
for raw_name in "${database_names[@]}"; do
  database_name="$(trim "$raw_name")"
  if [[ -z "$database_name" ]]; then
    continue
  fi
  if [[ ! "$database_name" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
    echo "Invalid database name: ${database_name}" >&2
    exit 1
  fi

  psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
    -v dbname="$database_name" -v owner="$POSTGRES_USER" <<'SQL'
SELECT format('CREATE DATABASE %I OWNER %I', :'dbname', :'owner')
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = :'dbname')\gexec
SQL
done
