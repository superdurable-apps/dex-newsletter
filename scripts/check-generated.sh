#!/usr/bin/env bash
set -euo pipefail

root_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
temporary_directory="$(mktemp -d "${TMPDIR:-/tmp}/dex-tech-blog-codegen.XXXXXX")"
trap 'rm -rf -- "${temporary_directory}"' EXIT
# tools/openapi is an isolated tool module; ignore any local go.work override.
GOWORK=off go -C "${root_directory}/tools/openapi" tool ogen --target "${temporary_directory}/go" --package generated ../../openapi/openapi.yaml
(cd "${root_directory}/web" && OPENAPI_OUTPUT="${temporary_directory}/web" npm run generate)
diff -ru "${root_directory}/internal/api/generated" "${temporary_directory}/go"
diff -ru "${root_directory}/web/src/api/generated" "${temporary_directory}/web"
