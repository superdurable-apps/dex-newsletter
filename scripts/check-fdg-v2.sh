#!/usr/bin/env bash
set -euo pipefail

root_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "${root_directory}"
# shellcheck source=scripts/dexcli-baseline.sh
source "${root_directory}/scripts/dexcli-baseline.sh"
require_dexcli_baseline

shopt -s nullglob
flow_files=(internal/techblog/*_flow.go)
shopt -u nullglob
if (( ${#flow_files[@]} == 0 )); then
  echo "No Flow files match internal/techblog/*_flow.go; nothing to validate." >&2
  exit 1
fi

# A gitignored go.work means unreleased local connector modules are in use. Only
# then is connector_release_required tolerated, and it is reported as a release
# blocker; without go.work it fails like every other diagnostic.
local_connector_override=0
if [[ -f "${root_directory}/go.work" ]]; then
  local_connector_override=1
fi
export LOCAL_CONNECTOR_OVERRIDE="${local_connector_override}"

output_directory="$(mktemp -d "${TMPDIR:-/tmp}/dex-tech-blog-fdg-v2.XXXXXX")"
trap 'rm -rf -- "${output_directory}"' EXIT

failed_files=()
for flow_file in "${flow_files[@]}"; do
  output_base="${output_directory}/$(basename "${flow_file}" .go)"
  visualize_status=0
  "${DEXCLI}" visualize "${flow_file}" \
    --schema-version 2.0 \
    --json \
    --out "${output_base}" || visualize_status=$?
  if ! python3 - "${flow_file}" "${output_base}.json" "${visualize_status}" <<'PY'
import json
import os
import sys

flow_file, path, status = sys.argv[1], sys.argv[2], int(sys.argv[3])
if not os.path.exists(path):
    raise SystemExit(f"FAIL {flow_file}: dexcli visualize exited {status} without writing FDG 2.0 JSON")
with open(path) as handle:
    document = json.load(handle)
diagnostics = document.get("diagnostics") or []
release_blockers = [
    diagnostic for diagnostic in diagnostics
    if diagnostic.get("code") == "connector_release_required" and diagnostic.get("severity") == "warning"
]
if os.environ.get("LOCAL_CONNECTOR_OVERRIDE") == "1" and release_blockers:
    diagnostics = [diagnostic for diagnostic in diagnostics if diagnostic not in release_blockers]
    print(f"RELEASE BLOCKER {flow_file}: {len(release_blockers)} Connector Step(s) use an unreleased local connector module (go.work)")
if status != 0 and not diagnostics and release_blockers and os.environ.get("LOCAL_CONNECTOR_OVERRIDE") == "1":
    status = 0
if status != 0 or document.get("schemaVersion") != "2.0" or document.get("valid") is not True or diagnostics:
    raise SystemExit(
        f"FAIL {flow_file}: FDG 2.0 graph is not clean "
        f"(dexcli exit {status}, schemaVersion={document.get('schemaVersion')!r}, "
        f"valid={document.get('valid')!r}, {len(diagnostics)} diagnostics):\n"
        f"{json.dumps(diagnostics, indent=2)}"
    )
print(f"validated FDG 2.0 graph: {flow_file}")
PY
  then
    failed_files+=("${flow_file}")
  fi
done

if (( ${#failed_files[@]} > 0 )); then
  echo "FDG 2.0 validation failed for: ${failed_files[*]}" >&2
  exit 1
fi
echo "validated ${#flow_files[@]} FDG 2.0 Flow graphs"
