# Token attribution and MCP costs

Use the live `costs` schema for recorded token use across accessible products.
This reads the existing SQLite event ledger and leaves the cost page unchanged.
Attribution format version 1 records the originating actor, native product identity
and name, activity, model/account and existing session/run/execution references.
The runtime account and shared-account accounting do not change. Dashboard origins
are fields on the target execution's events, so one event contributes once.

```json
{"name":"costs","arguments":{"action":"breakdown","group_by":"product","from":"2026-10-01","to":"2026-10-13","top":10}}
```

Group by `product`, `activity`, `person`, `model` or UTC `day`. The response includes
the full visible filtered total, top rows, omitted remainder and compact coverage.
Use returned product IDs and optional owner IDs to distinguish private projects;
IDs are not paths. Dates are inclusive UTC, defaulting to the last 30 days, with a
maximum 366-day window. Top and detail limit are bounded to 100; defaults are 10/50.

```json
{"name":"costs","arguments":{"action":"detail","product_kind":"workflow","product_id":"wf_example","from":"2026-10-01","to":"2026-10-13","limit":50}}
```

Detail filters a product/activity/person or dashboard origin, then lists individual
executions with tokens, pricing basis, model keys, recorded first/last event times
and permitted link IDs. Pass `next_cursor` unchanged with the same filters to read
the next page. Every page checks current access. Totals cover the whole filtered
range, not just the page. Model generation milliseconds do not imply run wall time.

Existing product sharing and connection consent/bounds determine visibility:
shared products expose every actor's usage; personal usage belongs to its actor;
admins with `users:manage` see the server. Restricted admin connections keep their
product bounds. Private Code remains personal. Account credentials, other people's
personal account names, prompts and arbitrary tool metadata never appear. Unknown
historical actor/activity stays unattributed; product mapping uses current native
manifests or validated Relay release provenance, without guessing from names.

Input includes cached tokens exactly once through existing ledger normalization.
Provider actual, token estimates and subscription shadow costs stay separate;
unpriced/missing usage is visible. This does not invent vendor charges or combine
the separate opt-in external-agent native usage ledger with model events.

Production writers: admitted chat/Builder turns; workflow agents;
delegated children; product schedules and functions; channel bots; Python Relay
agents; paid virtual tools and MCP activity. The durable queue preserves trusted
origin context. Dashboard dispatch on the current base has no execution adapter;
its admitted origin context is ready for adapters to inherit, without a new charge.
New contexts do not come from client JSON. Unsupported/missing source evidence is
reported in coverage rather than attributed to a guessed product or person.

Coverage source is `platform_cost_events`. External gateway inference proxies to
its separate private backend and its usage is not imported into this ledger;
external-client native usage is also separate. Gateway-backed agent events already
recorded by the platform remain counted once. Do not interpret the platform total
as a reconciled vendor invoice or as every external client's consumption.

Release note: first opening an older large ledger adds attribution columns and
indexes. The one-time index build can pause ledger writes at startup. Queries use
WAL reads and a 15-second/200,000-event scan bound; narrow a busy date window when
that bound is reached. Existing cost-page UI and account limits are unchanged.
