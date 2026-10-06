[← app / chat](index.md)

# PLAT-407 — Models: allowed models per account, one Model card on the workflow Models tab, changes apply between turns

| Field | Value |
|---|---|
| State | deployed |
| Priority | P2 |
| Product | app |
| Area | chat |
| Summary | fixed on `main`; deployed to Excellence only (the between-turns change also in the RTS build from `ad3956735`). |

| Coordination | Value |
|---|---|
| State | fixed on `main`; allowed models (`5bb70410a`) and the Models tab (`74c404df3`) are in Excellence `agents-0cf68aa9` only; the between-turns change (`ad3956735`) is in `agents-0cf68aa9` / `agents-e7db4f50` and the RTS build from `ad3956735`; deploy pending elsewhere |
| Severity | P2 |
| Date | 2026-10-03 |
| Owner | frontend-chat |
| Related | PLAT-402 (Native agent tools), PLAT-406 (no model picker in the chat input) |

## What was done

### Allowed models per provider account (`5bb70410a`)

Owner request: when anyone adds an account, admin or user, they can choose which models are available (default all, optional restriction), e.g. limit the Codex account to `gpt-5.3-codex`.

- Every provider account (admin-managed `global:<provider>` and personal) has an optional `allowed_models` list; absent or empty = every model, no migration.
- `PATCH /api/provider-connections/{id}` with `allowed_models` (`[]` clears): an admin for `global:<provider>` (stored in `config/provider-account-settings.json`), the owner for a
  personal account (stored on the account). Authorization is the neighbouring routes' own: a personal account is private to its owner, admins included. POST accepts it when adding an
  account; the account view carries it for everyone who can see it.
- Enforcement is server-side: a new explicit pick of a disallowed model is refused ("<model> is not allowed on <account>; allowed: ..."); a saved or default selection naming a
  disallowed model runs on the first allowed model instead. Choke points: `handleQuery` before the agent config is built (request, saved project/workflow, profile and product-default
  models; Crew, Code, Goals, workflow chats, bots); product chat turns before the runtime is bound (`constrainProductChatModel`: refuse when the model differs from the
  conversation's bound one, else fall back); `workshopConvertAgentLLMConfig` (every workflow role); sub-agent delegation; delegation tier config (`LoadAndResolveTierConfig`);
  `effectiveProductDefaults`.
- Where: `agent_go/cmd/server/provider_allowed_models.go`, `provider_account_routes.go`, `provider_accounts.go`; UI `AllowedModelsEditor.tsx` (Providers, account menu "Models"; card shows
  "Models: All models / N models"), `utils/allowedModels.ts`, `WorkModelsPanel.tsx`, `WorkflowLLMConfigurationPanel.tsx` (pickers offer only allowed models).

### Workflow Models tab (`74c404df3`)

One Model card (agent + model on a line, reasoning-effort buttons inside, account chooser only with more than one usable account) sets every role. "Use different models for different roles"
(off by default, on at load when saved roles differ) reveals a compact list: role name, one-line description, a summary button opening a popover with the existing pickers, a dot for a customised role, a reset
arrow. Switching off asks inline, then sets every role to High reasoning's value. "Use provider defaults for all roles" stays. Role ids (tier_1..3, builder_llm, pulse_llm) and the saved `llm_config`
format are unchanged; a provider-profile workflow opens with the switch off and the card on High reasoning's default, with a note. Where: `WorkflowRoleModels.tsx`, `RoleModelPopover.tsx`,
`utils/roleModelSummary.ts`, `WorkflowLLMConfigurationPanel.tsx` (the "Models per role" collapsible and its localStorage flag are gone); the `ProviderAccounts` selection-only picker uses the same
label/select size. Crew, Code and Relay (builder-only) screens are unaffected.

### Model or reasoning-effort change between turns (`ad3956735`)

Owner: "Reasoning or model change should apply only when the agent has completed turns." Changing Muse's reasoning effort while a turn ran and then sending a message relaunched the retained CLI at once
(`interruptWorkflowPolicySession`), cancelling the running turn ("muse tmux session ... died before run completion"). Now, when the runtime changed and a turn is running, the message waits in the durable
turn queue; when it runs nothing is in flight and the CLI relaunches with the new model/effort (`server.go`, before `interruptWorkflowPolicySession`).

## Left

- Not covered by allowed models: the provider's account resolver (`llm.ProviderAPIKeys.ResolveConnection`) receives no model, so a model initialised outside the listed paths is not checked; a retained CLI keeps
  its model until its next turn (turns re-resolve). The list is not validated against the provider's catalog (catalogs are dynamic); an unknown id never matches a picker entry.
- Deploy the allowed models and Models tab beyond Excellence (RTS, Confida, Dominion, SparkQuill).

## Register notes

[PLAT-407](plat-407.md), fixed on `main`; deployed to Excellence only (the
between-turns change also in the RTS build from `ad3956735`). Optional `allowed_models` per provider account enforced on
the server; workflow Models tab with one Model card; a model or effort change waits for the running turn.
