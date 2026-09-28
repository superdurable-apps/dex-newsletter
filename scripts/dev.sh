#!/usr/bin/env bash
# Local development stack with persistent state in .dex-dev/ (gitignored).
#
#   scripts/dev.sh dex   Dex Server + Dex Web in the foreground        (make dev-dex)
#   scripts/dev.sh app   build the page, run the server + Dex Worker   (make dev-app)
#   scripts/dev.sh all   both; Dex stops when the application stops    (make dev)
#
# Runs, blobs, and logs survive restarts. Delete .dex-dev/ to start over.
set -euo pipefail

root_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "${root_directory}"
# shellcheck source=scripts/dexcli-baseline.sh
source "${root_directory}/scripts/dexcli-baseline.sh"

mode="${1:-all}"
case "${mode}" in
  dex | app | all) ;;
  *)
    echo "usage: scripts/dev.sh [dex|app|all]" >&2
    exit 2
    ;;
esac

state_directory="${DEX_DEV_STATE_DIR:-${root_directory}/.dex-dev}"
mkdir -p "${state_directory}"
state_directory="$(cd "${state_directory}" && pwd -P)"
dex_port="${DEX_DEV_PORT:-8801}"
web_port="${DEX_DEV_WEB_PORT:-8802}"
connector_config_directory="${DEX_DEV_CONNECTOR_CONFIG_DIR:-${HOME}/.dex/connectors}"
flow_definition_directory="${state_directory}/flow-definitions"
connector_artifact_directory="${state_directory}/connector-artifacts"
application_binary="${state_directory}/bin/dex-tech-blog"

port_in_use() {
  python3 - "$1" <<'PY'
import socket
import sys

with socket.socket() as probe:
    probe.settimeout(0.5)
    sys.exit(0 if probe.connect_ex(("127.0.0.1", int(sys.argv[1]))) == 0 else 1)
PY
}

require_free_dex_ports() {
  local port
  for port in "${dex_port}" "${web_port}"; do
    if port_in_use "${port}"; then
      echo "Port 127.0.0.1:${port} is already in use. If it is 'make dev-dex', run 'make dev-app' instead;" >&2
      echo "otherwise stop that process or choose other ports with DEX_DEV_PORT and DEX_DEV_WEB_PORT." >&2
      exit 1
    fi
  done
}

# Prints the error diagnostics of a partial FDG written by a failed visualize.
summarize_flow_definition_errors() {
  python3 - "$1" <<'PY'
import json
import sys

with open(sys.argv[1]) as handle:
    document = json.load(handle)
errors = [d for d in document.get("diagnostics") or [] if d.get("severity") == "error"]
for diagnostic in errors[:5]:
    print(f"  {diagnostic.get('code')}: {diagnostic.get('message')}")
if len(errors) > 5:
    print(f"  ... {len(errors) - 5} more")
print(f"  valid={document.get('valid')}; the Dex Web catalog (Work Queue, Actions) stays disabled while graphs are invalid."
      " Fix them ('make check-fdg-v2') and restart Dex.")
PY
}

# Dex Web builds Connections, the Work Queue, and Actions from Flow Definition
# Graphs, so every Flow file is rendered with schema 2.0 on each Dex start.
generate_flow_definitions() {
  local flow_file name output status
  shopt -s nullglob
  local flow_files=(internal/techblog/*_flow.go)
  shopt -u nullglob
  if (( ${#flow_files[@]} == 0 )); then
    echo "No Flow files match internal/techblog/*_flow.go." >&2
    exit 1
  fi
  mkdir -p "${flow_definition_directory}"
  rm -f -- "${flow_definition_directory}"/*.json
  for flow_file in "${flow_files[@]}"; do
    name="$(basename "${flow_file}" .go)"
    status=0
    output="$("${DEXCLI}" visualize "${flow_file}" --schema-version 2.0 --json --out "${flow_definition_directory}/${name}" 2>&1)" || status=$?
    if [[ ! -s "${flow_definition_directory}/${name}.json" ]]; then
      printf '%s\n' "${output}" >&2
      echo "dexcli visualize exited ${status} without writing an FDG 2.0 graph for ${flow_file}." >&2
      exit 1
    fi
    if (( status != 0 )); then
      echo "warning: dexcli visualize exited ${status} for ${flow_file}; its graph is loaded but may be invalid:" >&2
      summarize_flow_definition_errors "${flow_definition_directory}/${name}.json" >&2 || printf '%s\n' "${output}" >&2
    fi
  done
  echo "Flow Definition Graphs (FDG 2.0): ${#flow_files[@]} in ${flow_definition_directory}"
}

# While go.work points at unreleased connector checkouts, Dex needs release
# metadata built from that source (--connector-release-override); without it
# those connections show "Unsupported" in Dex Web. Failures are reported and
# skipped so Dex still starts. DEX_DEV_CONNECTOR_RELEASE_OVERRIDE=0 disables it.
connector_release_overrides=()

# Prints every go.work "use" directory except the application module itself.
go_work_module_directories() {
  local work_json
  work_json="$(go work edit -json "${root_directory}/go.work")" || return 1
  python3 - "${work_json}" <<'PY'
import json
import sys

for use in json.loads(sys.argv[1]).get("Use") or []:
    if use.get("DiskPath") not in (None, "", "."):
        print(use["DiskPath"])
PY
}

build_connector_release_overrides() {
  local module_directory
  if [[ ! -f "${root_directory}/go.work" ]]; then
    return 0
  fi
  if [[ "${DEX_DEV_CONNECTOR_RELEASE_OVERRIDE:-1}" == "0" ]]; then
    echo "Connector release overrides disabled (DEX_DEV_CONNECTOR_RELEASE_OVERRIDE=0); local connectors show as Unsupported in Dex Web." >&2
    return 0
  fi
  local module_directories
  if ! module_directories="$(go_work_module_directories)"; then
    echo "warning: could not read go.work; starting Dex without connector release overrides." >&2
    return 0
  fi
  while IFS= read -r module_directory; do
    [[ -n "${module_directory}" ]] || continue
    if [[ "${module_directory}" != /* ]]; then
      module_directory="${root_directory}/${module_directory}"
    fi
    build_connector_release_override "${module_directory}" || true
  done <<<"${module_directories}"
}

# Prints "<metadata.name> <metadata.version> <studio|->" for a connector.yaml.
read_connector_metadata() {
  python3 - "$1" <<'PY'
import re
import sys

values, section = {}, None
studio = False
with open(sys.argv[1]) as handle:
    for line in handle:
        top_level = re.match(r"^([A-Za-z]\w*):", line)
        if top_level:
            section = top_level.group(1)
            continue
        field = re.match(r"^  (\w+):\s*(\S*)", line)
        if not field:
            continue
        if section == "metadata" and field.group(1) in ("name", "version"):
            values[field.group(1)] = field.group(2).strip("'\"")
        if section == "spec" and field.group(1) == "studio":
            studio = True
if not values.get("name") or not values.get("version"):
    sys.exit(1)
print(values["name"], values["version"], "studio" if studio else "-")
PY
}

# Builds a connector's Connector Studio UI tarball the way the library's
# release workflow does: the shared React SDK first, then the connector's ui/
# package, then connectorctl ui-artifact. npm output goes to ui-build.log.
build_connector_ui_artifact() {
  local checkout="$1" module_directory="$2" manifest="$3" destination="$4" log
  log="${destination}/ui-build.log"
  if [[ ! -f "${module_directory}/ui/package-lock.json" ]]; then
    echo "warning: ${module_directory}/ui has no package-lock.json." >&2
    return 1
  fi
  echo "Building the Connector Studio UI in ${module_directory}/ui ..."
  (npm ci --prefix "${checkout}/sdk/react" && npm run build --prefix "${checkout}/sdk/react") >"${log}" 2>&1 || return 1
  (cd "${module_directory}/ui" && npm ci && npm run build) >>"${log}" 2>&1 || return 1
  (cd "${checkout}" && GOWORK=off go run ./cmd/connectorctl ui-artifact \
    --manifest "${manifest}" \
    --ui-root "${module_directory}/ui/dist" \
    --output "${destination}/connector-ui.tgz" \
    --digest-output "${destination}/connector-ui.tgz.sha256")
}

build_connector_release_override() {
  local module_directory="$1" manifest checkout metadata connector_id version studio module_path relative source_sha destination
  if [[ ! -d "${module_directory}" ]]; then
    echo "warning: go.work module ${module_directory} does not exist; no connector release override for it." >&2
    return 1
  fi
  module_directory="$(cd "${module_directory}" && pwd -P)"
  manifest="${module_directory}/connector.yaml"
  if [[ ! -f "${manifest}" ]]; then
    return 0 # not a connector module (for example a local SDK)
  fi
  if ! metadata="$(read_connector_metadata "${manifest}")"; then
    echo "warning: could not read metadata.name/metadata.version from ${manifest}; no connector release override for it." >&2
    return 1
  fi
  read -r connector_id version studio <<<"${metadata}"
  if ! checkout="$(git -C "${module_directory}" rev-parse --show-toplevel 2>/dev/null)" || [[ ! -d "${checkout}/cmd/connectorctl" ]]; then
    echo "warning: ${module_directory} is not inside a dex-connectors-library checkout with cmd/connectorctl;" >&2
    echo "         connector ${connector_id} has no release override and shows as Unsupported in Dex Web." >&2
    return 1
  fi
  checkout="$(cd "${checkout}" && pwd -P)"
  module_path="$(awk '$1 == "module" { print $2; exit }' "${module_directory}/go.mod")"
  relative="${module_directory#"${checkout}"/}"
  source_sha="$(git -C "${checkout}" rev-parse HEAD)"
  destination="${connector_artifact_directory}/${connector_id}"
  rm -rf -- "${destination}"
  mkdir -p "${destination}"
  local ui_arguments=()
  if [[ "${studio}" == "studio" ]]; then
    if ! build_connector_ui_artifact "${checkout}" "${module_directory}" "${manifest}" "${destination}"; then
      echo "warning: the Connector Studio UI build failed for ${connector_id} (log: ${destination}/ui-build.log);" >&2
      echo "         it has no release override and shows as Unsupported in Dex Web." >&2
      return 1
    fi
    ui_arguments=(--ui-artifact "${destination}/connector-ui.tgz" --ui-digest "${destination}/connector-ui.tgz.sha256")
  fi
  # Same invocation as the library's release workflow (GOWORK=off, tag
  # <module-dir>/<version>), pointed at the local checkout's HEAD.
  if ! (cd "${checkout}" && GOWORK=off go run ./cmd/connectorctl release-artifact \
    --manifest "${manifest}" \
    --module-path "${module_path}" \
    --version "${version}" \
    --tag "${relative}/${version}" \
    --source-sha "${source_sha}" \
    --output "${destination}/connector-release.json" \
    --digest-output "${destination}/connector-release.json.sha256" \
    ${ui_arguments[@]+"${ui_arguments[@]}"}); then
    echo "warning: connectorctl release-artifact failed for ${connector_id}; it shows as Unsupported in Dex Web." >&2
    return 1
  fi
  connector_release_overrides+=(--connector-release-override "${connector_id}=${destination}")
  echo "Connector release override: ${connector_id} ${version} (${checkout} @ ${source_sha:0:12}) -> ${destination}"
}

dex_arguments=()
prepare_dex() {
  require_dexcli_baseline
  require_free_dex_ports
  generate_flow_definitions
  build_connector_release_overrides
  mkdir -p "${state_directory}/dex-blobs" "${state_directory}/logs" "${connector_config_directory}"
  dex_arguments=(
    dev
    -open="${DEX_DEV_OPEN:-false}"
    -dex-port "${dex_port}"
    -web-port "${web_port}"
    -sqlite-db-filename "${state_directory}/dex.sqlite.db"
    -blob-store-dir "${state_directory}/dex-blobs"
    -server-log-folder "${state_directory}/logs"
    -flow-rendering-dir "${flow_definition_directory}"
    -connector-config-dir "${connector_config_directory}"
  )
  if (( ${#connector_release_overrides[@]} > 0 )); then
    dex_arguments+=("${connector_release_overrides[@]}")
  fi
  echo "Dex state: ${state_directory} (delete it to reset Runs, blobs, and logs)"
  echo "Connections: ${connector_config_directory}/connections.json; export DEX_CONNECTOR_CONFIG_FILE to the path Dex Web Connections shows, then restart the application"
}

build_application() {
  npm --prefix web run build
  mkdir -p "$(dirname "${application_binary}")"
  go build -o "${application_binary}" ./cmd/server
}

application_environment() {
  export DEX_FLOW_SERVICE_ADDRESS="127.0.0.1:${dex_port}"
  export DEX_BLOB_CACHE_DIR="${state_directory}/application-blobs"
  mkdir -p "${DEX_BLOB_CACHE_DIR}"
  if [[ -z "${DEX_CONNECTOR_CONFIG_FILE:-}" ]]; then
    echo "warning: DEX_CONNECTOR_CONFIG_FILE is not set, so every connection is unconfigured. Export it to the" >&2
    echo "         connections file shown in Dex Web Connections (normally ${connector_config_directory}/connections.json)." >&2
  fi
  echo "Application: http://127.0.0.1:${PORT:-8080} (Dex FlowService ${DEX_FLOW_SERVICE_ADDRESS}, Dex Web http://127.0.0.1:${web_port})"
}

stop_process() {
  local pid="$1" attempts=0
  kill "${pid}" 2>/dev/null || return 0
  while kill -0 "${pid}" 2>/dev/null && (( attempts < 150 )); do
    sleep 0.1
    attempts=$((attempts + 1))
  done
  kill -9 "${pid}" 2>/dev/null || true
  wait "${pid}" 2>/dev/null || true
}

case "${mode}" in
  dex)
    prepare_dex
    exec "${DEXCLI}" "${dex_arguments[@]}"
    ;;
  app)
    build_application
    application_environment
    if ! port_in_use "${dex_port}"; then
      echo "warning: nothing answers on 127.0.0.1:${dex_port}; start Dex with 'make dev-dex'. The Worker keeps retrying." >&2
    fi
    exec "${application_binary}"
    ;;
  all)
    prepare_dex
    build_application
    dex_log="${state_directory}/dexcli.log"
    dex_pid=""
    application_pid=""
    cleanup() {
      local exit_code=$?
      trap - EXIT INT TERM
      if [[ -n "${application_pid}" ]]; then stop_process "${application_pid}"; fi
      if [[ -n "${dex_pid}" ]]; then stop_process "${dex_pid}"; fi
      exit "${exit_code}"
    }
    trap cleanup EXIT
    trap 'exit 130' INT
    trap 'exit 143' TERM
    "${DEXCLI}" "${dex_arguments[@]}" >"${dex_log}" 2>&1 &
    dex_pid=$!
    deadline=$((SECONDS + 60))
    until "${DEXCLI}" health -server "127.0.0.1:${dex_port}" -timeout 1s >/dev/null 2>&1; do
      if ! kill -0 "${dex_pid}" 2>/dev/null || (( SECONDS >= deadline )); then
        cat "${dex_log}" >&2
        echo "Dex did not become healthy on 127.0.0.1:${dex_port}." >&2
        exit 1
      fi
      sleep 0.2
    done
    echo "Dex Web: http://127.0.0.1:${web_port} (Dex FlowService 127.0.0.1:${dex_port}; dexcli log ${dex_log})"
    application_environment
    "${application_binary}" &
    application_pid=$!
    status=0
    wait "${application_pid}" || status=$?
    application_pid=""
    exit "${status}"
    ;;
esac
