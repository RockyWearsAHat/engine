#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
STATE_DIR="$REPO_ROOT/.engine/llama-fleet"
PIDS_DIR="$STATE_DIR/pids"
LOGS_DIR="$STATE_DIR/logs"
ENV_FILE_DEFAULT="$REPO_ROOT/.engine/llama-fleet.env"

if [[ -f "${LLAMA_FLEET_ENV:-$ENV_FILE_DEFAULT}" ]]; then
  # shellcheck disable=SC1090
  source "${LLAMA_FLEET_ENV:-$ENV_FILE_DEFAULT}"
fi

LLAMA_HOST="${LLAMA_HOST:-127.0.0.1}"
LLAMA_PORTS="${LLAMA_PORTS:-}"
LLAMA_BASE_PORT="${LLAMA_BASE_PORT:-8081}"
LLAMA_BACKENDS="${LLAMA_BACKENDS:-auto}"
LLAMA_PARALLEL="${LLAMA_PARALLEL:-2}"
LLAMA_CTX="${LLAMA_CTX:-8192}"
LLAMA_THREADS="${LLAMA_THREADS:-8}"
LLAMA_THREADS_BATCH="${LLAMA_THREADS_BATCH:-$LLAMA_THREADS}"
LLAMA_THREADS_HTTP="${LLAMA_THREADS_HTTP:-8}"
LLAMA_BATCH="${LLAMA_BATCH:-512}"
LLAMA_UBATCH="${LLAMA_UBATCH:-128}"
LLAMA_N_GPU_LAYERS="${LLAMA_N_GPU_LAYERS:-0}"
LLAMA_DEFRAG_THOLD="${LLAMA_DEFRAG_THOLD:-0.2}"
LLAMA_FLASH_ATTN="${LLAMA_FLASH_ATTN:-0}"
LLAMA_MLOCK="${LLAMA_MLOCK:-0}"
LLAMA_NO_MMAP="${LLAMA_NO_MMAP:-0}"
LLAMA_NUMA="${LLAMA_NUMA:-}"
LLAMA_MODEL_PATH="${LLAMA_MODEL_PATH:-}"
LLAMA_HF_REPO="${LLAMA_HF_REPO:-}"
LLAMA_HF_FILE="${LLAMA_HF_FILE:-}"
LLAMA_MODEL_NAME="${LLAMA_MODEL_NAME:-default}"

LLAMA_ROUTER_HOST="${LLAMA_ROUTER_HOST:-127.0.0.1}"
LLAMA_ROUTER_PORT="${LLAMA_ROUTER_PORT:-8080}"

err() {
  echo "error: $*" >&2
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || err "missing required command: $1"
}

ensure_dirs() {
  mkdir -p "$STATE_DIR" "$PIDS_DIR" "$LOGS_DIR"
}

backend_pid_file() {
  local port="$1"
  echo "$PIDS_DIR/backend-$port.pid"
}

router_pid_file() {
  echo "$PIDS_DIR/router.pid"
}

detect_cpu_cores() {
  local cores
  cores="$(getconf _NPROCESSORS_ONLN 2>/dev/null || true)"
  if [[ -z "$cores" ]]; then
    cores="1"
  fi
  if [[ "$cores" -lt 1 ]]; then
    cores="1"
  fi
  echo "$cores"
}

auto_backend_count() {
  local cores
  cores="$(detect_cpu_cores)"
  # Keep one core budgeted for desktop/system responsiveness.
  if [[ "$cores" -le 2 ]]; then
    echo "1"
    return
  fi
  local suggested=$(( cores / 3 ))
  if [[ "$suggested" -lt 1 ]]; then
    suggested=1
  fi
  if [[ "$suggested" -gt 6 ]]; then
    suggested=6
  fi
  echo "$suggested"
}

generate_port_list() {
  local backends="$1"
  local base_port="$2"
  local i
  for ((i = 0; i < backends; i++)); do
    echo $(( base_port + i ))
  done
}

port_list() {
  if [[ -n "${LLAMA_PORTS:-}" ]]; then
    echo "$LLAMA_PORTS" | tr ',' '\n' | awk 'NF > 0 {gsub(/^[ \t]+|[ \t]+$/, "", $0); print $0}'
    return
  fi

  local backend_count
  backend_count="${LLAMA_BACKENDS:-auto}"
  if [[ "$backend_count" == "auto" ]]; then
    backend_count="$(auto_backend_count)"
  fi
  if ! [[ "$backend_count" =~ ^[0-9]+$ ]]; then
    err "LLAMA_BACKENDS must be a positive integer or 'auto'"
  fi
  if [[ "$backend_count" -lt 1 ]]; then
    err "LLAMA_BACKENDS must be at least 1"
  fi
  generate_port_list "$backend_count" "$LLAMA_BASE_PORT"
}

is_pid_alive() {
  local pid="$1"
  if [[ -z "$pid" ]]; then
    return 1
  fi
  kill -0 "$pid" >/dev/null 2>&1
}

wait_health() {
  local url="$1"
  local retries="${2:-30}"
  local sleep_s="${3:-1}"

  for _ in $(seq 1 "$retries"); do
    if curl -fsS "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep "$sleep_s"
  done

  return 1
}

build_model_args() {
  if [[ -n "$LLAMA_MODEL_PATH" ]]; then
    printf '%s\n' "--model" "$LLAMA_MODEL_PATH"
    return
  fi

  if [[ -n "$LLAMA_HF_REPO" ]]; then
    printf '%s\n' "--hf-repo" "$LLAMA_HF_REPO"
    if [[ -n "$LLAMA_HF_FILE" ]]; then
      printf '%s\n' "--hf-file" "$LLAMA_HF_FILE"
    fi
    return
  fi

  err "set LLAMA_MODEL_PATH or LLAMA_HF_REPO (via env or .engine/llama-fleet.env)"
}

start_backends() {
  local model_args=()
  while IFS= read -r line; do
    model_args+=("$line")
  done < <(build_model_args)

  local runtime_args=(
    --ctx-size "$LLAMA_CTX"
    --parallel "$LLAMA_PARALLEL"
    --cont-batching
    --threads "$LLAMA_THREADS"
    --threads-batch "$LLAMA_THREADS_BATCH"
    --threads-http "$LLAMA_THREADS_HTTP"
    --batch-size "$LLAMA_BATCH"
    --ubatch-size "$LLAMA_UBATCH"
    --n-gpu-layers "$LLAMA_N_GPU_LAYERS"
    --defrag-thold "$LLAMA_DEFRAG_THOLD"
  )

  if [[ "$LLAMA_FLASH_ATTN" == "1" ]]; then
    runtime_args+=(--flash-attn)
  fi
  if [[ "$LLAMA_MLOCK" == "1" ]]; then
    runtime_args+=(--mlock)
  fi
  if [[ "$LLAMA_NO_MMAP" == "1" ]]; then
    runtime_args+=(--no-mmap)
  fi
  if [[ -n "$LLAMA_NUMA" ]]; then
    runtime_args+=(--numa "$LLAMA_NUMA")
  fi

  for port in $(port_list); do
    local pid_file
    pid_file="$(backend_pid_file "$port")"
    if [[ -f "$pid_file" ]] && is_pid_alive "$(cat "$pid_file")"; then
      echo "backend $port already running (pid $(cat "$pid_file"))"
      continue
    fi

    local log_file
    log_file="$LOGS_DIR/backend-$port.log"

    echo "starting backend on $LLAMA_HOST:$port"
    nohup llama-server \
      --host "$LLAMA_HOST" \
      --port "$port" \
      --alias "$LLAMA_MODEL_NAME" \
      "${runtime_args[@]}" \
      "${model_args[@]}" \
      >"$log_file" 2>&1 &

    echo "$!" >"$pid_file"

    if ! wait_health "http://$LLAMA_HOST:$port/health" 40 1; then
      err "backend on port $port failed health check. see $log_file"
    fi
  done
}

router_backends_env() {
  local targets=()
  for port in $(port_list); do
    targets+=("http://$LLAMA_HOST:$port")
  done

  local joined
  joined="$(IFS=,; echo "${targets[*]}")"
  echo "$LLAMA_MODEL_NAME=$joined"
}

start_router() {
  local pid_file
  pid_file="$(router_pid_file)"
  if [[ -f "$pid_file" ]] && is_pid_alive "$(cat "$pid_file")"; then
    echo "router already running (pid $(cat "$pid_file"))"
    return
  fi

  local log_file
  log_file="$LOGS_DIR/router.log"

  local backends
  backends="$(router_backends_env)"

  echo "starting router on $LLAMA_ROUTER_HOST:$LLAMA_ROUTER_PORT"
  LLAMA_ROUTER_HOST="$LLAMA_ROUTER_HOST" \
  LLAMA_ROUTER_PORT="$LLAMA_ROUTER_PORT" \
  LLAMA_ROUTER_BACKENDS="$backends" \
  nohup node "$REPO_ROOT/scripts/llama-router.mjs" >"$log_file" 2>&1 &

  echo "$!" >"$pid_file"

  if ! wait_health "http://$LLAMA_ROUTER_HOST:$LLAMA_ROUTER_PORT/health" 30 1; then
    err "router failed health check. see $log_file"
  fi
}

stop_one() {
  local pid_file="$1"
  if [[ ! -f "$pid_file" ]]; then
    return
  fi

  local pid
  pid="$(cat "$pid_file")"
  if is_pid_alive "$pid"; then
    kill "$pid" >/dev/null 2>&1 || true
    sleep 1
    if is_pid_alive "$pid"; then
      kill -9 "$pid" >/dev/null 2>&1 || true
    fi
  fi
  rm -f "$pid_file"
}

cmd_start() {
  ensure_dirs
  need_cmd llama-server
  need_cmd node
  need_cmd curl

  start_backends
  start_router

  echo
  echo "llama fleet ready"
  echo "  model alias:      $LLAMA_MODEL_NAME"
  echo "  router endpoint:  http://$LLAMA_ROUTER_HOST:$LLAMA_ROUTER_PORT"
  echo "  backends:         $(port_list | tr '\n' ' ' | sed 's/ *$//')"
  echo "  parallel slots:   $LLAMA_PARALLEL"
  echo "  thread config:    gen=$LLAMA_THREADS batch=$LLAMA_THREADS_BATCH http=$LLAMA_THREADS_HTTP"
  echo "  batch config:     batch=$LLAMA_BATCH ubatch=$LLAMA_UBATCH ctx=$LLAMA_CTX"
  echo "  memory config:    gpu_layers=$LLAMA_N_GPU_LAYERS defrag=$LLAMA_DEFRAG_THOLD mlock=$LLAMA_MLOCK no_mmap=$LLAMA_NO_MMAP"
  echo
  echo "set Engine to use the router:"
  echo "  export ENGINE_MODEL_PROVIDER=llamacpp"
  echo "  export LLAMACPP_BASE_URL=http://$LLAMA_ROUTER_HOST:$LLAMA_ROUTER_PORT"
  echo "  export ENGINE_MODEL=$LLAMA_MODEL_NAME"
}

cmd_stop() {
  stop_one "$(router_pid_file)"
  for port in $(port_list); do
    stop_one "$(backend_pid_file "$port")"
  done
  echo "llama fleet stopped"
}

cmd_status() {
  local healthy=1

  echo "router:"
  if [[ -f "$(router_pid_file)" ]] && is_pid_alive "$(cat "$(router_pid_file)")"; then
    echo "  running pid $(cat "$(router_pid_file)")"
    if curl -fsS "http://$LLAMA_ROUTER_HOST:$LLAMA_ROUTER_PORT/health" >/dev/null 2>&1; then
      echo "  health: ok"
    else
      echo "  health: failing"
      healthy=0
    fi
  else
    echo "  stopped"
    healthy=0
  fi

  echo "backends:"
  for port in $(port_list); do
    local pid_file
    pid_file="$(backend_pid_file "$port")"
    if [[ -f "$pid_file" ]] && is_pid_alive "$(cat "$pid_file")"; then
      echo "  $port: running pid $(cat "$pid_file")"
      if curl -fsS "http://$LLAMA_HOST:$port/health" >/dev/null 2>&1; then
        echo "    health: ok"
      else
        echo "    health: failing"
        healthy=0
      fi
    else
      echo "  $port: stopped"
      healthy=0
    fi
  done

  if [[ "$healthy" -eq 1 ]]; then
    exit 0
  fi
  exit 1
}

cmd_logs() {
  ensure_dirs
  tail -f "$LOGS_DIR"/*.log
}

case "${1:-}" in
  start)
    cmd_start
    ;;
  stop)
    cmd_stop
    ;;
  restart)
    cmd_stop
    cmd_start
    ;;
  status)
    cmd_status
    ;;
  logs)
    cmd_logs
    ;;
  *)
    echo "Usage: $0 {start|stop|restart|status|logs}"
    exit 1
    ;;
esac
