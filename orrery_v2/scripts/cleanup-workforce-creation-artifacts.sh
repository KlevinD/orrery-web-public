#!/usr/bin/env bash
set -euo pipefail

mode='dry-run'
backup_dir=''

usage() {
  cat <<'EOF'
Usage: cleanup-workforce-creation-artifacts.sh [--apply --backup-dir ABSOLUTE_PATH]

Environment:
  ORRERY_CLEANUP_CORE_DATABASE_URL  PostgreSQL URL for the core database
  PGPASSWORD                        PostgreSQL password
  ORRERY_CLEANUP_PSQL_BIN           Optional psql executable override
EOF
}

fail() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

while (($# > 0)); do
  case "$1" in
    --apply)
      mode='apply'
      shift
      ;;
    --backup-dir)
      (($# >= 2)) || fail '--backup-dir requires a path'
      backup_dir=$2
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      fail "unknown argument: $1"
      ;;
  esac
done

database_url=${ORRERY_CLEANUP_CORE_DATABASE_URL-}
psql_bin=${ORRERY_CLEANUP_PSQL_BIN:-psql}
[[ -n $database_url ]] || fail 'ORRERY_CLEANUP_CORE_DATABASE_URL is required'
command -v "$psql_bin" >/dev/null 2>&1 || fail "psql executable not found: $psql_bin"

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
repository_root=$(git -C "$script_dir" rev-parse --show-toplevel 2>/dev/null || true)

if [[ $mode == 'apply' ]]; then
  [[ -n $backup_dir ]] || fail '--backup-dir is required with --apply'
  [[ $backup_dir == /* ]] || fail 'backup directory must be absolute'
  backup_dir=$(realpath -m -- "$backup_dir")
  if [[ -n $repository_root ]]; then
    repository_root=$(realpath -m -- "$repository_root")
    case "$backup_dir" in
      "$repository_root"|"$repository_root"/*)
        fail 'backup directory must be outside the repository'
        ;;
    esac
  fi
fi

targets_sql=$(cat <<'SQL'
  ('human-orrery-browser-e2e-1786379570670'),
  ('human-orrery-browser-e2e-1786379643169'),
  ('human-orrery-pa-e2e-1786378314'),
  ('human-orrery-pa-e2e-1786420622'),
  ('human-orrery-pa-e2e-1786424602'),
  ('human-orrery-pa-e2e-1786435622'),
  ('human-ding-ding'),
  ('human-migration-auditor'),
  ('human-scheduler-audit'),
  ('human-scheduler-diagnostics'),
  ('human-skill-migration-auditor'),
  ('human-scheduler-agent-task'),
  ('human-feishu-ou_98847333991e23e3bedd670b5e58903b')
SQL
)

exact_match_sql=$(cat <<'SQL'
(
  (p.table_name IN ('agent_profiles', 'agent_availability')
    AND p.record_key IN (SELECT profile_id FROM cleanup_targets))
  OR (p.table_name = 'agent_profile_deletions'
    AND p.record_key IN (SELECT profile_id FROM cleanup_targets))
  OR (p.table_name = 'discussion_session_commands'
    AND p.payload_json::jsonb ->> 'viewer_id' IN (SELECT profile_id FROM cleanup_targets))
  OR (p.table_name = 'knowledge_bases'
    AND p.payload_json::jsonb ->> 'owner_id' IN (SELECT profile_id FROM cleanup_targets))
  OR (p.table_name = 'memory_profiles'
    AND p.record_key IN (SELECT 'memory-' || profile_id FROM cleanup_targets))
)
SQL
)

possible_reference_sql=$(cat <<'SQL'
EXISTS (
  SELECT 1
  FROM cleanup_targets t
  WHERE p.record_key = t.profile_id
     OR p.record_key = 'memory-' || t.profile_id
     OR p.payload_json LIKE '%' || t.profile_id || '%'
)
SQL
)

run_sql() {
  "$psql_bin" "$database_url" -X -v ON_ERROR_STOP=1 -A -t -F '|' -c "$1"
}

inventory_sql="
-- cleanup_exact_inventory
WITH cleanup_targets(profile_id) AS (VALUES
$targets_sql
), inventory(table_name, row_count) AS (
  SELECT 'agent_profile_deletions', count(*) FROM platform_records p
    WHERE p.table_name = 'agent_profile_deletions' AND p.record_key IN (SELECT profile_id FROM cleanup_targets)
  UNION ALL
  SELECT 'agent_profiles', count(*) FROM platform_records p
    WHERE p.table_name = 'agent_profiles' AND p.record_key IN (SELECT profile_id FROM cleanup_targets)
  UNION ALL
  SELECT 'agent_availability', count(*) FROM platform_records p
    WHERE p.table_name = 'agent_availability' AND p.record_key IN (SELECT profile_id FROM cleanup_targets)
  UNION ALL
  SELECT 'discussion_session_commands', count(*) FROM platform_records p
    WHERE p.table_name = 'discussion_session_commands'
      AND p.payload_json::jsonb ->> 'viewer_id' IN (SELECT profile_id FROM cleanup_targets)
  UNION ALL
  SELECT 'knowledge_bases', count(*) FROM platform_records p
    WHERE p.table_name = 'knowledge_bases'
      AND p.payload_json::jsonb ->> 'owner_id' IN (SELECT profile_id FROM cleanup_targets)
  UNION ALL
  SELECT 'memory_profiles', count(*) FROM platform_records p
    WHERE p.table_name = 'memory_profiles'
      AND p.record_key IN (SELECT 'memory-' || profile_id FROM cleanup_targets)
  UNION ALL
  SELECT 'rebac_relation_tuples', count(*) FROM rebac_relation_tuples r
    WHERE r.subject_id IN (SELECT profile_id FROM cleanup_targets)
       OR r.object_id IN (SELECT profile_id FROM cleanup_targets)
)
SELECT table_name, row_count FROM inventory ORDER BY table_name;
"

unexpected_sql="
-- cleanup_unexpected_references
WITH cleanup_targets(profile_id) AS (VALUES
$targets_sql
)
SELECT p.table_name, p.record_key
FROM platform_records p
WHERE $possible_reference_sql
  AND NOT $exact_match_sql
UNION ALL
SELECT 'identity_subjects', i.id
FROM identity_subjects i
WHERE i.id IN (SELECT profile_id FROM cleanup_targets)
   OR i.user_id IN (SELECT profile_id FROM cleanup_targets)
   OR i.agent_profile_id IN (SELECT profile_id FROM cleanup_targets)
ORDER BY 1, 2;
"

printf 'mode=%s\n' "$mode"
printf 'inventory_before:\n'
run_sql "$inventory_sql"

unexpected=$(run_sql "$unexpected_sql")
if [[ -n $unexpected ]]; then
  printf 'unexpected_references:\n%s\n' "$unexpected" >&2
  fail 'unexpected dependent tables or records found; no data was deleted'
fi

if [[ $mode == 'dry-run' ]]; then
  printf 'result=no changes applied\n'
  exit 0
fi

umask 077
install -d -m 700 -- "$backup_dir"
backup_file=$(mktemp "$backup_dir/workforce-creation-artifacts-$(date -u +%Y%m%dT%H%M%SZ)-XXXXXX.jsonl")
chmod 600 -- "$backup_file"

backup_sql="
-- cleanup_backup_rows
WITH cleanup_targets(profile_id) AS (VALUES
$targets_sql
), matched AS MATERIALIZED (
  SELECT p.table_name, p.record_key, p.payload_json
  FROM platform_records p
  WHERE $exact_match_sql
  UNION ALL
  SELECT 'rebac_relation_tuples', r.id, row_to_json(r)::text
  FROM rebac_relation_tuples r
  WHERE r.subject_id IN (SELECT profile_id FROM cleanup_targets)
     OR r.object_id IN (SELECT profile_id FROM cleanup_targets)
), backup_lines AS (
  SELECT
    0 AS line_order,
    ''::text AS table_name,
    ''::text AS record_key,
    json_build_object(
      'backup_meta', true,
      'row_count', count(*),
      'digest', md5(coalesce(string_agg(table_name || chr(31) || record_key || chr(31) || payload_json, chr(30) ORDER BY table_name, record_key), ''))
    )::text AS line
  FROM matched
  UNION ALL
  SELECT
    1 AS line_order,
    p.table_name,
    p.record_key,
    json_build_object(
      'table_name', p.table_name,
      'record_key', p.record_key,
      'payload_json', p.payload_json
    )::text AS line
  FROM matched p
)
SELECT line
FROM backup_lines
ORDER BY line_order, table_name, record_key;
"
run_sql "$backup_sql" >"$backup_file"

backup_meta=$(head -n 1 -- "$backup_file")
backup_identity=$(printf '%s\n' "$backup_meta" | sed -nE 's/.*"row_count"[[:space:]]*:[[:space:]]*([0-9]+).*"digest"[[:space:]]*:[[:space:]]*"([0-9a-f]{32})".*/\1 \2/p')
[[ -n $backup_identity ]] || fail 'backup metadata is invalid; no data was deleted'
read -r backup_row_count backup_digest <<<"$backup_identity"

backup_sha256=$(sha256sum "$backup_file" | cut -d' ' -f1)
printf 'backup_file=%s\n' "$backup_file"
printf 'backup_sha256=%s\n' "$backup_sha256"

apply_sql="
-- cleanup_apply
BEGIN;
LOCK TABLE platform_records, rebac_relation_tuples, identity_subjects IN SHARE ROW EXCLUSIVE MODE;
CREATE TEMP TABLE cleanup_targets(profile_id text PRIMARY KEY) ON COMMIT DROP;
INSERT INTO cleanup_targets(profile_id) VALUES
$targets_sql;

DO \$cleanup\$
DECLARE
  current_row_count bigint;
  current_digest text;
BEGIN
  IF EXISTS (
    SELECT 1
    FROM platform_records p
    WHERE $possible_reference_sql
      AND NOT $exact_match_sql
  ) OR EXISTS (
    SELECT 1
    FROM identity_subjects i
    WHERE i.id IN (SELECT profile_id FROM cleanup_targets)
       OR i.user_id IN (SELECT profile_id FROM cleanup_targets)
       OR i.agent_profile_id IN (SELECT profile_id FROM cleanup_targets)
  ) THEN
    RAISE EXCEPTION 'unexpected workforce cleanup references appeared after backup';
  END IF;

  SELECT
    count(*),
    md5(coalesce(string_agg(m.table_name || chr(31) || m.record_key || chr(31) || m.payload_json, chr(30) ORDER BY m.table_name, m.record_key), ''))
  INTO current_row_count, current_digest
  FROM (
    SELECT p.table_name, p.record_key, p.payload_json
    FROM platform_records p
    WHERE $exact_match_sql
    UNION ALL
    SELECT 'rebac_relation_tuples', r.id, row_to_json(r)::text
    FROM rebac_relation_tuples r
    WHERE r.subject_id IN (SELECT profile_id FROM cleanup_targets)
       OR r.object_id IN (SELECT profile_id FROM cleanup_targets)
  ) m;

  IF current_row_count <> $backup_row_count OR current_digest <> '$backup_digest' THEN
    RAISE EXCEPTION 'backup digest changed before delete';
  END IF;
END
\$cleanup\$;

DELETE FROM platform_records p
WHERE p.table_name = 'discussion_session_commands'
  AND p.payload_json::jsonb ->> 'viewer_id' IN (SELECT profile_id FROM cleanup_targets);

DELETE FROM platform_records p
WHERE p.table_name = 'knowledge_bases'
  AND p.payload_json::jsonb ->> 'owner_id' IN (SELECT profile_id FROM cleanup_targets);

DELETE FROM platform_records p
WHERE p.table_name = 'memory_profiles'
  AND EXISTS (SELECT 1 FROM cleanup_targets t WHERE p.record_key = 'memory-' || t.profile_id);

DELETE FROM platform_records p
WHERE p.table_name = 'agent_availability'
  AND p.record_key IN (SELECT profile_id FROM cleanup_targets);

DELETE FROM rebac_relation_tuples r
WHERE r.subject_id IN (SELECT profile_id FROM cleanup_targets)
   OR r.object_id IN (SELECT profile_id FROM cleanup_targets);

DELETE FROM platform_records p
WHERE p.table_name = 'agent_profiles'
  AND p.record_key IN (SELECT profile_id FROM cleanup_targets);

INSERT INTO platform_records(table_name, record_key, payload_json)
SELECT
  'agent_profile_deletions',
  t.profile_id,
  json_build_object(
    'profile_id', t.profile_id,
    'reason', 'workforce.creation.synthetic_cleanup',
    'deleted_at', CURRENT_TIMESTAMP
  )::text
FROM cleanup_targets t
ON CONFLICT (table_name, record_key) DO NOTHING;
COMMIT;
"
run_sql "$apply_sql"

printf 'inventory_after:\n'
run_sql "$inventory_sql"
printf 'result=cleanup committed\n'
