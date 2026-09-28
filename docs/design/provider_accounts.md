# Provider accounts: installed, private and shared

Status: built on branch `feat/provider-accounts` (not merged, not deployed).
See "Implementation notes" at the end.

A provider is a coding CLI (Claude Code, Codex, Cursor, Muse, Pi, agy). An
account is whose login or key that CLI runs as. This design makes every
account's origin visible and lets a person share their own account on
purpose.

## Today

- **Server account (one per provider).** Merged from three sources that the
  UI shows as a single "Server account":
  1. `.env` / service environment written at installation
     (`CLAUDE_CODE_OAUTH_TOKEN`, `CURSOR_API_KEY`, `META_API_KEY`, ...);
  2. keys an admin saves on the Providers page (encrypted, override 1);
  3. the CLI's own login in the service user's HOME (used when there is no
     key; an admin's "Sign in" writes it).
  Anyone whose workflow, Crew or Code selects the server account uses it.
- **Private account (per user).** Added under "Add a private account":
  a key/token, or a browser login in the account's own HOME
  (`~/.local/state/agentworks/provider-connections/<id>/home`). Used only by
  runs whose principal is the owner (`connectionAPIKeys`,
  `provider_connections.go`); the server's credentials are stripped from the
  CLI's environment.
- **Per-workflow credential.** A per-user, per-workflow Claude Code token,
  Cursor key or Pi Gemini key (`workflow_provider_auth.go`).
- Install-time knobs: `SUPPORTED_LLM_PROVIDERS`, `LLM_CONFIG_LOCKED`,
  `ALLOW_PERSONAL_PROVIDER_CONNECTIONS`.

Problems: nobody can see which of 1-3 a server account really is; a sign-in
on the Providers page silently replaces the login everyone uses (Muse on
excellence, 2026-09-28); and a person cannot share their own account with a
workflow, a Crew or a colleague.

## Model

Two kinds of account, both listed per provider.

### 1. Installed accounts (server-provided)

Configured by the installation, not in the UI. Shown with an
**Installed** badge and read-only details:

- source: "installation (.env: `CURSOR_API_KEY`)", "installation (CLI login
  on the server: <account email or id when the CLI reports one>)"; never the
  value;
- who may use it, from the installation's policy (below);
- status: working / signed out / key rejected (from the existing inspect
  action).

Keys an admin adds on the Providers page become **Admin-configured**
accounts with the same shape, editable by admins only. The CLI login in the
service HOME is an installed account; re-signing it is an admin action with
the label "Sign in the server's shared <provider> login (used by
everyone allowed below)".

**Installation policy.** One setting per provider, written by the installer
or `.env`, e.g.

```
AGENTWORKS_PROVIDER_POLICY='{
  "claude-code": {"available_to": "all"},
  "cursor-cli":  {"available_to": "admins"},
  "muse-cli":    {"available_to": {"products": ["code"], "users": ["alice@x.com"]}}
}'
```

`available_to`: `all` (default, today's behaviour), `admins`, or any mix of
`products` (agentworks = workflows, work = Crews, code = Code) and `users`
(email or user id). A provider absent from the policy keeps today's
behaviour. The UI shows the policy in words ("Available to: Code, and
alice@x.com").

**Product defaults** (owner decision 2026-09-28). The same setting names the
provider, model and account a product starts with, so a new workflow, Crew
or Code needs no model setup:

```
AGENTWORKS_PRODUCT_DEFAULTS='{
  "code":       {"provider": "muse-cli",    "model": "<model id>"},
  "work":       {"provider": "claude-code", "model": "<model id>"},
  "agentworks": {"provider": "claude-code", "model": "<model id>"}
}'
```

- The account is the provider's installed account unless `account` names an
  admin-configured one.
- A default must be admitted by that provider's `available_to` for the
  product; the server refuses to start otherwise.
- New items copy the default into their own settings when created, so a
  later default change does not silently switch existing workflows.
- The Providers page shows each product's default; admins change it there
  (an admin-configured default) unless the installation pins it.

### 2. User accounts (added by a person)

Added by any user (unless the installation turns personal accounts off, as
`ALLOW_PERSONAL_PROVIDER_CONNECTIONS` does today), with **browser login**
(Claude Code, Codex, Cursor, Muse) or an **API key / token**. The owner
chooses who can use it:

- **Private**: only the owner's own runs (today's behaviour, the default).
- **Shared**, with any mix of:
  - **a) Workflows**: chosen workflows. Every run of that workflow (anyone
    who runs it, schedules, triggers) may use the account.
  - **b) Crews**: chosen Crews. Every chat and run of that Crew, by anyone
    with access, may use it.
  - **c) Users**: chosen people. They can pick it for their own workflows,
    Crews and Codes, as if it were theirs, but never see the credential.

Only the owner (or an admin) edits the sharing, re-signs, or removes the
account. Nobody else ever sees the key or the login files.

## Using an account

A workflow, Crew or Code's model settings name one account
(`connection_id`, already in `workflowtypes`). The account picker lists only
accounts the person may select there:

- installed / admin-configured accounts whose policy admits this person and
  product;
- their own accounts;
- accounts shared with them (c), and accounts shared with this workflow or
  Crew (a, b).

**At run time** (every turn, not only when saving), `connectionAPIKeys`
admits an account when one of these holds, else the run fails with "this
account is no longer available to <workflow>"; it never silently falls
back to another account:

| Account | Admitted when |
|---|---|
| Installed / admin | the policy admits the run's principal and product |
| User, private | run principal = owner |
| User, shared (a) | the run's workflow is in the list |
| User, shared (b) | the run's Crew is in the list |
| User, shared (c) | run principal is in the list |

The run principal is the person who started the turn; for scheduled and
triggered runs it is the workflow owner (as `workflow_capacity_wait.go` does
today). Sharing changes and removals apply from the next turn; running turns
finish.

## Security

- **Credentials stay server-side.** Keys stay encrypted; a browser-login
  account's files live in its own HOME. The CLI runs with that HOME, and the
  server's own credentials are stripped (already true for private accounts).
- **Known gap: hybrid mode.** A coding CLI in hybrid mode ("native agent
  tools") is not under Landlock (PLAT-364 part 2), so the agent can read the
  account HOME it runs with, including the login token. For the owner's own
  runs that is their own token. For **shared** accounts it would let someone
  else's run read the owner's login. *(Superseded 2026-09-28: the owner
  decided native tools stay on by default everywhere, shared accounts
  included; see "Decision 2026-09-28: native tools on by default".)*
- **Whose identity and bill.** A shared account's runs act as the owner's
  CLI account: quota, billing, and anything the provider ties to it. The
  share dialog says this plainly.
- **Editing workflows.** Selecting a shared account in a workflow or Crew
  needs edit access to it; the selection is re-checked at run time, so
  copying `connection_id` into another workflow grants nothing.
- **Admins.** Admins can see every account's metadata and sharing (never
  credentials) and remove any account.
- **Audit and cost.** Each turn records the account ID it used (the cost
  ledger already records the session); the owner sees their account's usage
  by workflow, Crew and person.

## Storage

`config/provider-connections.json` (encrypted, today) gains per record:

```json
"sharing": {"mode": "private" | "shared",
            "workflows": ["<workflow id>"], "crews": ["<crew id>"], "users": ["<user id>"]}
```

Installed accounts are not stored; they are derived at start from the
environment, the admin key store and the CLI login check. The policy is
read from `AGENTWORKS_PROVIDER_POLICY` (installer writes it; absent = all).

## UI

Providers page, per provider:

- **Installed** and **Admin-configured** accounts first, with source,
  "Available to", status, and (admin) Re-sign / Edit.
- **Your accounts**: add (Browser login / API key), then "Who can use it":
  Private / Shared with workflows, Crews, people (pickers limited to what
  the owner can see). A warning when sharing: runs act as your account and
  are billed to it.
- **Shared with you**: accounts others shared, with the owner's name.

The workflow / Crew / Code model picker groups accounts the same way.

## Usage and cost per provider and account

Owner request 2026-09-28: the Providers page shows usage for every
installed provider and every account inside it, and costs are grouped by
provider and account.

- **Live usage (quota).** Each account row has a "Usage" button that runs
  the CLI's own usage command (`/usage` for Claude Code and Muse, `/status`
  for Codex) with that account's environment, using the existing guided
  `usage` action with the account's `connection_id`. The result (plan,
  limits, reset time) is shown inline. A person sees usage for the
  accounts they can use; only the owner or an admin sees a private account's.
- **Cost (AgentWorks' own ledger).** Every cost-ledger entry records the
  provider and the account ID the turn used (`global:<provider>`, an
  installed or admin account, or a user account ID). The Providers page shows
  cost and tokens per provider and per account for a date range, split by
  workflow, Crew, Code and person. The cost overview gains "by provider" and
  "by account" views.
  - The owner of a shared account sees its full split (who used it and
    where); others see their own share.
  - Admins see everything.
  - Entries written before this change have no account; they show as
    "unrecorded account" under their provider.

## Tests (end-to-end)

1. Installed Cursor account with `available_to: admins`: a member cannot
   select it and a member's run naming it is refused.
2. Alice shares her Claude login with workflow W; Bob runs W and it works;
   Bob selects it in his own workflow V and is refused; Alice removes W from
   the list and Bob's next W turn is refused with the clear error.
3. Alice shares with Bob (c); Bob uses it in his Code; Carol cannot.
4. *(Flipped 2026-09-28.)* A shared account in Bob's session keeps the
   configured tool mode (native tools on by default), like Alice's own.
5. Signing in the server account is admin-only and logged as the server
   account; a private browser login never touches the service HOME.
6. A turn on Alice's shared account, run by Bob in workflow W, lands in the
   ledger under Alice's account with Bob and W in the split; the Providers
   page shows it for Alice and for an admin, and only Bob's share for Bob.
7. "Usage" on a private Muse account runs `/usage` in that account's HOME,
   not the server's.

## Open questions for the owner

1. *(Decided: yes to product defaults, see "Product defaults".)*
2. *(Decided 2026-09-28: everyone with access to the workflow or Crew uses
   an account shared with it: viewers, editors, co-owners, schedules and
   triggers. Choosing the account in the model settings still needs edit
   access.)*
3. *(Decided 2026-09-28: no. Native tools stay on by default for every
   turn, shared accounts included; see below.)*
4. *(Decided 2026-09-28: yes, from the UI. An admin sets an
   admin-configured account's "Available to" (everyone, admins, products,
   people) on the Providers page; the installation policy is the default
   and an installation-pinned account stays read-only.)*
5. *(Direction 2026-09-28: native tools on by default. Blocked on confining
   the coding CLIs themselves under Landlock and the private /tmp (PLAT-364
   part 2); until then native tools would read other users' trees and every
   account's login files. Once CLIs are confined, the "shared accounts run
   MCP-only for others" rule above is dropped.)*

## Decision 2026-09-28: native tools on by default

The owner decided: "native tools, keep it on by default always now".

- Native agent tools (hybrid) are the default wherever nothing chose
  otherwise: workflow chats, Crews and Code (through their "Native agent
  tools" switch, on unless turned off). Items that explicitly chose
  AgentWorks-only tools keep that choice. Step agents, schedules, webhooks,
  bots and read-only users are unchanged.
- The forced MCP-only for turns on someone else's shared account is
  removed; such turns keep the configured mode.
- **Known exposure until the coding CLIs run under Landlock (PLAT-364
  part 2):** a run on someone else's shared account can read that
  account's login files in its HOME, and native reads in a Code (or any
  hybrid chat) can reach files outside the Code. Confining the CLIs is the
  follow-up that closes both.

## Implementation notes

Built on branch `feat/provider-accounts` (2026-09-28).

### What was built

- **Installation policy.** `AGENTWORKS_PROVIDER_POLICY` per provider:
  `available_to` is `all`, `admins`, or `{admins, products, users}`
  (products: `agentworks`/`workflows`, `work`/`crews`, `code`; users: email,
  username or user ID). An entry may add `"pinned": true`. An absent policy
  or provider means everyone (today's behaviour). A broken policy stops the
  server at start.
- **Admin "Available to".** Admins change any server account's "Available
  to" on the Providers page (`PATCH /api/provider-connections/global:<p>`,
  stored in `config/provider-account-settings.json`); `null` returns to the
  installation policy. A pinned entry is read-only (409). A change that
  would take a provider away from a product whose default uses it is
  refused.
- **Product defaults.** `AGENTWORKS_PRODUCT_DEFAULTS` (`provider`, `model`,
  optional `account` = `global:<provider>`, optional `pinned`). Checked at
  start against the provider's policy. Admins change unpinned products
  (`GET/PUT /api/provider-accounts/product-defaults`). Applied where new
  items read their model: `primary_config` and `product_defaults` in
  `/api/llm-config/defaults` (workflows), the default engine of the Crew
  and Code profiles (profile listing and server-side Crew creation). The
  installation default is added to a profile's engines at registration if
  the product did not list it; an admin default must be an engine the
  product offers.
- **User accounts and sharing.** `sharing: {mode, workflows, crews, users}`
  on each stored account (workflow IDs, Crew roots, user IDs). Only the owner
  or an admin edits sharing, re-signs or removes; sharing may only name
  workflows, Crews and people the owner can see
  (`GET /api/provider-connections/share-targets`; people are the enabled
  accounts every signed-in user already sees in the workflow and Code share
  dialogs, `/api/users/directory`, minus read-only accounts unless the
  caller is an admin). Removing an account first stops the retained CLIs
  whose last turn ran on it, then deletes its HOME (login files).
- **Admission on every turn.** `connectionAPIKeys(ctx, providerAccountScope,
  provider, id)` applies the table above. The resolver attached by
  `withConnectionResolver(keys, scope)` runs at every model init; a model
  that names no account goes through the same check for the server account
  (`llmguard.WithServerAccountAdmission`, applied in `pkg/agentwrapper` and
  the two orchestrator init sites). `/api/query` re-checks before a retained
  CLI gets live input, and `/sessions/{id}/live-input` re-checks too.
  Denials say "this account is no longer available to <workflow / Crew /
  this Code>" and never fall back.
- **Native tools.** Since 2026-09-28 the account does not change the tool
  mode (see the decision above). `handleQuery` still admits the turn on its
  FINAL account (`finalQueryTurnConnection`: a workflow chat that does not
  override its manifest runs on the manifest's account).
- **Per-account actions** (every row, server account and user accounts
  alike; permissions enforced server-side):
  - *Status* (`GET /api/provider-connections/{id}/status[?verify=1]`):
    the CLI's own status command in the account's environment, returning
    signed in / signed out / key rejected and the identity (email or org)
    only. Verified commands: `claude auth status --json` (plus, on
    Refresh, one real `claude -p hi --model claude-haiku-4-5 --max-turns 1`
    because status reports loggedIn for any token), `codex login status`,
    `cursor-agent status --format json`. Muse has no status command; its
    `$XDG_CONFIG_HOME/muse/auth.json` is read for `providers.meta` and only
    `user_email` is returned. Anyone who may use or manage the account.
  - *Usage*: managers get the terminal, others the server-collected text.
  - *Open terminal* (`inspect`): owner or admin for a user account, admins
    for the server account.
  - *Sign in*: per account; the server account's button reads "Sign in the
    shared server login (used by everyone allowed)", admins only.
  - *Sign out* (`POST /api/provider-connections/{id}/sign-out`): the CLI's
    own logout in the account's environment (`claude auth logout`, `codex
    logout`, `cursor-agent logout`, `muse logout`, all verified with
    `--help`; no login files are removed by hand). Browser-login user
    accounts: owner or admin; the server account: admins, after a
    confirmation. Retained CLIs on the account are stopped first. Logged as
    `[PROVIDER_SETUP] <provider> sign-out for server account | private
    account <id> by <user>`. The account record stays. Key accounts have no
    sign-out (remove the account or change the key).
  - *Remove*: user accounts only.
- **Usage.** Owners and admins run `usage` in an interactive terminal in
  the account's own HOME. Anyone else who may use the account (a shared
  account, or the server account for a non-admin) gets no terminal: the
  server sends `/usage` (`/status` for Codex), collects the output, ends the
  session and returns the text; every input to such a session is dropped.
  `authenticate` and `inspect` stay owner/admin only.
- **Cost.** Every ledger entry records `account_id` (`global:<provider>` or
  a user account ID; empty on older rows = "Unrecorded account").
  `GET /api/provider-accounts/costs` and `by_account` in `/api/cost/overview`
  give provider → account → (work, person). Admins see everything, an
  account's owner its full split, everyone else their own share. A
  workflow, Crew or Code the viewer cannot open is shown as "a workflow you
  can't see" (the person and the numbers stay).
- **UI.** Providers page: server accounts with Installed / Admin-configured
  badge, source, "Available to" (admin edit), per-account Usage, sharing
  editor and warning, "Shared with you", product defaults, cost by account.
  The model picker lists only accounts usable for the workflow / Crew / Code
  being edited.

### File map

- `agent_go/cmd/server/provider_accounts.go`: policy, admin settings,
  product defaults, run scope, admission, sharing validation.
- `agent_go/cmd/server/provider_account_actions.go`: per-account status
  and sign-out.
- `agent_go/cmd/server/provider_account_routes.go`: account list / add /
  edit / remove, server-account "Available to", share targets, product
  defaults API.
- `agent_go/cmd/server/provider_account_defaults.go`: product defaults on
  profiles.
- `agent_go/cmd/server/provider_account_costs.go`: cost per provider and
  account.
- `agent_go/cmd/server/provider_connections.go`: credentials, account HOME,
  resolver.
- `agent_go/cmd/server/provider_setup.go`: who may open a setup / usage
  terminal on which account.
- `agent_go/pkg/llmguard/llmguard.go`: `WithServerAccountAdmission`.
- `agent_go/pkg/costledger`, `agent_go/pkg/costobserver`: `account_id`.
- Frontend: `frontend/src/components/providers/*` and
  `frontend/src/components/workflow/WorkflowLLMConfigurationPanel.tsx`.
- Tests: `agent_go/cmd/server/provider_accounts_e2e_test.go` (tests 1-7).

### Known gaps

- One server account per provider. A product default's `account` can only
  be `global:<provider>`; there are no extra named admin accounts.
- The server account's source does not show the CLI login's email; the
  status comes from the existing inspect action.
- Crew Run mode lets every user with the Crew product chat with any Crew,
  so an account shared with a Crew reaches all of them.
- Test 4 now checks (through `handleQuery`) that a shared-account Builder
  turn and a Code turn get hybrid by default. The live part of test 7 is skipped unless
  `AGENTWORKS_LIVE_MUSE_ACCOUNT_HOME` points at a HOME with a Muse login.
- The account registry and the settings file are cached in memory (read
  once, updated on every save; this server is their only writer). If the
  settings file was never readable, the installation policy applies (or
  everyone, without one), so a deployment with no settings never fails a
  turn.
- The mcpagent continuation relaunch and model switch
  (`agent/llm_generation.go`) call InitializeLLM themselves. mcpagent branch
  `feat/llm-config-hook` adds `llm.ConfigHook`; once it is merged and
  go.mod bumped, set `mcpllm.ConfigHook = llmguard.WithServerAccountAdmission`
  at start. Until then those two re-inits are covered only by the per-turn
  server-account check in `handleQuery`.
- Crew and Code creation copy the default through the profile's default
  engine; new workflows copy it into `llm_config` on create.
- Tool-cost ledger rows (paid tools) have no account.
- The model picker's optional `product` prop is not passed by callers yet;
  the server works out the product from `workspace_path`.
