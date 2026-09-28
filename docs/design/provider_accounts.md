# Provider accounts: installed, private and shared

Status: design, for the owner's review. Not built.

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
  else's run read the owner's login. Until CLIs are confined, shared user
  accounts run **MCP-only** (native tools off) in every session that is not
  the owner's; the UI says so when sharing.
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
  are billed to it; others' runs use it with native tools off.
- **Shared with you**: accounts others shared, with the owner's name.

The workflow / Crew / Code model picker groups accounts the same way.

## Tests (end-to-end)

1. Installed Cursor account with `available_to: admins`: a member cannot
   select it and a member's run naming it is refused.
2. Alice shares her Claude login with workflow W; Bob runs W and it works;
   Bob selects it in his own workflow V and is refused; Alice removes W from
   the list and Bob's next W turn is refused with the clear error.
3. Alice shares with Bob (c); Bob uses it in his Code; Carol cannot.
4. A shared account in Bob's session runs MCP-only; Alice's own session keeps
   her configured mode.
5. Signing in the server account is admin-only and logged as the server
   account; a private browser login never touches the service HOME.

## Open questions for the owner

1. *(Decided: yes to product defaults, see "Product defaults".)*
2. Shared (a)/(b): may everyone with access to the workflow or Crew use the
   account, or only its editors?
3. The hybrid-mode rule: accept "shared accounts run MCP-only for others"
   until CLIs are confined?
4. Should admins be able to share an admin-configured account with specific
   users only (it is then just an installed account with a `users` policy)?
