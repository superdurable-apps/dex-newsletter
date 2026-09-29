#!/usr/bin/env bash
set -euo pipefail

test_directory="$(mktemp -d "${TMPDIR:-/tmp}/dex-basic-process.XXXXXX")"
free_port() {
  python3 -c 'import socket; server = socket.socket(); server.bind(("127.0.0.1", 0)); print(server.getsockname()[1]); server.close()'
}
dex_port="${DEX_TEST_PORT:-$(free_port)}"
web_port="${DEX_TEST_WEB_PORT:-$(free_port)}"
worker_port="${DEX_TEST_WORKER_PORT:-$(free_port)}"
export E2E_PORT="${E2E_PORT:-$(free_port)}"
export DEX_FLOW_SERVICE_ADDRESS="127.0.0.1:${dex_port}"
export DEX_BLOB_CACHE_DIR="${test_directory}/application-blobs"
export DEX_WORKER_BIND_ADDRESS="127.0.0.1:${worker_port}"
export DEX_WORKER_TARGET="127.0.0.1:${worker_port}"
export DEX_TEST_ARTIFACT_DIR="${test_directory}"

cleanup() {
  exit_code=$?
  trap - EXIT INT TERM
  if [[ -n "${dex_pid:-}" ]]; then kill "${dex_pid}" 2>/dev/null || true; wait "${dex_pid}" 2>/dev/null || true; fi
  if [[ "${exit_code}" -eq 0 ]]; then
    rm -rf -- "${test_directory}"
  else
    cat "${test_directory}/dex.log" >&2 || true
    echo "Dex test artifacts: ${test_directory}" >&2
  fi
  exit "${exit_code}"
}
trap cleanup EXIT INT TERM

dexcli dev -open=false -dex-port "${dex_port}" -web-port "${web_port}" -blob-store-dir "${test_directory}/dex-blobs" -sqlite-db-filename "${test_directory}/dex.sqlite" -server-log-folder "${test_directory}/logs" >"${test_directory}/dex.log" 2>&1 &
dex_pid=$!
deadline=$((SECONDS + 45))
until dexcli health -server "${DEX_FLOW_SERVICE_ADDRESS}" -timeout 1s >/dev/null 2>&1; do
  if (( SECONDS >= deadline )); then cat "${test_directory}/dex.log"; exit 1; fi
  sleep 0.1
done
"$@"
