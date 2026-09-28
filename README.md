# dex-newsletter

A process that writes a newsletter and sends it to its subscribers. The
product, **Dex Tech Blog**, is a Slack-triggered Dex Process that produces a
tech blog post and newsletter. Newsletter requests start from Slack, are
reviewed in Dex Web, and are mailed to the readers who subscribed on the
application's home page. Subscribers are stored in Dex. This is a Superverse
`go-react-v1` application built on the Dex basic-process template `v1.6.1`.

## Not yet built

Readers can subscribe on the home page, but they can't leave the list yet:

- **Unsubscribe:** no newsletter carries an unsubscribe link or a
  `List-Unsubscribe` header, and there is no way to remove an address. The
  released Gmail connector cannot set custom message headers yet.
- **Confirmation:** a subscription takes effect immediately, with no
  confirmation email (double opt-in) and no rate limit, so anyone who can reach
  the page can add any address, and one client can fill the list to
  `newsletter.maxRecipients`. New readers then get 409, and every approved issue
  goes to the addresses that were added. With no removal path yet, recovering
  means raising `newsletter.maxRecipients` (at most 2000) or deleting the Dex
  state, which deletes every Run too. The application server listens on every
  network interface (`:$PORT`), so keep the machine on a trusted network, or
  block the port, until confirmation exists.

Tracked in [#2](https://github.com/superdurable-apps/dex-newsletter/issues/2).

## UI mode: Custom UI (subscription form only)

Dex Web v2 is still the complete process-management surface: Runs, Work Queue,
search, details, edits, and Actions. The one Dex Web v2 capability gap is a
participant portal: newsletter readers are not Dex Web operators, and Dex Web
runs behind the operators' trusted authentication, so readers need their own
page to subscribe. The application adds exactly one reader-facing control:

- the home page shows the application name, an **Open Dex Web** link, and a
  newsletter subscription form with one **Email** field and a **Subscribe**
  button;
- the Go HTTP server serves the page and the API;
- two OpenAPI operations with their locally generated Go server and TypeScript
  client:
  `getApplicationInfo` (`GET /api/application-info`) and
  `subscribeToNewsletter` (`POST /api/newsletter/subscriptions`).

`subscribeToNewsletter` answers 200 with the canonical address whether it was
new or already on the list; 400 for an address that is not a single
deliverable address; 409 for every valid address, subscribed or not, once the
list holds `newsletter.maxRecipients` addresses; and 503 when Dex cannot be
reached. No status reveals who is subscribed. There are no
approval, status, list, detail, or retry routes or controls. Component tests
mock the generated client for states that are hard to trigger; `make test-e2e`
runs the real journey against Dex. Slack ingress is a Dex Connector Trigger,
not an application webhook.

## Process

One Slack message in the configured channel ("Write a blog post about connectors
over the past two weeks") starts one `TechBlogNewsletterFlow` run. The Flow ID is
`tech-blog-newsletter-<team>-<channel>-<message-ts>`, so Slack redelivery attaches
to the same run.

1. **Intake** — acknowledge in the Slack thread; the language model reads the topic,
   instructions, time range (default 7 days, max 90, measured back from the Slack
   message time), and which catalog repositories and paths to research. An unclear
   request gets a clarifying question and closes.
2. **Research** — one `RepositoryChangeResearchFlow` SubFlow per selected repository
   (up to 5, in parallel): merged pull requests in the window, changed files for the
   newest ones, and commits, then a per-repository digest. One failed repository does
   not fail the request.
3. **Synthesis and writing** — a research brief (only meaningful capabilities,
   features, and improvements, citing real PR/commit URLs), a structured blog post,
   a self-contained HTML artifact at `<blog.artifactDirectory>/<flow-id>/<slug>.html`
   (inline CSS, no external resources, strict CSP), and a newsletter email that
   carries the full post inline plus a link to `<blog.publicBaseUrl>/<slug>.html`
   when configured.
4. **Editorial review** — the run waits in the Dex Web Work Queue. A reminder is
   posted to the Slack thread after `review.reminderInterval`; the review expires
   after `review.maxReminders` more intervals.
5. **Delivery** — the run snapshots the [subscriber list](#subscribers)
   (re-validated, de-duplicated, capped at `newsletter.maxRecipients`) and sends each
   subscriber their own Gmail message. Each send persists synchronously, so it is
   repeated only if the Worker is lost mid-send (Gmail has no idempotency key). A rejected address is recorded; an unconfirmed send is recorded as
   `uncertain` and is never resent automatically. The run's Dex Web detail view lists
   every rejected, unconfirmed, or failed recipient under **Recipients to check before
   resending**, and the thread gets a delivery report with counts only.

Every language-model call goes through `LanguageModelGenerationFlow`, a
provider-neutral SubFlow. Gemini is the only registered provider; adding one means
adding its Connector Step and completion Step there and listing it in
`config.SupportedLanguageModelProviders` — the calling Flows do not change.

Any stage whose provider or model output is unusable moves the run to
`needs-attention` and posts to the thread; an operator retries the stage or
abandons the request from Dex Web. Abandoning a run whose delivery had already
started (or leaving it unrecovered for 7 days) closes it as `delivery-stopped`
with the delivery counts and tells the thread not to post the request again,
because a new request would email every subscriber a second time.

### Subscribers

`NewsletterSubscriberListFlow` owns the subscriber list. It is one long-lived
Flow with the fixed ID `newsletter-subscriber-list` and no Steps; the
application starts it once Dex answers at Worker startup. Its typed RPCs are
the only way in:

- `AddNewsletterSubscriber`, called by `subscribeToNewsletter`, trims the
  address, applies the address policy (a single bare ASCII addr-spec with a
  dotted domain), lowercases it, and appends it unless the list already holds
  `newsletter.maxRecipients` addresses or the address. It locks the list, so
  concurrent subscriptions serialize.
- `ListNewsletterSubscribers` returns a snapshot; `LoadNewsletterSubscribers`
  in each `TechBlogNewsletterFlow` run calls it from `Execute` after approval.
  It takes the same lock, which needs an open list. A failed read is retried
  for about ten minutes, then the run holds for attention at
  `load-subscribers`.

The application starts the list with a constant request ID, so every restart
finds the existing list and the Slack Trigger starts either way.

Open the `newsletter-subscriber-list` run in Dex Web to see the count and the
addresses. Don't stop it: a stopped list rejects subscriptions (503) and holds
deliveries, and the default Flow ID reuse policy keeps the application from
replacing it with an empty list. The list lives in the Dex state directory, so
`rm -rf .dex-dev` deletes every subscriber.

Storage decision (Dex Skills 0.25.7 makes Dex state the default store):

| Fact | Owner | Access | Dex primitive | External store |
| --- | --- | --- | --- | --- |
| Subscriber addresses (canonical, unique, in subscription order) | `NewsletterSubscriberListFlow` | one locked write per subscription; one whole-list read per approved issue | one Attribute, `newsletter-subscribers`, of at most `newsletter.maxRecipients` (≤ 2000) entries, plus `newsletter-subscriber-count` for Dex Web | none: the list is bounded and always read whole, with no search, joins, or analytics |
| One issue's audience and delivery outcomes | its `TechBlogNewsletterFlow` run | snapshot at approval; one update per send | Attributes `subscriber-list` and `delivery-summary`; AttributeMap `delivery-exceptions`, one instance per unconfirmed or rejected recipient | none |

Upgrading from the Google Sheet version is a one-way migration. The Sheet's
subscribers are not imported (the list starts empty), and the Step types
`PrepareNewsletterDelivery`, `RecordSubscriberSheetFailure`, and
`HoldSubscriberSheetAfterRetries` no longer exist, so a run that was open
between approval and its first send when the old build stopped cannot resume.
Finish or abandon such runs before upgrading, and delete the removed Sheet
fields from `TECH_BLOG_CONFIG_FILE`.

### Dex Web Actions and permissions

| Action | Available when | Permission |
| --- | --- | --- |
| Approve and send newsletter | `awaiting-editor-review` | `newsletter.approve` |
| Request revision (feedback, max `review.maxRevisions`) | `awaiting-editor-review` | `newsletter.revise` |
| Discard draft | `awaiting-editor-review` | `newsletter.discard` |
| Retry failed stage | `needs-attention` | `newsletter.recover` |
| Abandon request | `needs-attention` | `newsletter.recover` |

Actions carry the current review or attention gate key, so an Action from a stale
page is rejected. The newsletter subject is editable in the run's detail view.
Production Work Queue authorization must come from a trusted-header boundary; the
Dex Web local permission selector is development-only.

### Connections

Configure these in Dex Web **Connections** (loopback `dexcli dev`), then restart the
application so it reloads the connection file. [Setting up providers](#setting-up-providers)
covers the account-side setup for each one. Every capability below was matched by
exact kind and name against the published
[connector catalog](https://superdurable.github.io/dex-connectors-library/catalog.yaml)
and the release-tagged manifest:

| Connector | Release tag ([manifest](https://github.com/superdurable/dex-connectors-library/tree/main/connectors)) | Connection name | Capabilities | Used for |
| --- | --- | --- | --- | --- |
| Slack | [`connectors/slack/v0.10.0`](https://github.com/superdurable/dex-connectors-library/blob/connectors/slack/v0.10.0/connectors/slack/connector.yaml) | `slack-workspace` | Trigger `channelThreadCreated`; Mutation `postThreadReply` | `tech-blog-newsletter-request` Trigger binding (channel, optional text match, allowed members) and thread replies |
| GitHub | [`connectors/github/v0.7.0`](https://github.com/superdurable/dex-connectors-library/blob/connectors/github/v0.7.0/connectors/github/connector.yaml) | `github-account` | Queries `listMergedPullRequests`, `listPullRequestFiles`, `listCommits` | merged PRs, PR files, commits (OAuth grant must be exactly `read:user user:email`) |
| Gemini | [`connectors/google/gemini/v0.1.0`](https://github.com/superdurable/dex-connectors-library/blob/connectors/google/gemini/v0.1.0/connectors/google/gemini/connector.yaml) | `gemini-api` | Query `generateContent` | every language-model stage (API key; the Connection's model applies unless a stage names one) |
| Gmail | [`connectors/google/gmail/v0.12.0`](https://github.com/superdurable/dex-connectors-library/blob/connectors/google/gmail/v0.12.0/connectors/google/gmail/connector.yaml) | `newsletter-sender` | Mutation `sendMessage` | one message per subscriber from the authorized account |

Missing connections are logged at startup and the run holds for attention at the
stage that needs them. Subscribers need no connection: they live in Dex.

### Process configuration

`config/tech-blog.example.json` is the complete default configuration (repository
catalog, lookback limits, per-stage token limits, blog voice, artifact directory,
public blog URL, subscriber and recipient cap, footer, review timing). Copy it, edit it, and set
`TECH_BLOG_CONFIG_FILE`. Fields you omit keep their defaults; unknown fields are
rejected. A `research.repositories` list you set replaces the default catalog;
give every field of each entry, because an omitted field keeps the value of the
default entry at the same position. Every stage leaves `model` blank, so it uses the
model chosen on the `gemini-api` Connection in Dex Web; set `model` on a stage to
override it there. No stage sets `temperature` ([why](#gemini-gemini-api)). A file
written for the old Google Sheet (`newsletter.subscriberSheet`,
`emailColumnHeader`, `statusColumnHeader`, `unsubscribedStatusValues`) is
rejected with a message naming those fields; delete them.

## Requirements

- Go, Node.js/npm, and Python 3.
- For `make test-e2e` (and so `make check`), Playwright's Chromium, installed
  once by hand with `(cd web && npx playwright install chromium)`.
  `make bootstrap` installs the npm packages but no browser; CI installs
  Chromium itself.
- Dex CLI `v0.14.0` (`DEX_CLI_BASELINE`), which embeds Dex Web v2. The Homebrew
  `dexcli` may be older; point `DEXCLI` at the pinned binary.
- Dex Server `server/v0.14.0` (`DEX_SERVER_BASELINE`) and Dex Go SDK `v0.13.1`
  (`go.mod`), the pins of basic-process template `v1.7.1`.

| Variable | Purpose |
| --- | --- |
| `DEXCLI` | Dex CLI binary (default `dexcli` on `PATH`). Scripts fail fast unless `$DEXCLI version` matches `DEX_CLI_BASELINE`. |
| `DEX_CONNECTOR_CONFIG_FILE` | Absolute path of the local connector connection store shown by Dex Web (default `~/.dex/connectors/connections.json`). Contains plaintext development credentials; never commit it. |
| `TECH_BLOG_CONFIG_FILE` | Path of the non-secret process configuration JSON. |

## Start locally

```bash
export DEXCLI="$HOME/.local/dexcli/v0.14.0/dexcli"
export TECH_BLOG_CONFIG_FILE=/absolute/path/to/tech-blog.json   # optional
make bootstrap

make dev-dex   # terminal 1: Dex Server + Dex Web; leave it running
make dev-app   # terminal 2: page, HTTP server, and Dex Worker; restart freely
```

| Command | Runs |
| --- | --- |
| `make dev-dex` | Dex Server and Dex Web in the foreground (`dexcli dev`). |
| `make dev-app` | Builds the page and the server binary, then runs the server and Dex Worker against `DEX_FLOW_SERVICE_ADDRESS=127.0.0.1:8801`. |
| `make dev` | Both in one terminal. Stopping it stops Dex too. |

Open <http://127.0.0.1:8080> for the home page and its subscription form, and manage Runs, the Work
Queue, and Actions in Dex Web at <http://127.0.0.1:8802>. Use the two-terminal
form whenever you will restart the application (after configuring connections,
changing Go code, or editing `TECH_BLOG_CONFIG_FILE`): only `make dev-app`
restarts, and Dex Web and open Runs keep going.

Every Dex start (`make dev-dex` or `make dev`) does two things before it launches
`dexcli dev`:

- It renders every `internal/techblog/*_flow.go` file to an FDG 2.0 graph in
  `.dex-dev/flow-definitions/` and passes that directory as
  `--flow-rendering-dir`. Dex Web builds Connections, the Work Queue, and Actions
  from these graphs, so restart Dex after you change a Flow's Steps, Attributes,
  or Actions. If a Flow does not type-check, the partial graph is still loaded
  and the errors are printed; Dex Web keeps the Work Queue and Actions disabled
  until you fix it.
- While a gitignored `go.work` exists (for example, while you develop a
  connector change), it builds release metadata for each local connector
  checkout that `go.work` lists with
  that checkout's `go run ./cmd/connectorctl release-artifact`, writes it to
  `.dex-dev/connector-artifacts/<connector>/`, and passes
  `--connector-release-override <connector>=<directory>`. Dex Web then shows
  those connections as **Local override** instead of **Unsupported**. A failed
  build is reported and skipped; `DEX_DEV_CONNECTOR_RELEASE_OVERRIDE=0` turns
  it off.

### Persistent state

`.dex-dev/` (gitignored) holds the Dex SQLite database (`dex.sqlite.db`, which
keeps every Run), the Dex blob store, Dex server logs, the application blob
cache (`DEX_BLOB_CACHE_DIR`), the generated graphs and connector metadata, and
the built server binary. Restarting the application or Dex keeps all Runs. To
start over, stop everything and run `rm -rf .dex-dev`. Connections are stored
outside `.dex-dev/`, so a reset keeps them.

### Configure connections

1. Start Dex (`make dev-dex`) and open Dex Web **Connections**. Configure each
   connection listed in [Connections](#connections).
2. Dex Web shows the connections file it writes (by default
   `~/.dex/connectors/connections.json`; set `DEX_DEV_CONNECTOR_CONFIG_DIR` to use
   another directory). It holds plaintext credentials; never commit it. Export
   that path in the application's terminal:

   ```bash
   export DEX_CONNECTOR_CONFIG_FILE="$HOME/.dex/connectors/connections.json"
   ```

3. Restart the application: stop `make dev-app` and run it again. The
   application reads connections and the Slack Trigger binding only at
   startup.
   Dex does not need a restart. Without `DEX_CONNECTOR_CONFIG_FILE`,
   `make dev-app` warns and every connection stays unconfigured.

### Ports and overrides

| Variable | Default | Purpose |
| --- | --- | --- |
| `DEX_DEV_PORT` | `8801` | Dex FlowService port; the application connects to `127.0.0.1:$DEX_DEV_PORT`. |
| `DEX_DEV_WEB_PORT` | `8802` | Dex Web port. If you change it, set `dexWebUrl` in `TECH_BLOG_CONFIG_FILE` to match so the **Open Dex Web** link works. |
| `PORT` | `8080` | Application page and API. |
| `DEX_WORKER_BIND_ADDRESS`, `DEX_WORKER_TARGET` | `127.0.0.1:8811` | Dex Worker address. Keep it stable across restarts so open Runs return to the same Worker. |
| `DEX_DEV_CONNECTOR_CONFIG_DIR` | `~/.dex/connectors` | Connector configuration directory passed to `dexcli dev`. |
| `DEX_DEV_STATE_DIR` | `.dex-dev` | Persistent local Dex state. |
| `DEX_DEV_OPEN` | `false` | Set to `true` to open Dex Web in a browser on start. |
| `LOG_LEVEL` | `info` | Application log level: `debug`, `info`, `warn`, or `error`. `debug` logs why the Slack Trigger ignored each message. |

If the Dex ports are already in use, `make dev-dex` and `make dev` stop with a
message. If `make dev-dex` is already running, use `make dev-app`. Tests always
use free ports and temporary state.

If another process already listens on the Worker port `127.0.0.1:8811` (for
example a second `dexcli dev`), `make dev-app` cannot start the Worker. Pick a
free port and set both variables to it, and keep the same value on every
restart so open Runs return to the same Worker:

```bash
DEX_WORKER_BIND_ADDRESS=127.0.0.1:8821 DEX_WORKER_TARGET=127.0.0.1:8821 make dev-app
```

## Setting up providers

These notes come from the first end-to-end run against the real providers.
The GitHub, Slack, and Google OAuth clients redirect to Dex Web's callback,
`http://127.0.0.1:8802/api/v2/connector-oauth/callback` (use your port if you
set `DEX_DEV_WEB_PORT`). Dex Web asks for the OAuth client ID and secret on
every connect and reconnect: it keeps them in memory for the ten-minute OAuth
session and never writes them to the connections file. The application
re-reads credentials before every provider call, so a reconnected connection
works on the next **Retry failed stage**. A connection that did not exist when
the application started, a changed Trigger binding, and a changed Step
configuration need a `make dev-app` restart.

### Gemini (`gemini-api`)

- Create an API key in Google AI Studio and restrict it to the Gemini API
  (Generative Language API).
- The key's project needs prepaid credits. Without them every call fails with
  HTTP 402 and the run moves to `needs-attention`; add credits, then retry.
- Gemini 2.x models are closed to new projects. Choose the model on the
  `gemini-api` Connection; when it is blank the connector uses
  `gemini-3.5-flash-lite`. `temperature` is left unset so Gemini 3 uses its own
  default. Override `model`, `temperature`, `thinkingBudget`, or
  `maxOutputTokens` per stage in `TECH_BLOG_CONFIG_FILE`.
- Gemini rejects a response schema that carries many length, count, or range
  bounds with HTTP 400 `INVALID_ARGUMENT`. The application strips `minLength`,
  `maxLength`, `minItems`, `maxItems`, `minimum`, and `maximum` from every schema
  it sends to Gemini and enforces them itself when it parses the reply.

### GitHub (`github-account`)

- Register a dedicated GitHub OAuth App with the authorization callback URL
  `http://127.0.0.1:8802/api/v2/connector-oauth/callback` and enter its client
  ID and secret in Dex Web.
- The connector requires a grant of exactly `read:user user:email` and rejects
  any other scope set as `insufficientScope`, so a broader token, such as the
  one the `gh` CLI uses, is rejected. With these scopes it reads only public
  repositories.

### Slack (`slack-workspace`)

- Create a Socket Mode app with the bot scopes, user scopes, bot events, and
  `xapp-` app-level token (`connections:write`) listed in the
  [Slack connector setup](https://github.com/superdurable/dex-connectors-library/blob/connectors/slack/v0.10.0/connectors/slack/README.md#slack-app-setup),
  and install it.
- Under **OAuth & Permissions** > **Redirect URLs**, add
  `http://127.0.0.1:8802/api/v2/connector-oauth/callback` exactly (your port
  if you set `DEX_DEV_WEB_PORT`; keep `http` and `127.0.0.1`) and save.
- Dex Web connects Slack with OAuth. Enter the app's client ID and client
  secret (**Basic Information** > **App Credentials**) and the `xapp-` token,
  then authorize. The bot and user tokens come from the OAuth grant; you do
  not paste them. The connector's
  [thread-approval example](https://github.com/superdurable/dex-connectors-library/blob/connectors/slack/v0.10.0/connectors/slack/examples/thread-approval/README.md)
  walks through the same Slack app setup step by step.
- Invite the bot to the request channel (`/invite @your-app`). It only sees
  and replies in channels it belongs to.
- Configure the `tech-blog-newsletter-request` Trigger binding: the channel,
  an optional text the message must contain (case-insensitive substring), and
  optional allowed members. A message that fails any of these filters is
  dropped silently: no run starts and nothing is posted. Run
  `LOG_LEVEL=debug make dev-app` to see a DEBUG `trigger event ignored` record
  with the `reason`, for example `matcher_mismatch` (text or member filter),
  `channel_mismatch`, `not_a_root` (a thread reply), `bot` (bot messages,
  including the application's own replies), `subtype` (edited messages, file
  shares, thread broadcasts, and other message subtypes; the record names the
  `subtype`), or `missing_user`. A message the connector passes but the
  application's filter rejects, such as one with empty text, is logged at INFO
  as `trigger event skipped: filtered`. Restart the application after changing
  the binding.

### Gmail (`newsletter-sender`)

- In Google Cloud, enable the Gmail API and create an OAuth client of type
  **Web application** with the authorized redirect URI
  `http://127.0.0.1:8802/api/v2/connector-oauth/callback`. Enter its client ID
  and secret in Dex Web for the Gmail connection.
- On the OAuth consent screen, add the sender account as a test user while the
  app is External and in Testing, or choose the Internal audience in a Google
  Workspace organization.
- When Google asks for consent, tick every checkbox. An unticked scope is not
  granted and the connection fails its scope check.
- Dex Web stores only the short-lived Google access token, not a refresh
  token, so a local Google connection works for about an hour. Mail is sent
  only after approval: approve within the hour, or reconnect
  `newsletter-sender` (each reconnect asks for the OAuth client ID and secret
  again, because Dex Web does not store them) and choose **Retry failed
  stage**.
- Gmail `v0.12.0` requests the canonical
  `https://www.googleapis.com/auth/userinfo.email` scope, so Dex Web saves the
  grant. Versions `v0.11.1` and earlier requested the `email` alias and failed
  with `CONNECTOR_OAUTH_SCOPE_INSUFFICIENT`; a connection saved under them must
  be reconnected once. If Dex Web still refuses a grant, store a token obtained
  in the [Google OAuth 2.0 Playground](https://developers.google.com/oauthplayground)
  instead:
  1. Add `https://developers.google.com/oauthplayground` as a second authorized
     redirect URI of the Web client.
  2. In the Playground settings, choose **Use your own OAuth credentials** and
     enter the client ID and secret.
  3. Authorize `openid https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/gmail.readonly https://www.googleapis.com/auth/gmail.send`
     as the sender account, exchange the code for tokens, and copy the access
     token.
  4. Run `scripts/local-connections/set-google-connection.py --connector gmail`,
     paste the token at the hidden prompt, then enter the sender account's
     email address (or pass `--primary-email`).

### Quick-test profile

A first run is faster and cheaper with a small profile. Save it as, for
example, `$HOME/tech-blog.quick.json`, export
`TECH_BLOG_CONFIG_FILE=$HOME/tech-blog.quick.json`, and restart
`make dev-app`. Omitted fields keep their defaults.

```json
{
  "research": {
    "defaultLookbackDays": 7,
    "maxLookbackDays": 7,
    "maxRepositoriesPerRequest": 1,
    "maxMergedPullRequestsPerRepository": 5,
    "maxPullRequestsWithFileDetails": 2,
    "maxFilesPerPullRequest": 8,
    "maxCommitsPerRepository": 10,
    "maxPatchCharactersPerFile": 500,
    "maxEvidenceCharactersPerRepository": 15000,
    "repositories": [
      {
        "owner": "superdurable",
        "name": "dex-connectors-library",
        "description": "Official Dex connectors (Slack, Gmail, Google Sheets, GitHub, and language-model providers such as Gemini, OpenAI, and Claude), the connector SDK, manifests, and code generation.",
        "topics": ["connectors", "integrations", "triggers", "connector sdk"],
        "pathHints": ["connectors/", "sdkgo/"]
      }
    ]
  },
  "blog": {
    "styleGuide": "Quick test post: about 400 words, 2 or 3 short sections, concrete and technical, at most one short code snippet, no marketing language."
  }
}
```

### Local connection helper scripts

`scripts/local-connections/` holds developer-only helpers (Python 3 standard
library only). They use the connections file at `$DEX_CONNECTOR_CONFIG_FILE`
(default `~/.dex/connectors/connections.json`; `--connections-file` overrides
it) and never print a token.

| Script | Does |
| --- | --- |
| `set-google-connection.py --connector gmail` | Stores a Google access token you obtained yourself as `newsletter-sender`. It reads the token at a hidden prompt, or with `--from-clipboard` from the macOS clipboard, which it then clears. For Gmail it also asks for the sender's email address (`--primary-email` skips the prompt and is required with `--from-clipboard`). It backs up the file to a new `connections.json.bak-<timestamp>` that is mode `0600` from creation, replaces only that connection, writes the file atomically with mode `0600`, and sets `credentialExpiresAt` 55 minutes ahead. |

Backups hold the same plaintext credentials; delete them when you are done.
`make test-unit` runs the helpers' offline tests.

## Contract and generated code

`openapi/openapi.yaml` is authoritative. `make generate`
(`scripts/generate-openapi.sh`) runs Ogen for the Go server contract in
`internal/api/generated` and Hey API for the TypeScript client in
`web/src/api/generated`, writing both to a temporary directory first so a
failed run leaves the previous outputs intact. Both directories are ignored
local build outputs: they are never committed, and every build, test, and dev
target regenerates them. Never edit them by hand.

```bash
make generate
make check-fdg-v2
```

## Verification

```bash
make test-unit
make test-integration
make test-e2e
make build
make check
```

`make test-unit` runs the Go tests, the web tests, and the offline Python tests
of `scripts/local-connections/`, which use temporary files only.

Integration and E2E tests run under `scripts/with-dex.sh`, which starts a
throwaway `dexcli dev` on free ports with temporary state and an empty
temporary connector directory. It unsets `DEX_CONNECTOR_CONFIG_FILE` and
`TECH_BLOG_CONFIG_FILE` for Dex and the wrapped command, so tests never read
`~/.dex/connectors` or your process configuration, never touch `.dex-dev/`, and
never open a Slack Socket Mode connection. The temporary directory is deleted
on success and kept (with its path printed) on failure.

`make test-e2e` builds the server binary into that directory, runs it, and on
exit stops that process and waits for it, so no server is left running. The
Playwright suite loads the production page, asserts that the application name
and the subscription form (one Email field, one Subscribe button) render,
subscribes a fresh address through the real Dex subscriber list, and checks
that the removed `/api/flows` management routes return 404. There is no
application mock server: component tests mock the generated client, and mock
evidence never replaces the real Dex and application journey.

`make check-fdg-v2` validates every `internal/techblog/*_flow.go` file with
rendering schema 2.0 and requires a diagnostic-free graph with `valid: true`.
The only tolerated diagnostic is `connector_release_required`, and only while the
local `go.work` connector override exists; it is printed as a release blocker.
Schema v1 is not an accepted fallback.

`make check` is the required completion gate for coding agents and CI.

## Dex skills

Develop this application with the released
[Dex plugin](https://github.com/superdurable/dex-skills#install) installed in
the coding-agent host. Invoke `dex-app-builder` as the product workflow
entrypoint; it loads the matching `dex-sdk` guidance. Superverse Coding Sandbox
preinstalls a pinned release, while external developers install the plugin in
Codex, Claude Code, Cursor, or another Agent Skills client. The repository
never assumes a fixed skill path and contains no project-local skill copy.
