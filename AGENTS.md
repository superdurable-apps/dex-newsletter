# Dex Tech Blog Instructions

This repository is the Dex Tech Blog process product: a Slack-triggered Dex
Process that produces a tech blog post and newsletter, with review in Dex Web.
It is a Superverse `go-react-v1`
application adapted from the basic-process template (release `v1.6.1`). Read
`.superverse/template.json` and `openapi/openapi.yaml`, then load the installed
`dex-app-builder` skill through the current coding-agent host before changing
product behavior; it loads the sibling `dex-sdk` Core and Go guidance for
backend work. Superverse Coding Sandbox preinstalls a pinned Dex Skills
release; external developers install the released Dex plugin in their coding
agent. Never assume a fixed skill path. This repository must not vendor, clone,
or initialize a project-local copy.

## UI mode: No custom UI

The confirmed UI mode is **No custom UI**. Dex Web v2 is the only
process-management surface: Runs, Work Queue, search, details, edits, and
Actions all happen there. The web application is a non-business Hello World
page that shows the application name and a link to Dex Web.

Do not add approval, rejection, retry, escalation, status, display, list,
search, detail, Action-proxy, or Attribute-proxy routes, components, mock
lifecycle state, or tests. Newsletter requests start from Slack through a
dedicated Connector Trigger, not an application HTTP webhook. If a custom
process UI is ever required, stop and follow the dex-app-builder custom-UI
workflow (mock-first, explicit user approval) before implementing it.

## HTTP contract

`openapi/openapi.yaml` is the only HTTP contract source and defines exactly one
operation, `getApplicationInfo` (`GET /api/application-info`). Never edit files
below `internal/api/generated` or `web/src/api/generated` by hand. Change the
spec, run `make generate`, and update server, UI, and E2E coverage in the same
change. `internal/api` implements only that operation plus JSON 404/405
responses.

## Dex Flows

The Flows live in `internal/techblog/*_flow.go`; the runtime that registers
them and starts the Worker lives in `internal/runtime`. Every Flow is a Dex
Web v2 / FDG 2.0 definition with stable Step, Attribute, Channel, Timer, and
RPC identities. Keep external effects in `Execute`; `WaitFor` only declares
durable conditions and must not query or mutate providers or Dex state.
Register every durable primitive in the Flow persistence schema. Preserve
open-Flow compatibility unless the user explicitly requests a migration. Every
Step has exactly one group and explanation. `make check-fdg-v2` validates every
`internal/techblog/*_flow.go` file with rendering schema 2.0 and requires
`valid: true` with no diagnostics; never fall back to rendering schema v1.

Provider access goes through released Dex connectors. Credentials stay in the
connector connection store; Flows store only logical connection names. A local
connector override may use an uncommitted `go.work` (ignored by Git); never
commit a `go.work`, local `replace`, branch, or pseudo-version. The isolated
`tools/openapi` module always runs with `GOWORK=off`.

## Required environment

| Variable | Purpose |
| --- | --- |
| `DEXCLI` | Dex CLI binary used by `make check-fdg-v2`, `make test-integration`, `make test-e2e`, `make dev`, and `make dev-dex`. Defaults to `dexcli` on `PATH`. The scripts fail fast unless `$DEXCLI version` reports the release pinned in `DEX_CLI_BASELINE`. A project-local copy may live at `$HOME/.local/dexcli/v0.13.8/dexcli`. |
| `DEX_CONNECTOR_CONFIG_FILE` | Absolute path of the local connector connection store shown by Dex Web (default `~/.dex/connectors/connections.json`). It holds plaintext development credentials: never commit, log, or copy it into Flow state. |
| `TECH_BLOG_CONFIG_FILE` | Path of the non-secret process configuration JSON (application name, Dex Web URL, model, research, blog, newsletter, and review settings). `make dev` and `make dev-dex` run Dex Web on dexcli's default port `8802` to match the default `dexWebUrl`; keep them in sync if you override `DEX_DEV_WEB_PORT`. |

## Commands and verification

Stable commands are `make bootstrap`, `make generate`, `make check-generated`,
`make check-fdg-v2`, `make test-unit`, `make test-integration`,
`make test-e2e`, `make test-mock-e2e`, `make build`, `make dev`, `make mock`,
and `make check`.

`make mock` (`scripts/with-mock.sh`) starts the Go in-memory mock API and Vite
HMR without Dex on `MOCK_WEB_PORT` (default `8080`) and `MOCK_API_PORT`
(default `18081`). The mock is a contract test double: `cmd/mock-server` and
`internal/mockserver` implement the generated server interface, import only
`internal/api/generated` from this module, and never reach the Flows, the
runtime, or a provider. `/__mock__/control` accepts `reset` and `fail-next`
(the next subscribe answers 503); the page has no mock controls.
`make test-mock-e2e` runs `web/e2e/mock-newsletter-subscription.spec.ts`
against it on free ports. Mock verification does not replace the real Dex
integration and E2E tests.

`make dev` (`scripts/dev.sh`) is `make dev-dex` (Dex Server + Dex Web in the
foreground) plus `make dev-app` (build the page and server binary, run it
against `127.0.0.1:8801`); run those two separately to restart the application
without restarting Dex. Local Dex state (SQLite Runs, blobs, logs, generated
FDG 2.0 graphs, local connector release metadata) persists in gitignored
`.dex-dev/`; `rm -rf .dex-dev` resets it. Every Dex start regenerates the Flow
graphs for `--flow-rendering-dir` and, while `go.work` exists, passes
`--connector-release-override` for the local connectors. Tests use
`scripts/with-dex.sh`: free ports, temporary state and connector directory,
and `DEX_CONNECTOR_CONFIG_FILE` / `TECH_BLOG_CONFIG_FILE` unset; never point
tests at `.dex-dev/` or a real connection store.

`scripts/local-connections/` holds developer-only Python 3 helpers (standard
library only) that write a Google access token into the local connection store
(`set-google-connection.py`) or create the subscriber spreadsheet
(`create-subscriber-sheet.py`). Never run them from tests or automation against
a real store, and never let them print a credential. `make test-unit` also runs
their offline unittest, which uses temporary files and checks that the Google
connector module versions in `local_connections.py` match `go.mod`; bump them
together.

After each edit batch, run the narrowest relevant Make target. Before calling
`commit_and_push`, run `make check` successfully and include it in verification.
If `make check` fails or cannot run, report `blocked=true`. Do not weaken, skip,
or delete a failing check.

`DEX_SERVER_BASELINE` and `DEX_CLI_BASELINE` pin Dex Server `server/v0.13.2`
and Dex CLI `cli-v0.13.8`; `go.mod` pins the Dex Go SDK `v0.13.1`. These are
the pins of basic-process template `v1.6.1`. Advance them only together with
the template release and the contract test in `internal/templatecontract`. The
template's release machinery (`scripts/check-template-version.py`, the release
CI job) and its scheduled `update-dex-dependencies` workflow are not ported:
this repository is an application, not a template release, and its dependency
updates are reviewed by hand.

`make bootstrap`, `npm ci`, and `go mod download` may restore dependencies
already declared by the committed manifests and lockfiles. Before adding or
upgrading a project dependency, verify that the standard library and existing
dependencies cannot satisfy explicit requested behavior. Pin the selected
version, update the manifest and lockfile together, explain why it is needed,
and run `make check`. Do not add convenience-only dependencies, perform
unrelated upgrades or audit auto-fixes such as `npm audit fix`, install global
or operating-system packages, or run remote installation scripts.

When structure, commands, or required tooling changes, update this file,
`.superverse/template.json`, `README.md`, and contract tests together. Do not
maintain a separate static repository map.
