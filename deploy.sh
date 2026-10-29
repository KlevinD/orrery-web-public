#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
  cat <<'EOF'
Usage: ./deploy.sh [options]

Pull the latest Orrery code, rebuild Docker images, and publish the Compose
stack. PageIndex is optional; enable it with --with-pageindex.

Options:
  --only-if-updated       Skip Docker rebuild/publish when pulled code is unchanged.
  --pull-images           Pull base/service images before rebuilding.
  --with-pageindex        Include the optional external PageIndex service.
  --no-cache              Rebuild images without Docker build cache.
  --skip-git-pull         Do not pull the Orrery repository.
  --skip-pageindex-pull   Deprecated compatibility flag; PageIndex is image-based.
  -h, --help              Show this help.

The script reads ORRERY_RELEASE_FILE from .env.
The release file owns image references; config/build.env owns build inputs.
EOF
}

log() {
  printf '\n[%s] %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*"
}

die() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

read_env_value() {
  local key="$1"
  local line value
  line="$(grep -E "^[[:space:]]*${key}=" .env 2>/dev/null | tail -n 1 || true)"
  if [[ -z "$line" ]]; then
    return 0
  fi
  value="${line#*=}"
  value="${value%$'\r'}"
  value="${value%%#*}"
  value="${value#"${value%%[![:space:]]*}"}"
  value="${value%"${value##*[![:space:]]}"}"
  value="${value%\"}"
  value="${value#\"}"
  value="${value%\'}"
  value="${value#\'}"
  printf '%s' "$value"
}

require_clean_git_tree() {
  local repo_dir="$1"
  local repo_name="$2"

  if ! git -C "$repo_dir" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    log "Skipping git pull for ${repo_name}: not a git repository at ${repo_dir}"
    return
  fi

  if ! git -C "$repo_dir" diff --quiet --ignore-submodules -- || ! git -C "$repo_dir" diff --cached --quiet --ignore-submodules --; then
    die "${repo_name} has tracked uncommitted changes at ${repo_dir}. Commit, stash, or use the skip flag before deploying."
  fi
}

pull_git_repo() {
  local repo_dir="$1"
  local repo_name="$2"
  local before after

  require_clean_git_tree "$repo_dir" "$repo_name"
  if ! git -C "$repo_dir" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    return
  fi

  before="$(git -C "$repo_dir" rev-parse HEAD)"
  log "Pulling latest ${repo_name} code"
  git -C "$repo_dir" fetch --prune

  if git -C "$repo_dir" rev-parse --abbrev-ref --symbolic-full-name '@{u}' >/dev/null 2>&1; then
    git -C "$repo_dir" pull --ff-only
  else
    local branch
    branch="$(git -C "$repo_dir" branch --show-current)"
    [[ -n "$branch" ]] || die "${repo_name} is detached and has no upstream branch."
    git -C "$repo_dir" pull --ff-only origin "$branch"
  fi

  after="$(git -C "$repo_dir" rev-parse HEAD)"
  if [[ "$before" != "$after" ]]; then
    code_updated=1
    log "${repo_name} updated: ${before:0:12} -> ${after:0:12}"
  else
    log "${repo_name} already at latest commit: ${after:0:12}"
  fi
}

with_pageindex=0
skip_git_pull=0
skip_pageindex_pull=0
no_cache=0
pull_images=0
only_if_updated=0
code_updated=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --only-if-updated)
      only_if_updated=1
      ;;
    --with-pageindex)
      with_pageindex=1
      ;;
    --pull-images)
      pull_images=1
      ;;
    --no-cache)
      no_cache=1
      ;;
    --skip-git-pull)
      skip_git_pull=1
      ;;
    --skip-pageindex-pull)
      skip_pageindex_pull=1
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage
      die "Unknown option: $1"
      ;;
  esac
  shift
done

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
cd "$script_dir"

command -v git >/dev/null 2>&1 || die "git is required."

if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
  compose=(docker compose)
elif command -v docker-compose >/dev/null 2>&1; then
  compose=(docker-compose)
elif command -v sudo >/dev/null 2>&1 && sudo -n docker info >/dev/null 2>&1 && sudo -n docker compose version >/dev/null 2>&1; then
  compose=(sudo -n docker compose)
elif command -v sudo >/dev/null 2>&1 && command -v docker-compose >/dev/null 2>&1 && sudo -n docker-compose version >/dev/null 2>&1; then
  compose=(sudo -n docker-compose)
else
  die "Docker Compose is required, and this user must be able to access Docker directly or through passwordless sudo."
fi

[[ -f .env ]] || die "Missing .env. Create it with: cp .env.example .env"
[[ -f orrery_v2/config/build.env ]] || die "Missing canonical build config: orrery_v2/config/build.env"

release_file="$(read_env_value ORRERY_RELEASE_FILE)"
release_file="${release_file:-orrery_v2/deploy/releases/local.env}"
case "$release_file" in
  /*) ;;
  *) release_file="${script_dir}/${release_file}" ;;
esac
[[ -f "$release_file" ]] || die "Release manifest not found: ${release_file}"

set -a
# shellcheck disable=SC1091
source .env
# shellcheck disable=SC1091
source orrery_v2/config/build.env
# shellcheck disable=SC1090
source "$release_file"
set +a

: "${ORRERY_API_IMAGE:?ORRERY_API_IMAGE is required in the release manifest}"
: "${ORRERY_WEB_IMAGE:?ORRERY_WEB_IMAGE is required in the release manifest}"
compose+=(-f docker-compose.yml)
if [[ "$with_pageindex" -eq 1 ]]; then
  : "${PAGEINDEX_IMAGE:?PAGEINDEX_IMAGE is required for --with-pageindex}"
  compose+=(-f docker-compose.pageindex.yml)
fi


if [[ "$skip_git_pull" -eq 0 ]]; then
  pull_git_repo "$script_dir" "Orrery"
else
  log "Skipping Orrery git pull"
fi

if [[ "$skip_pageindex_pull" -eq 1 ]]; then
  log "Ignoring deprecated --skip-pageindex-pull; use --with-pageindex to opt in"
fi

if [[ "$only_if_updated" -eq 1 && "$code_updated" -eq 0 ]]; then
  log "No code updates detected; skipping Docker rebuild and publish"
  exit 0
fi

build_args=(build)
if [[ "$pull_images" -eq 1 ]]; then
  build_args+=(--pull)
fi
if [[ "$no_cache" -eq 1 ]]; then
  build_args+=(--no-cache)
fi

if [[ "$pull_images" -eq 1 ]]; then
  log "Pulling external service images"
  "${compose[@]}" pull --ignore-pull-failures
else
  log "Skipping Docker image pulls; pass --pull-images to refresh base and service images"
fi

log "Checking Orrery Web/API context graph production gate"
bash "${script_dir}/orrery_v2/scripts/check-context-graph-production-gate.sh"

log "Rebuilding all buildable images"
"${compose[@]}" "${build_args[@]}"

log "Publishing Compose stack"
"${compose[@]}" up -d

log "Current service status"
"${compose[@]}" ps
