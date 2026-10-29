#!/usr/bin/env bash
set -euo pipefail

: "${POSTGRES_USER:?POSTGRES_USER is required}"
: "${POSTGRES_PASSWORD:?POSTGRES_PASSWORD is required}"
: "${POSTGRES_DB:?POSTGRES_DB is required}"
: "${PAGEINDEX_POSTGRES_USER:?PAGEINDEX_POSTGRES_USER is required}"
: "${PAGEINDEX_POSTGRES_PASSWORD:?PAGEINDEX_POSTGRES_PASSWORD is required}"
: "${PAGEINDEX_POSTGRES_DB:?PAGEINDEX_POSTGRES_DB is required}"

for identifier in "$POSTGRES_USER" "$POSTGRES_DB" "$PAGEINDEX_POSTGRES_USER" "$PAGEINDEX_POSTGRES_DB"; do
  if [[ ! "$identifier" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
    echo "Invalid PostgreSQL identifier: $identifier" >&2
    exit 1
  fi
done

if [[ "$PAGEINDEX_POSTGRES_USER" == "$POSTGRES_USER" ]]; then
  if [[ "$PAGEINDEX_POSTGRES_PASSWORD" != "$POSTGRES_PASSWORD" ]]; then
    echo "Shared PostgreSQL role must use the same password" >&2
    exit 1
  fi
else
  psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
    -v role="$PAGEINDEX_POSTGRES_USER" -v password="$PAGEINDEX_POSTGRES_PASSWORD" <<'SQL'
SELECT format('CREATE ROLE %I LOGIN', :'role')
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = :'role')\gexec
SELECT format('ALTER ROLE %I LOGIN PASSWORD %L', :'role', :'password')\gexec
SQL
fi

if [[ "$PAGEINDEX_POSTGRES_DB" != "$POSTGRES_DB" ]]; then
  psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
    -v dbname="$PAGEINDEX_POSTGRES_DB" -v owner="$PAGEINDEX_POSTGRES_USER" <<'SQL'
SELECT format('CREATE DATABASE %I OWNER %I', :'dbname', :'owner')
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = :'dbname')\gexec
SQL
fi
