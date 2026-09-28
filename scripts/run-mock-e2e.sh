#!/usr/bin/env bash
set -euo pipefail

# Runs the mock Playwright journey against scripts/with-mock.sh on free ports.
root_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "${root_directory}"

free_port() {
  python3 -c 'import socket; server = socket.socket(); server.bind(("127.0.0.1", 0)); print(server.getsockname()[1]); server.close()'
}

api_port="${MOCK_API_PORT:-$(free_port)}"
web_port="${MOCK_WEB_PORT:-$(free_port)}"
MOCK_API_PORT="${api_port}" \
MOCK_WEB_HOST="127.0.0.1" \
MOCK_WEB_PORT="${web_port}" \
./scripts/with-mock.sh env \
  E2E_BASE_URL="http://127.0.0.1:${web_port}" \
  E2E_MOCK=true \
  npm --prefix web run test:e2e:mock
