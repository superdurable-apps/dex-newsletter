# Dex Blog Newsletter

A Dex Process that turns recent product development into a tech blog post and a
newsletter.

1. Someone posts in a designated Slack channel: *"Write a blog post about connectors from the past 2 weeks."*
2. A model reads the topic and time range (default 7 days, at most 90).
3. The run lists the configured GitHub owners' public repositories, asks the
   model which ones relate to the topic, and reads their merged pull requests,
   the largest pull requests' changed files, and their commits in the window.
4. The model writes a structured blog post. The application renders it into a
   self-contained HTML artifact, keeping only links found in the research.
5. The model writes a newsletter email based on the post.
6. The full draft is posted to the request's Slack thread for review. Reviewers
   reply `approve` to send it, `reject` to stop, or any feedback, which the
   model uses to write a revised draft that is posted back for the next round.
   Anyone can also polish the text in the pre-publish editor, which previews the
   post and email exactly as they will ship, and approve from there. Dex Web's
   Approve, Revise, Retry, and Reject Actions keep working.
7. On approval, the newsletter goes to every subscriber through Gmail, one
   email per reader, each with a signed unsubscribe link. The requester's Slack
   thread follows along.

Readers subscribe on a one-field page served by this application. Subscribers
live in Dex Attributes on one long-lived `NewsletterSubscribers` Flow; there is
no spreadsheet or database.

Built from [dex-template-basic-process](https://github.com/superdurable/dex-template-basic-process)
`v1.7.2`: Go backend with Dex Go SDK `v0.13.1` and a React reader UI. Dex Server and CLI are
`v0.14.2`, one patch ahead of the template's `v0.14.1`, for the Dex Web theme in connector setup frames.

## Architecture

| Flow | Identity | Lifecycle |
| --- | --- | --- |
| `BlogPost` | `blog-post-<team>-<channel>-<message ts>`, one per Slack message | request → research → writing → review → delivery → `sent`, `rejected`, `no-changes-found`, `not-a-blog-request`, or `delivery-stopped` |
| `NewsletterSubscribers` | `newsletter-subscribers`, one list | open indefinitely; Subscribe/Unsubscribe RPCs and a Dex Web removal Action |

Connectors (all released):

| Need | Connector | Capability |
| --- | --- | --- |
| Slack request and thread replies | `slack` v0.11.0 | Trigger `channelThreadCreated`; Mutation `postThreadReply` |
| GitHub research | `github` v0.8.0 | Queries `listPublicRepositories`, `listMergedPullRequests`, `listPullRequestFiles`, `listCommits` |
| Language model | `llm` v0.1.0 | Query `generateText` with provider/model routing (OpenAI, Claude, Gemini) |
| Email | `gmail` v0.13.0 | Mutation `sendMessage` |

### Swapping the model provider

The four generation Steps (`InterpretBlogRequest`, `ChooseRepositories`,
`WriteBlogPost`, `WriteNewsletter`) use the unified `llm` connector. The
connection's default model starts as `gemini/<model>`. To switch providers, add
that provider's key to the `llm` connection and change its model, or pick a
model per Step with the Step's model picker in Dex Web, such as
`anthropic/claude-sonnet-5` or `openai/gpt-6-sol`. No code changes. The
connection's default model and the Step picks are read when the application
starts, while keys are re-read on every call, so restart the application (`make
dev-app`) after changing a model. Dex Web replaces every key on each save:
re-enter the Gemini key if a Step still selects `gemini/`.

### Roles and permissions

Every Action requires one permission, `newsletter.manage`. Grant it to your
editor role: locally, choose it under **Working as** in Dex Web; in production,
the authenticating proxy maps the editor role to it.

| Action | Available when |
| --- | --- |
| Approve and send | `awaiting-review` |
| Revise (notes) | `awaiting-review` |
| Retry | `needs-attention` (a generation, GitHub, or Gmail failure stopped the run) |
| Reject | `awaiting-review` or `needs-attention` |
| Remove subscriber | always, on the subscriber list |

Dex Web's local **Working as** selector is development-only. In production, put
Dex Web behind an authenticated proxy that injects the trusted permission header.

## Run it locally

Requires Go 1.25, Node 22, and Dex CLI `v0.14.2` on `PATH` (or `DEXCLI=...`).

```bash
make bootstrap
cp config/blog-newsletter.example.json config/blog-newsletter.json   # set github.owners
make dev-dex    # terminal 1: Dex Web at http://127.0.0.1:8842
```

In Dex Web **Connections** (`http://127.0.0.1:8842/v2/connections`), set up:

- `slack-workspace`: Slack app with Socket Mode, plus the **blog request channel**
  on the `blog-post-request` Trigger binding and the same channel with its
  **Reviewers** on the `blog-post-review` Trigger binding (only their thread
  replies approve, reject, or give feedback);
- `github`: GitHub OAuth app with `read:user user:email`;
- `llm`: a Gemini API key and a default model such as `gemini/gemini-3.5-flash`;
- `newsletter-sender`: the Gmail account that sends the newsletter.

Then start the application:

```bash
make dev-app    # terminal 2: Worker, Slack Trigger, reader pages at http://127.0.0.1:8844
```

Google access tokens from Dex Web last one hour. With a Workspace service
account authorized for domain-wide delegation of `gmail.send`, keep the Gmail
connection supplied with fresh tokens:

```bash
DEX_CONNECTOR_CONFIG_FILE=$PWD/.dex-dev/connectors/connections.json \
  scripts/local-connections/refresh-gmail-delegated-token.py \
  --service-account-key path/to/service-account.json --sender newsletter@example.com
```

Blog artifacts are written to `artifacts/blog/<run id>/<slug>-r<revision>.html`.
Set `newsletter.publicBaseUrl` to the reader-facing URL before sending real
newsletters; a loopback URL makes unsubscribe links work only on this machine.

## Verify

```bash
make check    # FDG 2.0 for both Flows, unit, real-Dex integration, real E2E, build
```

The integration tests run a real Dex Server with local fake Slack, GitHub,
Gemini, and Gmail endpoints. They cover Trigger routing and redelivery, the
revise loop, stale and repeated Actions, Worker replacement during review,
generation-failure retry, a Gmail auth stop and retry without duplicate
emails, unsubscribe links, and the Dex Web removal Action.

## Known limits

- Research and delivery run one provider call at a time: a connector Step's result
  does not say which input it answered. A send takes about a second, so large
  lists take a while.
- The subscriber list is one Attribute capped at 10,000 addresses. Dex Web does not
  pass RPC loads yet ([superdurable/dex#562](https://github.com/superdurable/dex/issues/562)),
  which rules out an AttributeMap for now. Dex Web Actions also run without their
  registered locks, so editor decisions carry the review round and removals go
  through a Channel.
- Subscribing has no double opt-in or rate limit. Gmail's send API sets no
  `List-Unsubscribe` header.
- The editor link is a signed capability link, posted in the Slack thread and
  Dex Web: anyone who has it can edit and approve that run's draft. Put the
  editor behind your SSO before exposing it beyond a trusted team.
- Slack feedback after manual edits revises the blog from the edited version, then
  regenerates the newsletter from it, so manual newsletter edits are replaced.
- The post is an HTML artifact; publishing it to a website is up to you
  (`blog.postUrlTemplate` links the newsletter to it).
