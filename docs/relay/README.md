# Relays

A Relay is a fixed chain of agents you run from anywhere: JSON in, agents and
scripts do the work, JSON out. You describe what you want in a Builder chat,
test the draft, then publish versioned releases (`v1`, `v2`, …) that stay
stable while you keep editing.

## How one call flows

<div class="sec-flow">
<div class="sec-box"><b>1. Call</b><span>POST your function, input JSON, and an idempotency key. Empty version means the active release.</span></div>
<div class="sec-arrow">→</div>
<div class="sec-box"><b>2. Run pinned</b><span>The run executes that release's frozen snapshot — later publishes can't change it mid-flight.</span></div>
<div class="sec-arrow">→</div>
<div class="sec-box hi"><b>3. Poll result</b><span>GET the run until it completes. The response carries the JSON result plus the version that ran.</span></div>
</div>

Repeat an idempotency key and you get the original run back (`duplicate:
true`), even after newer releases publish. Reuse a key with *different* input
and the API refuses with 409 — keys are promises, not suggestions.

## Drafts vs releases

| | Draft | Release (v1, v2, …) |
|---|---|---|
| Lives in | Your editable workspace | A frozen, checksummed snapshot |
| Changes when | You edit in the Builder | Never — publish mints a new one |
| Runs via | Builder tests | API calls (active, or a pinned older one) |
| On failure | You see it in chat and fix it | Fails visibly with the step and reason |

Publishing requires a valid graph: a final output agent producing JSON, at
least one enabled function with a required object `INPUT`, and saved code for
every script step. Only owners and editors can publish or call.

## What's inside a Relay

<div class="sec-lanes">
<div class="sec-lane"><h4>Agents</h4>Authored prompts that read the caller's JSON (<code>{{input}}</code>), use tools and skills, and must return valid JSON.</div>
<div class="sec-lane"><h4>Scripts</h4>Strict Python steps with saved code. A script failure stops the run — no retries, no repairs.</div>
<div class="sec-lane"><h4>Branches</h4>Deterministic routes on JSON values. Every route must reach the output agent; no loops or joins.</div>
</div>

Schedules can fire a Relay on a timer with a fixed JSON payload. Slack
notifications are supported; WhatsApp, bot chats, and Pulse are not.

## API reference

All calls are authenticated the same way as the rest of the API.

**Start a run** — `POST /api/relays/{id}/runs`

```json
{
  "function": "greet",
  "input": { "name": "Ada" },
  "idempotency_key": "order-8842-attempt-1",
  "version": "v1"
}
```

`version` is optional and defaults to the active release. Returns `202`:

```json
{
  "run_id": "b28e48e9-…",
  "status": "running",
  "version": "v1",
  "duplicate": false,
  "poll_url": "/api/relays/wf_bb882752/runs/b28e48e9-…"
}
```

**Poll a run** — `GET /api/relays/{id}/runs/{run}`

Returns the run status while it works, and the output agent's JSON plus
`version` once complete. Unknown runs and other people's runs both answer
404 — the API never confirms what you may not see.

**List releases** — `GET /api/relays/{id}/releases`

Returns the active version and every published release with its content hash.

## Limits to know

- Snapshots hold UTF-8 text only (50 MiB / 5000 files). Binary assets are
  outside the release contract.
- Schedules execute the draft, not a pinned release.
- A crashed run ends honestly as `interrupted` and stays pollable — but it
  never resumes mid-chain. Don't describe Relays as resumable.
- A release whose files change after publishing fails its checksum and stops
  serving until you republish. Runs should only write inside their run folder.

Related: [Security overview](../security/README.md),
[Sharing and slots](../security/sharing.md).
