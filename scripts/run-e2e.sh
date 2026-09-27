#!/usr/bin/env bash
set -euo pipefail

root_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "${root_directory}"

# Build the server binary and run that, not `go run`: killing a `go run` parent
# leaves the compiled child serving (and holding the Worker port) after the test.
owns_artifact_directory=0
if [[ -n "${DEX_TEST_ARTIFACT_DIR:-}" ]]; then
  artifact_directory="${DEX_TEST_ARTIFACT_DIR}"
else
  artifact_directory="$(mktemp -d "${TMPDIR:-/tmp}/dex-tech-blog-e2e.XXXXXX")"
  owns_artifact_directory=1
fi
server_binary="${artifact_directory}/server"
app_log="${artifact_directory}/application.log"
port="${E2E_PORT:-18080}"
app_pid=""

cleanup() {
  local exit_code=$? attempts=0
  trap - EXIT INT TERM
  if [[ -n "${app_pid}" ]] && kill "${app_pid}" 2>/dev/null; then
    while kill -0 "${app_pid}" 2>/dev/null && (( attempts < 150 )); do
      sleep 0.1
      attempts=$((attempts + 1))
    done
    kill -9 "${app_pid}" 2>/dev/null || true
  fi
  if [[ -n "${app_pid}" ]]; then wait "${app_pid}" 2>/dev/null || true; fi
  if (( exit_code != 0 )) && [[ -f "${app_log}" ]]; then
    echo "Application log (${app_log}):" >&2
    cat "${app_log}" >&2 || true
  fi
  if (( owns_artifact_directory == 1 && exit_code == 0 )); then rm -rf -- "${artifact_directory}"; fi
  exit "${exit_code}"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

npm --prefix web run build
go build -o "${server_binary}" ./cmd/server
PORT="${port}" "${server_binary}" >"${app_log}" 2>&1 &
app_pid=$!
deadline=$((SECONDS + 45))
until curl --fail --silent "http://127.0.0.1:${port}/api/application-info" >/dev/null; do
  if ! kill -0 "${app_pid}" 2>/dev/null; then
    echo "Application server exited before answering on 127.0.0.1:${port}." >&2
    exit 1
  fi
  if (( SECONDS >= deadline )); then
    echo "Application server did not answer on 127.0.0.1:${port} within 45s." >&2
    exit 1
  fi
  sleep 0.1
done
E2E_BASE_URL="http://127.0.0.1:${port}" npm --prefix web run test:e2e
