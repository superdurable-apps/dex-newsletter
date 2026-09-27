#!/usr/bin/env bash
# Sourced by scripts that invoke the Dex CLI. Resolves DEXCLI (default: dexcli
# on PATH) and fails fast unless it is the release pinned in DEX_CLI_BASELINE.

dexcli_baseline_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"

require_dexcli_baseline() {
  local baseline expected version_output version_line
  baseline="$(tr -d '[:space:]' <"${dexcli_baseline_root}/DEX_CLI_BASELINE")"
  expected="${baseline#cli-}"
  DEXCLI="${DEXCLI:-dexcli}"
  if ! command -v "${DEXCLI}" >/dev/null 2>&1; then
    echo "Dex CLI not found: DEXCLI=${DEXCLI}. Install Dex CLI ${expected} (DEX_CLI_BASELINE=${baseline}) and set DEXCLI to its path." >&2
    exit 1
  fi
  if ! version_output="$("${DEXCLI}" version)"; then
    echo "Dex CLI check failed: '${DEXCLI} version' exited with an error." >&2
    exit 1
  fi
  version_line="${version_output%%$'\n'*}"
  if [[ "${version_line}" != "dexcli ${expected}" && "${version_line}" != "dexcli ${expected} "* ]]; then
    echo "Dex CLI version mismatch: '${DEXCLI} version' reported '${version_line}', but DEX_CLI_BASELINE requires ${expected}." >&2
    echo "Set DEXCLI to a Dex CLI ${expected} binary, for example DEXCLI=\$HOME/.local/dexcli/${expected}/dexcli." >&2
    exit 1
  fi
  export DEXCLI
}
