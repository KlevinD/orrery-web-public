#!/usr/bin/env bash
set -euo pipefail

ACTION="start"
API_ADDR="127.0.0.1:8080"
WEB_PORT="5178"
FOLLOW=0

usage() {
  cat <<'EOF'
Usage: ./dev.sh [start|stop|restart|status|logs] [--api-addr ADDR] [--web-port PORT] [-f|--follow]

Examples:
  ./dev.sh start
  ./dev.sh status
  ./dev.sh logs --follow
  ./dev.sh stop
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    start|stop|restart|status|logs)
      ACTION="$1"
      shift
      ;;
    --api-addr)
      API_ADDR="${2:?--api-addr requires a value}"
      shift 2
      ;;
    --web-port)
      WEB_PORT="${2:?--web-port requires a value}"
      shift 2
      ;;
    -f|--follow)
      FOLLOW=1
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

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WEB_ROOT="$ROOT/web"
RUN_ROOT="$ROOT/.dev"
API_PID_FILE="$RUN_ROOT/api.pid"
WEB_PID_FILE="$RUN_ROOT/web.pid"
API_OUT="$RUN_ROOT/api.out"
API_LOG="$RUN_ROOT/api.log"
API_ERR_LOG="$RUN_ROOT/api.err.log"
WEB_LOG="$RUN_ROOT/web.log"
WEB_ERR_LOG="$RUN_ROOT/web.err.log"
DEV_START_LOG="$RUN_ROOT/dev-start.log"
DEFAULT_DEV_CONFIG="$ROOT/config/orrery.development.yaml"
PI_CODING_AGENT_ROOT="$ROOT/runtime/pi-coding-agent"
MINIMAX_TOKEN_PLAN_SECRET="$ROOT/secrets/minimax_api_key"
REQUIRED_PI_NODE_MAJOR=22
REQUIRED_PI_NODE_MINOR=19

ensure_run_root() {
  mkdir -p "$RUN_ROOT"
}

log_dev_message() {
  local message="${1:-}"
  printf '%s\n' "$message"
  if [[ -d "$RUN_ROOT" ]]; then
    printf '[%s] %s\n' "$(date -Iseconds)" "$message" >> "$DEV_START_LOG"
  fi
}

require_tool() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Required tool not found on PATH: $1" >&2
    exit 127
  fi
}

assert_pi_node_version() {
  local version major minor
  version="$(node -p 'process.versions.node')"
  IFS=. read -r major minor _ <<< "$version"
  if [[ -z "$major" || -z "$minor" ]]; then
    echo "Cannot parse Node version '$version'. PI runtime requires Node >= ${REQUIRED_PI_NODE_MAJOR}.${REQUIRED_PI_NODE_MINOR}.0." >&2
    exit 1
  fi
  if (( major < REQUIRED_PI_NODE_MAJOR || (major == REQUIRED_PI_NODE_MAJOR && minor < REQUIRED_PI_NODE_MINOR) )); then
    echo "PI runtime requires Node >= ${REQUIRED_PI_NODE_MAJOR}.${REQUIRED_PI_NODE_MINOR}.0, current v${version}. Upgrade Node before starting Orrery with PI." >&2
    exit 1
  fi
}

ensure_pi_coding_agent_runtime() {
  local package="$PI_CODING_AGENT_ROOT/package.json"
  local lock="$PI_CODING_AGENT_ROOT/package-lock.json"
  local pi_package="$PI_CODING_AGENT_ROOT/node_modules/@earendil-works/pi-coding-agent/package.json"
  local utility_verifier="$ROOT/runtime/orrery-js/install.mjs"
  log_dev_message "Checking PI coding agent runtime..."
  if [[ ! -f "$package" || ! -f "$lock" ]]; then
    echo "PI coding agent runtime is missing at fixed path: $PI_CODING_AGENT_ROOT" >&2
    exit 1
  fi
  assert_pi_node_version
  if [[ ! -f "$pi_package" ]] || ! node "$utility_verifier" pi --check >/dev/null 2>&1; then
    log_dev_message "Installing PI coding agent runtime dependencies at $PI_CODING_AGENT_ROOT..."
    npm --prefix "$PI_CODING_AGENT_ROOT" ci --ignore-scripts
  fi
  node "$utility_verifier" pi --check
  log_dev_message "PI coding agent runtime ready."
}

read_pid() {
  local file="$1"
  [[ -f "$file" ]] || return 1
  local pid
  pid="$(tr -d '[:space:]' < "$file")"
  [[ "$pid" =~ ^[0-9]+$ ]] || return 1
  printf '%s\n' "$pid"
}

is_alive() {
  local pid="$1"
  [[ -n "$pid" ]] && kill -0 "$pid" >/dev/null 2>&1
}

alive_pid_from_file() {
  local file="$1"
  local pid
  if pid="$(read_pid "$file")" && is_alive "$pid"; then
    printf '%s\n' "$pid"
    return 0
  fi
  return 1
}

process_name() {
  local pid="$1"
  ps -p "$pid" -o comm= 2>/dev/null | awk '{$1=$1; print}' || true
}

port_owner() {
  local port="$1"
  local owner=""
  if command -v ss >/dev/null 2>&1; then
    owner="$(ss -ltnp "sport = :$port" 2>/dev/null | sed -n 's/.*pid=\([0-9][0-9]*\).*/\1/p' | head -n 1)"
  fi
  if [[ -z "$owner" ]] && command -v lsof >/dev/null 2>&1; then
    owner="$(lsof -nP -iTCP:"$port" -sTCP:LISTEN -t 2>/dev/null | head -n 1)"
  fi
  if [[ -z "$owner" ]] && command -v fuser >/dev/null 2>&1; then
    owner="$(fuser "$port/tcp" 2>/dev/null | tr ' ' '\n' | grep -E '^[0-9]+$' | head -n 1)"
  fi
  [[ -n "$owner" ]] || return 1
  printf '%s\n' "$owner"
}

port_listening() {
  local port="$1"
  if command -v ss >/dev/null 2>&1; then
    ss -H -ltn "sport = :$port" 2>/dev/null | grep -q .
    return
  fi
  if command -v lsof >/dev/null 2>&1; then
    lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1
    return
  fi
  if command -v fuser >/dev/null 2>&1; then
    fuser "$port/tcp" >/dev/null 2>&1
    return
  fi
  return 1
}

api_port() {
  printf '%s\n' "${API_ADDR##*:}"
}

stop_pid() {
  local name="$1"
  local pid="$2"
  if ! is_alive "$pid"; then
    return 0
  fi
  kill "$pid" >/dev/null 2>&1 || true
  for _ in {1..25}; do
    if ! is_alive "$pid"; then
      return 0
    fi
    sleep 0.2
  done
  kill -9 "$pid" >/dev/null 2>&1 || true
  log_dev_message "$name stopped forcefully (PID $pid)."
}

stop_port_owner() {
  local name="$1"
  local port="$2"
  local owner
  owner="$(port_owner "$port" 2>/dev/null || true)"
  [[ -n "$owner" ]] || return 1
  stop_pid "$name" "$owner"
  log_dev_message "$name stopped by port $port (PID $owner, $(process_name "$owner"))."
  return 0
}

new_api_env_file() {
  local env_file="$RUN_ROOT/api.env"
  {
    printf 'PATH=%q\n' "$PI_CODING_AGENT_ROOT/node_modules/.bin:$PATH"
    local minimax_cn_api_key="${MINIMAX_CN_API_KEY:-}"
    if [[ -z "$minimax_cn_api_key" && -f "$MINIMAX_TOKEN_PLAN_SECRET" ]]; then
      minimax_cn_api_key="$(tr -d '\r\n' < "$MINIMAX_TOKEN_PLAN_SECRET")"
    fi
    if [[ -n "$minimax_cn_api_key" ]]; then
      printf 'MINIMAX_CN_API_KEY=%q\n' "$minimax_cn_api_key"
    fi

    local has_config=0
    local key value
    for key in ORRERY_DATABASE_PASSWORD ORRERY_CONFIG; do
      value="${!key:-}"
      if [[ -n "$value" ]]; then
        printf '%s=%q\n' "$key" "$value"
        case "$key" in
          ORRERY_CONFIG) has_config=1 ;;
        esac
      fi
    done

    if [[ "$has_config" -eq 0 ]]; then
      printf 'ORRERY_CONFIG=%q\n' "$DEFAULT_DEV_CONFIG"
      has_config=1
    fi
    if [[ -z "${ORRERY_DATABASE_PASSWORD:-}" ]]; then
      printf 'ORRERY_DATABASE_PASSWORD=%q\n' "${ORRERY_DEV_POSTGRES_PASSWORD:-orrery}"
    fi
  } > "$env_file"
  chmod 600 "$env_file"
  printf '%s\n' "$env_file"
}

start_service() {
  local name="$1"
  local pid_file="$2"
  local work_dir="$3"
  local out_log="$4"
  local err_log="$5"
  shift 5

  local existing
  existing="$(alive_pid_from_file "$pid_file" 2>/dev/null || true)"
  if [[ -n "$existing" ]]; then
    log_dev_message "$name already running (PID $existing)."
    return
  fi

  (
    cd "$work_dir"
    setsid nohup "$@" >"$out_log" 2>"$err_log" < /dev/null &
    printf '%s\n' "$!" > "$pid_file"
  )
  log_dev_message "$name started (PID $(cat "$pid_file"))."
}

start_api_service() {
  local env_file="$1"
  local existing
  existing="$(alive_pid_from_file "$API_PID_FILE" 2>/dev/null || true)"
  if [[ -n "$existing" ]]; then
    log_dev_message "API already running (PID $existing)."
    return
  fi

  (
    cd "$ROOT"
    set -a
    # shellcheck disable=SC1090
    source "$env_file"
    set +a
    {
      printf '\n== API start %s ==\n' "$(date -Iseconds)"
      setsid nohup go run ./cmd/server -addr "$API_ADDR" >>"$API_OUT" 2>&1 < /dev/null &
      printf '%s\n' "$!" > "$API_PID_FILE"
    } >>"$API_OUT"
  )
  log_dev_message "API started (PID $(cat "$API_PID_FILE"), log $API_OUT)."
}

start_all() {
  ensure_run_root
  log_dev_message "Starting Orrery dev services..."
  require_tool go
  require_tool node
  require_tool npm
  ensure_pi_coding_agent_runtime

  local api_port_value
  api_port_value="$(api_port)"
  local api_env_file
  api_env_file="$(new_api_env_file)"

  local managed_api managed_web api_owner web_owner api_listening=0 web_listening=0 skip_api=0 skip_web=0
  managed_api="$(alive_pid_from_file "$API_PID_FILE" 2>/dev/null || true)"
  managed_web="$(alive_pid_from_file "$WEB_PID_FILE" 2>/dev/null || true)"
  api_owner="$(port_owner "$api_port_value" 2>/dev/null || true)"
  web_owner="$(port_owner "$WEB_PORT" 2>/dev/null || true)"
  port_listening "$api_port_value" && api_listening=1
  port_listening "$WEB_PORT" && web_listening=1

  if [[ -z "$managed_api" && "$api_listening" -eq 1 ]]; then
    if [[ -n "$api_owner" ]]; then
      echo "Warning: port $api_port_value is already used by PID $api_owner ($(process_name "$api_owner")); skipping API start." >&2
    else
      echo "Warning: port $api_port_value already has an external listener; skipping API start." >&2
    fi
    skip_api=1
  fi
  if [[ -z "$managed_web" && "$web_listening" -eq 1 ]]; then
    if [[ -n "$web_owner" ]]; then
      echo "Warning: port $WEB_PORT is already used by PID $web_owner ($(process_name "$web_owner")); skipping Web start." >&2
    else
      echo "Warning: port $WEB_PORT already has an external listener; skipping Web start." >&2
    fi
    skip_web=1
  fi

  if [[ "$skip_api" -eq 0 ]]; then
    start_api_service "$api_env_file"
  fi
  if [[ "$skip_web" -eq 0 ]]; then
    VITE_API_PROXY_TARGET="${VITE_API_PROXY_TARGET:-http://127.0.0.1:$api_port_value}" \
      start_service "Web" "$WEB_PID_FILE" "$WEB_ROOT" "$WEB_LOG" "$WEB_ERR_LOG" npm run dev -- --host 0.0.0.0 --port "$WEB_PORT"
  fi

  log_dev_message ""
  show_status
  log_dev_message ""
  log_dev_message "Open http://127.0.0.1:$WEB_PORT"
  log_dev_message "LAN/Netbird http://<host-ip>:$WEB_PORT"
}

stop_service_process() {
  local name="$1"
  local pid_file="$2"
  local port="$3"
  local pid
  pid="$(alive_pid_from_file "$pid_file" 2>/dev/null || true)"
  if [[ -z "$pid" ]]; then
    rm -f "$pid_file"
    if ! stop_port_owner "$name" "$port"; then
      log_dev_message "$name is not running."
    fi
    return
  fi

  stop_pid "$name" "$pid"
  rm -f "$pid_file"
  log_dev_message "$name stopped (PID $pid)."

  for _ in {1..25}; do
    if ! port_listening "$port"; then
      return
    fi
    sleep 0.2
  done
  stop_port_owner "$name" "$port" >/dev/null 2>&1 || true
}

stop_all() {
  stop_service_process "Web" "$WEB_PID_FILE" "$WEB_PORT"
  stop_service_process "API" "$API_PID_FILE" "$(api_port)"
}

show_status() {
  local api_pid web_pid api_owner web_owner api_port_value api_listening=0 web_listening=0
  api_port_value="$(api_port)"
  api_pid="$(alive_pid_from_file "$API_PID_FILE" 2>/dev/null || true)"
  web_pid="$(alive_pid_from_file "$WEB_PID_FILE" 2>/dev/null || true)"
  api_owner="$(port_owner "$api_port_value" 2>/dev/null || true)"
  web_owner="$(port_owner "$WEB_PORT" 2>/dev/null || true)"
  port_listening "$api_port_value" && api_listening=1
  port_listening "$WEB_PORT" && web_listening=1

  log_dev_message "Orrery dev services"
  if [[ -n "$api_pid" ]]; then
    log_dev_message "  API: running (PID $api_pid, http://$API_ADDR)"
  elif [[ -n "$api_owner" ]]; then
    log_dev_message "  API: external (PID $api_owner, $(process_name "$api_owner"), http://$API_ADDR)"
  elif [[ "$api_listening" -eq 1 ]]; then
    log_dev_message "  API: external listener (PID unavailable, http://$API_ADDR)"
  else
    log_dev_message "  API: stopped"
  fi

  if [[ -n "$web_pid" ]]; then
    log_dev_message "  Web: running (PID $web_pid, http://127.0.0.1:$WEB_PORT)"
  elif [[ -n "$web_owner" ]]; then
    log_dev_message "  Web: external (PID $web_owner, $(process_name "$web_owner"), http://127.0.0.1:$WEB_PORT)"
  elif [[ "$web_listening" -eq 1 ]]; then
    log_dev_message "  Web: external listener (PID unavailable, http://127.0.0.1:$WEB_PORT)"
  else
    log_dev_message "  Web: stopped"
  fi

  log_dev_message "  Ports:"
  if [[ "$api_listening" -eq 1 ]]; then
    if [[ -n "$api_owner" ]]; then
      log_dev_message "    API $api_port_value listening: True (PID $api_owner, $(process_name "$api_owner"))"
    else
      log_dev_message "    API $api_port_value listening: True (PID unavailable)"
    fi
  else
    log_dev_message "    API $api_port_value listening: False"
  fi
  if [[ "$web_listening" -eq 1 ]]; then
    if [[ -n "$web_owner" ]]; then
      log_dev_message "    Web $WEB_PORT listening: True (PID $web_owner, $(process_name "$web_owner"))"
    else
      log_dev_message "    Web $WEB_PORT listening: True (PID unavailable)"
    fi
  else
    log_dev_message "    Web $WEB_PORT listening: False"
  fi
  log_dev_message "  Logs: $RUN_ROOT"
  log_dev_message "    API combined: $API_OUT"
}

show_logs() {
  ensure_run_root
  local files=()
  local file
  for file in "$API_OUT" "$API_LOG" "$API_ERR_LOG" "$WEB_LOG" "$WEB_ERR_LOG"; do
    [[ -f "$file" ]] && files+=("$file")
  done
  if [[ "${#files[@]}" -eq 0 ]]; then
    log_dev_message "No logs yet. Start services first."
    return
  fi
  if [[ "$FOLLOW" -eq 1 ]]; then
    tail -n 80 -f "${files[@]}"
  else
    for file in "${files[@]}"; do
      log_dev_message ""
      log_dev_message "== $file =="
      tail -n 80 "$file"
    done
  fi
}

case "$ACTION" in
  start) start_all ;;
  stop) stop_all ;;
  restart)
    stop_all
    start_all
    ;;
  status) show_status ;;
  logs) show_logs ;;
esac
