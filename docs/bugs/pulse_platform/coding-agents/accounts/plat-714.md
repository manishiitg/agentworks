[← coding-agents / accounts](index.md)

# PLAT-714: Model limits per person, matching token limits

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | coding-agents |
| Area | accounts |
| Summary | Allowed models per person on the shared server accounts, like token limits: MCP get_token_usage shows them, set_allowed_models sets account and per-person lists; enforcement and pickers use the turn person's effective list; Providers row and Users page show/edit them. |

## What happened

The owner asked for model limits that work like the token limits
(PLAT-683/693/698): today a shared server account (`global:<provider>`) has
one `allowed_models` list for everyone (provider-account-settings.json), set
only from the account row's ⋯ → Models menu, with no MCP access and no
per-person exceptions.

## Fix

- **Per-person override** (users.json `account_allowed_models`, keyed by
  provider): a list replaces the account's list for that person, `["*"]` is
  every model, none falls back to the account's. Written through the admin
  user write (`PUT /api/admin/users/{id}` `account_allowed_models`).
- **Enforcement** (`provider_allowed_models.go`): `accountAllowedModels`
  returns the effective list of the turn's person for a server account. The
  person is the token-limit person (`tokenLimitOwnerForScope`): the
  signed-in principal, or the owner a Slack channel bot turn is billed to.
  Named explicitly in handleQuery (turn model and workflow phase model),
  workflow role models (`buildWorkshopConfig`, phase refresh), and
  delegation (tier and sub-agent model); product chats and other callers
  derive it from the request context. A disallowed model still runs on the
  first allowed one; the turn never fails.
- **Pickers**: `allowed_models` on a server account in `GET
  /api/provider-connections` is now the caller's effective list, so
  WorkModelsPanel, the workflow model panel and every other picker fed by it
  offer only that person's models. `default_allowed_models` carries the
  account's own list for the Providers page.
- **MCP**: `get_token_usage` returns `account_allowed_models` (each account's
  list, null = all) and per person `allowed_models` (per account that limits
  them: `models`, `source` account|person). New `set_allowed_models` (admin +
  users:manage, audited as `set_allowed_models`): account alone sets the
  account list (null/[] = all); with a person sets their override (list,
  null clears, `["*"]` or `all_models: true` = every model). Ids are checked
  against the provider's model catalog when it has one.
- **UI**: the Providers server row reads "Models: gpt-6-luna only" / "All
  models" with an Edit button for admins; Access → Users shows "<Account>
  models: … (own)" under each person's account limits, with "+ Models on an
  account…" and an editor offering Account default / All models / Only these.
- Test: `TestAllowedModelsPersonOverrideDecidesTheTurnModel` (set, refuse an
  unknown id, resolve per person, all/clear, usage read, reviewer refused,
  audit); the external tool golden lists include `set_allowed_models`.

## Left

- Not verified live on a server (no deploy in this task).
- Callers that resolve a model with no request context (e.g. retained-policy
  comparisons, scheduled-run token check) keep using the account's list;
  they only compare or pick the account, not the model that runs.

