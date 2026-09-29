#!/usr/bin/env bash
set -euo pipefail

# Runs the reader pages against the real Go application and Dex Server. Providers
# point at an unreachable loopback port: the journey never calls them.
npm --prefix web run build
artifacts="${DEX_TEST_ARTIFACT_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/dex-blog-newsletter-e2e.XXXXXX")}"
port="${E2E_PORT:-18080}"
unused="http://127.0.0.1:9"
mkdir -p "${artifacts}/connectors"
cat >"${artifacts}/connectors/connections.json" <<JSON
{"schemaVersion":"connectors.dex.dev/local-connections/v1alpha1","connections":[
 {"connectorId":"slack","modulePath":"github.com/superdurable/dex-connectors-library/connectors/slack","moduleVersion":"v0.11.0","provider":"slack","connectionName":"slack-workspace","configuration":{"endpoint":"${unused}"},"credentials":{"bot_token":"fake-bot-token","user_token":"fake-user-token","app_token":"fake-app-token"}},
 {"connectorId":"github","modulePath":"github.com/superdurable/dex-connectors-library/connectors/github","moduleVersion":"v0.8.0","provider":"github","connectionName":"github","configuration":{"baseUrl":"${unused}"},"credentials":{"access_token":"fake-github-token"}},
 {"connectorId":"llm","modulePath":"github.com/superdurable/dex-connectors-library/connectors/superdurable/llm","moduleVersion":"v0.1.0","provider":"llm","connectionName":"llm","configuration":{"model":"gemini/gemini-e2e"},"credentials":{"gemini_api_key":"AIza-e2e"}},
 {"connectorId":"gmail","modulePath":"github.com/superdurable/dex-connectors-library/connectors/google/gmail","moduleVersion":"v0.13.0","provider":"gmail","connectionName":"newsletter-sender","configuration":{"endpoint":"${unused}"},"credentials":{"access_token":"fake-gmail-token","primary_email":"news@example.com"}}
]}
JSON
chmod 600 "${artifacts}/connectors/connections.json"
export E2E_UNSUBSCRIBE_KEY_FILE="${artifacts}/unsubscribe.key"
cat >"${artifacts}/config.json" <<JSON
{"github":{"owners":["e2e"]},"dexWebUrl":"http://127.0.0.1:8842",
 "blog":{"artifactDirectory":"${artifacts}/blog"},
 "newsletter":{"publicBaseUrl":"http://127.0.0.1:${port}","unsubscribeKeyFile":"${E2E_UNSUBSCRIBE_KEY_FILE}"}}
JSON
app_log="${artifacts}/application.log"
DEX_CONNECTOR_CONFIG_FILE="${artifacts}/connectors/connections.json" BLOG_NEWSLETTER_CONFIG="${artifacts}/config.json" \
  SLACK_TRIGGER=off PORT="${port}" go run ./cmd/server >"${app_log}" 2>&1 &
app_pid=$!
cleanup() { exit_code=$?; trap - EXIT INT TERM; kill "${app_pid}" 2>/dev/null || true; wait "${app_pid}" 2>/dev/null || true; exit "${exit_code}"; }
trap cleanup EXIT INT TERM
deadline=$((SECONDS + 60))
until curl --fail --silent "http://127.0.0.1:${port}/api/health" >/dev/null && [[ -s "${E2E_UNSUBSCRIBE_KEY_FILE}" ]]; do
  if (( SECONDS >= deadline )) || ! kill -0 "${app_pid}" 2>/dev/null; then cat "${app_log}"; exit 1; fi
  sleep 0.2
done
E2E_BASE_URL="http://127.0.0.1:${port}" npm --prefix web run test:e2e
