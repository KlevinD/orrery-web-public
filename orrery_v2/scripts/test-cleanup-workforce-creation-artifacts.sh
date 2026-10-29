#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
cleanup_script="$script_dir/cleanup-workforce-creation-artifacts.sh"
test_root=$(mktemp -d)
trap 'rm -rf -- "$test_root"' EXIT

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assert_contains() {
  local file=$1
  local expected=$2
  grep -Fq -- "$expected" "$file" || fail "$file does not contain: $expected"
}

assert_no_delete() {
  local file=$1
  if grep -Fq -- 'DELETE FROM platform_records' "$file"; then
    fail "$file contains a delete statement"
  fi
}

fake_psql="$test_root/psql"
cat >"$fake_psql" <<'FAKE_PSQL'
#!/usr/bin/env bash
set -euo pipefail

sql=''
while (($# > 0)); do
  if [[ $1 == '-c' ]]; then
    shift
    sql=${1-}
    break
  fi
  shift
done
printf '%s\n-- fake-call-end --\n' "$sql" >>"$FAKE_PSQL_LOG"

case "$sql" in
  *cleanup_unexpected_references*)
    printf '%s' "${FAKE_UNEXPECTED_REFERENCES-}"
    ;;
  *cleanup_exact_inventory*)
    printf '%s\n' \
      'agent_availability|4' \
      'agent_profiles|4' \
      'discussion_session_commands|2' \
      'knowledge_bases|3' \
      'memory_profiles|4' \
      'rebac_relation_tuples|3'
    ;;
  *cleanup_backup_rows*)
    printf '%s\n' \
      '{"backup_meta":true,"row_count":1,"digest":"0123456789abcdef0123456789abcdef"}' \
      '{"table_name":"agent_profiles","record_key":"human-ding-ding","payload_json":"{}"}'
    ;;
  *cleanup_apply*)
    printf '%s\n' 'DELETE 2' 'DELETE 3' 'DELETE 4' 'DELETE 4' 'DELETE 4' 'COMMIT'
    ;;
esac
FAKE_PSQL
chmod +x "$fake_psql"

export ORRERY_CLEANUP_CORE_DATABASE_URL='postgres://orrery@127.0.0.1:5434/orrery_core?sslmode=disable'
export ORRERY_CLEANUP_PSQL_BIN="$fake_psql"
export PGPASSWORD='test-only'

dry_log="$test_root/dry.log"
dry_output="$test_root/dry.out"
FAKE_PSQL_LOG="$dry_log" "$cleanup_script" >"$dry_output"
assert_contains "$dry_output" 'mode=dry-run'
assert_contains "$dry_output" 'agent_profiles|4'
assert_no_delete "$dry_log"

if FAKE_PSQL_LOG="$test_root/no-backup.log" "$cleanup_script" --apply >"$test_root/no-backup.out" 2>&1; then
  fail 'apply succeeded without a backup directory'
fi
assert_contains "$test_root/no-backup.out" '--backup-dir is required with --apply'

if FAKE_PSQL_LOG="$test_root/relative.log" "$cleanup_script" --apply --backup-dir relative >"$test_root/relative.out" 2>&1; then
  fail 'apply accepted a relative backup directory'
fi
assert_contains "$test_root/relative.out" 'backup directory must be absolute'

if FAKE_PSQL_LOG="$test_root/repository.log" "$cleanup_script" --apply --backup-dir "$script_dir" >"$test_root/repository.out" 2>&1; then
  fail 'apply accepted a backup directory inside the repository'
fi
assert_contains "$test_root/repository.out" 'backup directory must be outside the repository'

unexpected_log="$test_root/unexpected.log"
if FAKE_PSQL_LOG="$unexpected_log" FAKE_UNEXPECTED_REFERENCES='identity_subjects|subject-1' \
  "$cleanup_script" --apply --backup-dir "$test_root/unexpected-backup" >"$test_root/unexpected.out" 2>&1; then
  fail 'apply continued after an unexpected reference'
fi
assert_contains "$test_root/unexpected.out" 'identity_subjects|subject-1'
assert_no_delete "$unexpected_log"

apply_log="$test_root/apply.log"
apply_output="$test_root/apply.out"
backup_dir="$test_root/backup"
FAKE_PSQL_LOG="$apply_log" "$cleanup_script" --apply --backup-dir "$backup_dir" >"$apply_output"
backup_file=$(find "$backup_dir" -maxdepth 1 -type f -name '*.jsonl' -print -quit)
[[ -n $backup_file ]] || fail 'apply did not create a JSONL backup'
[[ $(stat -c '%a' "$backup_file") == '600' ]] || fail 'backup permissions are not 0600'
assert_contains "$backup_file" 'human-ding-ding'
assert_contains "$apply_output" 'mode=apply'
assert_contains "$apply_output" 'backup_sha256='
assert_contains "$apply_log" 'cleanup_backup_rows'
assert_contains "$apply_log" 'cleanup_apply'

backup_line=$(grep -n -m1 'cleanup_backup_rows' "$apply_log" | cut -d: -f1)
apply_line=$(grep -n -m1 'cleanup_apply' "$apply_log" | cut -d: -f1)
((backup_line < apply_line)) || fail 'delete ran before backup'

for target in \
  human-orrery-browser-e2e-1786379570670 \
  human-orrery-browser-e2e-1786379643169 \
  human-orrery-pa-e2e-1786378314 \
  human-orrery-pa-e2e-1786420622 \
  human-orrery-pa-e2e-1786424602 \
  human-orrery-pa-e2e-1786435622 \
  human-ding-ding \
  human-migration-auditor \
  human-scheduler-audit \
  human-scheduler-diagnostics \
  human-skill-migration-auditor \
  human-scheduler-agent-task \
  human-feishu-ou_98847333991e23e3bedd670b5e58903b; do
  assert_contains "$apply_log" "$target"
done
assert_contains "$apply_log" "payload_json::jsonb ->> 'viewer_id'"
assert_contains "$apply_log" "payload_json::jsonb ->> 'owner_id'"
assert_contains "$apply_log" "record_key = 'memory-' || t.profile_id"
assert_contains "$apply_log" "DELETE FROM rebac_relation_tuples"
assert_contains "$apply_log" "'agent_profile_deletions'"
assert_contains "$apply_log" "ON CONFLICT (table_name, record_key) DO NOTHING"
assert_contains "$apply_log" 'backup digest changed before delete'

printf 'PASS: cleanup workforce creation artifacts\n'
