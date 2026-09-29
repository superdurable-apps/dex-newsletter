#!/usr/bin/env bash
set -euo pipefail

# Long-lived local Dex Server and Dex Web with persistent state under .dex-dev/.
# Dex Web: http://127.0.0.1:${DEX_WEB_PORT:-8842}. Connections are stored in .dex-dev/connectors/.
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
state="${root}/.dex-dev"
mkdir -p "${state}/flows" "${state}/connectors" "${state}/logs"
FLOW_RENDERING_DIR="${state}/flows" "${root}/scripts/check-fdg-v2.sh"
exec "${DEXCLI:-dexcli}" dev -open=false \
  -dex-port "${DEX_PORT:-8841}" -web-port "${DEX_WEB_PORT:-8842}" \
  -flow-rendering-dir "${state}/flows" -connector-config-dir "${state}/connectors" \
  -blob-store-dir "${state}/blobs" -sqlite-db-filename "${state}/dex.sqlite" -server-log-folder "${state}/logs"
