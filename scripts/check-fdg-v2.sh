#!/usr/bin/env bash
set -euo pipefail

# Validates every Flow as a diagnostic-free FDG 2.0 graph. With FLOW_RENDERING_DIR set,
# the JSON graphs are kept there for `dexcli dev --flow-rendering-dir`.
dexcli_binary="${DEXCLI:-dexcli}"
flow_sources=(internal/blogpost/flow.go internal/subscribers/flow.go)
output_directory="${FLOW_RENDERING_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/dex-blog-newsletter-fdg.XXXXXX")}"
mkdir -p "${output_directory}"
if [[ -z "${FLOW_RENDERING_DIR:-}" ]]; then
  trap 'rm -rf -- "${output_directory}"' EXIT
fi

for source in "${flow_sources[@]}"; do
  name="$(basename "$(dirname "${source}")")"
  "${dexcli_binary}" visualize "${source}" --schema-version 2.0 --json --out "${output_directory}/${name}" >/dev/null
  python3 - "${output_directory}/${name}.json" "${source}" <<'PY'
import json
import sys

path, source = sys.argv[1], sys.argv[2]
document = json.load(open(path))
if document.get("valid") is not True:
    raise SystemExit(f"{source}: FDG 2.0 graph is invalid:\n{json.dumps(document.get('diagnostics', []), indent=2)}")
if document.get("diagnostics"):
    raise SystemExit(f"{source}: FDG 2.0 graph has diagnostics: {document['diagnostics']}")
print(f"validated FDG 2.0 graph for {source}")
PY
done
