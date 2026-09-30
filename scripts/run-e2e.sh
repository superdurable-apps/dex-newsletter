#!/usr/bin/env bash
set -euo pipefail

# Runs the Playwright journeys against the real Go application and the Dex Server that
# scripts/with-dex.sh started. e2eproviders fakes Slack, GitHub, Gemini, and Gmail on a free
# loopback port and writes the connection store; e2eseed prepares one BlogPost run awaiting
# review for the draft editor journey (see internal/testsupport/e2eseed for why it seeds with its
# own short-lived Worker). Everything is built into and runs from the test directory, so a local
# application serving web/dist is left alone.
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "${root}"
artifacts="${DEX_TEST_ARTIFACT_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/dex-blog-newsletter-e2e.XXXXXX")}"
port="${E2E_PORT:-18080}"
app_url="http://127.0.0.1:${port}"
bin="${artifacts}/bin"
# cmd/server serves web/dist relative to its working directory.
app_directory="${artifacts}/app"
mkdir -p "${bin}" "${app_directory}/web" "${artifacts}/connectors"

npm --prefix web run build -- --outDir "${app_directory}/web/dist" --emptyOutDir
go build -o "${bin}/" ./cmd/server ./internal/testsupport/e2eproviders ./internal/testsupport/e2eseed

providers_pid=""
app_pid=""
cleanup() {
  exit_code=$?
  trap - EXIT INT TERM
  for pid in ${app_pid} ${providers_pid}; do kill "${pid}" 2>/dev/null || true; done
  for pid in ${app_pid} ${providers_pid}; do wait "${pid}" 2>/dev/null || true; done
  if [[ "${exit_code}" -ne 0 ]]; then
    for log in providers application seed; do
      if [[ -s "${artifacts}/${log}.log" ]]; then echo "--- ${log}.log (last 60 lines)" >&2; tail -n 60 "${artifacts}/${log}.log" >&2; fi
    done
  fi
  exit "${exit_code}"
}
trap cleanup EXIT INT TERM

# wait_for SECONDS PID WHAT COMMAND... polls COMMAND until it succeeds, PID exits, or the deadline passes.
wait_for() {
  local seconds=$1 pid=$2 what=$3
  shift 3
  local deadline=$((SECONDS + seconds))
  until "$@"; do
    if (( SECONDS >= deadline )); then echo "timed out waiting for ${what}" >&2; return 1; fi
    if ! kill -0 "${pid}" 2>/dev/null; then echo "stopped before ${what}" >&2; return 1; fi
    sleep 0.2
  done
}

"${bin}/e2eproviders" -connections-dir "${artifacts}/connectors" -url-file "${artifacts}/fake-url" >"${artifacts}/providers.log" 2>&1 &
providers_pid=$!
wait_for 30 "${providers_pid}" "the fake providers" test -s "${artifacts}/fake-url"
fake_url="$(<"${artifacts}/fake-url")"

export DEX_CONNECTOR_CONFIG_FILE="${artifacts}/connectors/connections.json"
export BLOG_NEWSLETTER_CONFIG="${artifacts}/config.json"
export E2E_UNSUBSCRIBE_KEY_FILE="${artifacts}/unsubscribe.key"
export E2E_SEED_FILE="${artifacts}/editor-seed.json"
# Editor and unsubscribe links point at this application; the Dex Web link is never followed.
cat >"${BLOG_NEWSLETTER_CONFIG}" <<JSON
{"github":{"owners":["acme"]},"dexWebUrl":"https://dex-web.acme.test",
 "blog":{"artifactDirectory":"${artifacts}/blog"},
 "newsletter":{"publicBaseUrl":"${app_url}","unsubscribeKeyFile":"${E2E_UNSUBSCRIBE_KEY_FILE}","editorKeyFile":"${artifacts}/editor.key"}}
JSON

# The application's model calls go to the fake too, so a journey that should not generate can prove it.
(cd "${app_directory}" && SLACK_TRIGGER=off BLOG_NEWSLETTER_TEST_GEMINI_BASE_URL="${fake_url}" PORT="${port}" exec "${bin}/server") >"${artifacts}/application.log" 2>&1 &
app_pid=$!
app_ready() { curl --fail --silent "${app_url}/api/health" >/dev/null && [[ -s "${E2E_UNSUBSCRIBE_KEY_FILE}" ]]; }
wait_for 60 "${app_pid}" "the application at ${app_url}" app_ready

"${bin}/e2eseed" -fake-url "${fake_url}" -out "${E2E_SEED_FILE}" -blob-cache-dir "${artifacts}/seed-blobs" >"${artifacts}/seed.log" 2>&1
tail -n 1 "${artifacts}/seed.log"

E2E_BASE_URL="${app_url}" npm --prefix web run test:e2e
