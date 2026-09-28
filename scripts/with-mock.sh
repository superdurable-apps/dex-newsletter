#!/usr/bin/env bash
set -euo pipefail

# Starts the in-memory mock API (cmd/mock-server) and Vite HMR without Dex.
# Vite proxies /api and /__mock__ to the mock API. With arguments, runs them
# once both answer and stops both afterwards; without, serves until Ctrl-C.
root_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "${root_directory}"

mock_directory="$(mktemp -d "${TMPDIR:-/tmp}/dex-tech-blog-mock.XXXXXX")"
api_host="${MOCK_API_HOST:-127.0.0.1}"
api_port="${MOCK_API_PORT:-18081}"
web_host="${MOCK_WEB_HOST:-0.0.0.0}"
web_port="${MOCK_WEB_PORT:-8080}"
api_binary="${mock_directory}/mock-server"
api_log="${mock_directory}/api.log"
web_log="${mock_directory}/web.log"
api_pid=""
web_pid=""

stop_process() {
  local pid="$1" attempts=0
  if [[ -z "${pid}" ]]; then return 0; fi
  if kill "${pid}" 2>/dev/null; then
    while kill -0 "${pid}" 2>/dev/null && (( attempts < 150 )); do
      sleep 0.1
      attempts=$((attempts + 1))
    done
    kill -9 "${pid}" 2>/dev/null || true
  fi
  wait "${pid}" 2>/dev/null || true
}

cleanup() {
  local exit_code=$?
  trap - EXIT INT TERM
  stop_process "${web_pid}"
  stop_process "${api_pid}"
  if (( exit_code == 0 || exit_code == 130 || exit_code == 143 )); then
    rm -rf -- "${mock_directory}"
  else
    cat "${api_log}" >&2 || true
    cat "${web_log}" >&2 || true
    echo "Mock artifacts: ${mock_directory}" >&2
  fi
  exit "${exit_code}"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# A readiness probe must never be answered by another stack on the same port.
port_in_use() {
  python3 -c 'import socket, sys; probe = socket.socket(); probe.settimeout(0.5); sys.exit(0 if probe.connect_ex(("127.0.0.1", int(sys.argv[1]))) == 0 else 1)' "$1"
}
for port in "${api_port}" "${web_port}"; do
  if port_in_use "${port}"; then
    echo "Port 127.0.0.1:${port} is already in use; choose other ports with MOCK_API_PORT and MOCK_WEB_PORT." >&2
    exit 1
  fi
done

# Build the binary and run that, not `go run`: killing a `go run` parent leaves
# the compiled child serving after the script exits.
go build -o "${api_binary}" ./cmd/mock-server
MOCK_API_ADDRESS="${api_host}:${api_port}" "${api_binary}" >"${api_log}" 2>&1 &
api_pid=$!

deadline=$((SECONDS + 45))
until curl --fail --silent "http://${api_host}:${api_port}/api/application-info" >/dev/null; do
  if ! kill -0 "${api_pid}" 2>/dev/null || (( SECONDS >= deadline )); then
    echo "Mock API did not answer on ${api_host}:${api_port}." >&2
    exit 1
  fi
  sleep 0.1
done

VITE_MOCK_API_TARGET="http://${api_host}:${api_port}" \
npm --prefix web run mock -- --host "${web_host}" --port "${web_port}" --strictPort >"${web_log}" 2>&1 &
web_pid=$!

deadline=$((SECONDS + 45))
until curl --fail --silent "http://127.0.0.1:${web_port}" >/dev/null; do
  if ! kill -0 "${web_pid}" 2>/dev/null || (( SECONDS >= deadline )); then
    echo "Vite did not answer on 127.0.0.1:${web_port}." >&2
    exit 1
  fi
  sleep 0.1
done

echo "Mock UI: http://127.0.0.1:${web_port} (mock API ${api_host}:${api_port})"
if (( $# > 0 )); then
  "$@"
else
  wait "${web_pid}"
fi
