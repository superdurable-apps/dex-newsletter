# Dex Tech Blog Instructions

This repository is the Dex Tech Blog process product: a Slack-triggered Dex
Process that produces a tech blog post and newsletter, with review in Dex Web,
mailed to readers who subscribed on the application's home page.
It is a Superverse `go-react-v1`
application adapted from the basic-process template (release `v1.7.1`). Read
`.superverse/template.json` and `openapi/openapi.yaml`, then load the installed
`dex-app-builder` skill through the current coding-agent host before changing
product behavior; it loads the sibling `dex-sdk` Core and Go guidance for
backend work. Superverse Coding Sandbox preinstalls a pinned Dex Skills
release; external developers install the released Dex plugin in their coding
agent. Never assume a fixed skill path. This repository must not vendor, clone,
or initialize a project-local copy.

## UI mode: Custom UI (the newsletter page only)

The confirmed UI mode is **Custom UI**. Its recorded Dex Web v2 capability gap
is a participant portal: newsletter readers are not Dex Web operators, so they
need their own page. It is limited to one reader-facing control:
the home page shows the application name, a link to Dex Web, and a newsletter
subscription form with exactly one Email input and one Subscribe button. Opened
from an email's unsubscribe link (`?unsubscribe=<token>`), the same page
unsubscribes immediately (the user chose immediate over a confirm button) and
keeps the form. The user confirmed that page, field, and those actions; keep it
intentionally minimal (no other fields, preferences, or subscription
management) unless the user asks for more and confirms a new static wireframe
first. Dex Web v2 remains the only
process-management surface: Runs, Work Queue, search, details, edits, and
Actions all happen there.

Do not add approval, rejection, retry, escalation, status, display, list,
search, detail, Action-proxy, or Attribute-proxy routes or components.
Newsletter requests start from Slack through a dedicated Connector Trigger, not
an application HTTP webhook. The page reuses the template's `.panel`, `label`,
`input`, and `button` styles; add no images, icons, animation, or branding.

## Naming

Use precise domain names. Do not use the case-insensitive stems `runtime` or
`normaliz` in repository-owned package, directory, file, type, interface,
method, function, field, parameter, variable, constant, schema, configuration,
or resource names. Name the concrete execution role or transformation instead,
such as `TrimWhitespace`, `CanonicalizeURL`, or
`ValidateAndSortSelections`. Generated and third-party code,
framework-mandated identifiers, and migration code or tests that must reference
immutable legacy names are exempt. Known deviations, to be renamed in a
separate change: the `internal/runtime` package and the
`internal/techblog/render/normalize*.go` files and their `Normalize*` and
`normalize*` identifiers. New code follows the rule.

## HTTP contract

`openapi/openapi.yaml` is the only HTTP contract source and defines exactly three
operations: `getApplicationInfo` (`GET /api/application-info`),
`subscribeToNewsletter` (`POST /api/newsletter/subscriptions`), and
`unsubscribeFromNewsletter` (`POST /api/newsletter/unsubscriptions`). Never edit
files below `internal/api/generated` or `web/src/api/generated` by hand.
Change the spec, run `make generate`, and update server, UI, and
E2E coverage in the same change. `internal/api` implements those operations
plus JSON 404/405 responses; the browser calls only the generated client and
never Dex. `subscribeToNewsletter` answers the same 200 for a new and an
existing address, and 409 for every valid address once the list is full, so
no response reveals list membership; `unsubscribeFromNewsletter` answers the
same 200 whether or not a token named a subscriber.

## Dex Flows

The Flows live in `internal/techblog/*_flow.go`; the runtime that registers
them, starts the Worker, and starts the subscriber list lives in
`internal/runtime`. `NewsletterSubscriberListFlow` (fixed ID
`newsletter-subscriber-list`, no Steps) is the only owner of subscriber
addresses: one bounded Attribute written by the locked
`AddNewsletterSubscriber` and `RemoveNewsletterSubscriber` RPCs and read by
`TechBlogNewsletterFlow`'s
`LoadNewsletterSubscribers` Step through `ListNewsletterSubscribers`. Keep
subscriber data in Dex; do not add a database, cache, or spreadsheet copy
without a storage decision the user confirms. Every Flow is a Dex
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
| `DEXCLI` | Dex CLI binary used by `make check-fdg-v2`, `make test-integration`, `make test-e2e`, `make dev`, and `make dev-dex`. Defaults to `dexcli` on `PATH`. The scripts fail fast unless `$DEXCLI version` reports the release pinned in `DEX_CLI_BASELINE`. A project-local copy may live at `$HOME/.local/dexcli/v0.14.0/dexcli`. |
| `DEX_CONNECTOR_CONFIG_FILE` | Absolute path of the local connector connection store shown by Dex Web (default `~/.dex/connectors/connections.json`). It holds plaintext development credentials: never commit, log, or copy it into Flow state. |
| `TECH_BLOG_UNSUBSCRIBE_KEY_FILE` | Required secret: the base64 key (at least 32 bytes) that signs unsubscribe links. `scripts/dev.sh` creates `.dex-dev/unsubscribe.key` when unset and `scripts/run-e2e.sh` a throwaway one; never commit a key, and never copy it into Flow state. |
| `TECH_BLOG_CONFIG_FILE` | Path of the non-secret process configuration JSON (application name, Dex Web URL, model, research, blog, newsletter, and review settings). `make dev` and `make dev-dex` run Dex Web on dexcli's default port `8802` to match the default `dexWebUrl`; keep them in sync if you override `DEX_DEV_WEB_PORT`. |

## Commands and verification

Stable commands are `make bootstrap`, `make generate`, `make check-fdg-v2`,
`make test-unit`, `make test-integration`, `make test-e2e`, `make build`,
`make dev`, and `make check`. Build, test, and dev targets regenerate the
OpenAPI outputs first; `make check` generates once. `internal/api/generated`
and `web/src/api/generated` are ignored local build outputs: never stage or
commit them. Do not restore `make check-generated`, `make mock`, or
`make test-mock-e2e`, and do not add an application mock server, product mock
routes, or Mock Controls. Use component-level mocks of the generated client
for hard-to-trigger UI states and, only for a browser-only edge case,
test-local Playwright request interception; that evidence never replaces the
real Dex integration and E2E tests.

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
(`set-google-connection.py`). Never run them from tests or automation against
a real store, and never let them print a credential. `make test-unit` also runs
their offline unittest, which uses temporary files and checks that the Google
connector module version in `local_connections.py` matches `go.mod`; bump them
together.

After each edit batch, run the narrowest relevant Make target. Before calling
`commit_and_push`, run `make check` successfully and include it in verification.
If `make check` fails or cannot run, report `blocked=true`. Do not weaken, skip,
or delete a failing check.

`DEX_SERVER_BASELINE` and `DEX_CLI_BASELINE` pin Dex Server `server/v0.14.0`
and Dex CLI `cli-v0.14.0`; `go.mod` pins the Dex Go SDK `v0.13.1`. These are
the pins of basic-process template `v1.7.1`. Advance them only together with
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
