#!/usr/bin/env bash
set -euo pipefail

# Runs the Worker, Slack Trigger, and reader pages against scripts/dev-dex.sh.
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "${root}"
npm --prefix web run build
export DEX_CONNECTOR_CONFIG_FILE="${DEX_CONNECTOR_CONFIG_FILE:-${root}/.dex-dev/connectors/connections.json}"
export DEX_FLOW_SERVICE_ADDRESS="${DEX_FLOW_SERVICE_ADDRESS:-127.0.0.1:${DEX_PORT:-8841}}"
export DEX_WORKER_BIND_ADDRESS="${DEX_WORKER_BIND_ADDRESS:-127.0.0.1:8843}"
export DEX_WORKER_TARGET="${DEX_WORKER_TARGET:-${DEX_WORKER_BIND_ADDRESS}}"
export DEX_BLOB_CACHE_DIR="${DEX_BLOB_CACHE_DIR:-${root}/.dex-dev/application-blobs}"
export PORT="${PORT:-8844}"
# A named binary lets a restart find the old process: pkill -f bin/blog-newsletter
if pgrep -f "${root}/bin/blog-newsletter" >/dev/null; then
  echo "blog-newsletter is already running; stop it with: pkill -f ${root}/bin/blog-newsletter" >&2
  exit 1
fi
go build -o bin/blog-newsletter ./cmd/server
exec "${root}/bin/blog-newsletter"
