[← app / accounts](index.md)

# PLAT-683: Per-user daily and weekly token limits on shared accounts

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | app |
| Area | accounts |
| Summary | Admins set a per-person daily and weekly token limit on the shared server accounts; at the limit new turns on a server account are refused, own accounts keep working. |

## What happened

Owner request (2026-10-07), mainly for Excellence: people share the admin
(server) provider accounts (`global:<provider>`), and one person can use up
the shared plan for everyone. There was no per-person cap.

## Fix

- **Limits** live on the person's `config/users.json` record as
  `token_limits: {daily, weekly}` (absent or 0 = unlimited, the default). An
  admin edits them through the existing `PUT /api/admin/users/{id}` with
  `token_limits` (replaces both). `GET /api/admin/users` returns each person's
  limits and current usage (`token_usage`).
- **What counts**: tokens in the cost ledger (`_system/costs.sqlite`) whose
  `account_id` starts with `global:`, for that person's `user_id`. The ledger
  already recorded the account on every LLM-call event (`costobserver.WithAccount`
  in handleQuery, delegation, Relays and the workflow orchestrator, so Code,
  Crew, Goals workflows, Relays and Brain chats are all covered); nothing new
  had to be recorded. A token is input (cached input counted once, as the
  ledger's `input_tokens`) plus output; reasoning is inside output for the CLIs
  that report it. Own accounts and accounts shared by another user have their
  own IDs and never count. New: `costledger.Ledger.AccountTokens` and an index on
  `(user_id, occurred_at)`.
- **Windows**: calendar day and Monday-start week, both UTC (there is no server
  timezone setting); the UI says UTC.
- **Enforcement**: `admitProviderAccount` (server-account branch) refuses when the
  principal is at or over either limit, with "You have used your daily limit of
  N tokens on the shared accounts (resets at 00:00 UTC). Use your own account or
  ask an admin to raise it." (weekly: "resets Monday 00:00 UTC"). That one
  admission runs for every turn in handleQuery and for every model start
  (`llmguard.WithServerAccountAdmission` → `ResolveConnection`), so all products
  are covered. Usage is cached per person for 20 s. A ledger read error logs and
  admits (budget control, not a security gate).
- **Running turns finish**: nothing stops a turn already running. A model that
  starts inside a running turn (for example a sub-agent) on a server account is
  admitted again and can be refused once the limit is reached.
- **Scheduled and triggered runs** count toward the run's identity (the
  scheduler's `OwnerUserID`, `workflowExecutionOwnerUserID`). Decision: a run
  whose workflow runs on a server account while that identity is over a limit is
  refused before it starts and recorded as failed with the limit message
  (`scheduledRunTokenLimitRefusal` in `runJob`). A workflow set to its owner's own
  account is not refused.
- **UI**: Access → Users has a "Shared-account tokens (UTC)" column with Daily and
  Weekly fields (empty = unlimited; accepts 500k / 5M) and the person's use today
  and this week. The Models panel (`WorkModelsPanel`) shows the person's own use
  against their limits when a limit is set (`GET /api/me/token-usage`), amber
  from 80%, red at the limit.
- Code: `agent_go/cmd/server/token_limits.go`, `user_directory.go`,
  `provider_accounts.go`, `scheduler.go`, `pkg/costledger`;
  `frontend/src/components/admin/UsersAdminPanel.tsx`,
  `components/providers/SharedTokenUsageNotice.tsx`, `utils/tokenLimits.ts`.
- Test: `TestTokenLimitsCountAndCapServerAccountsOnly` (own-account use does not
  count; over the limit a server turn and a scheduled server run are refused, the
  own account and an unlimited person are not).

## Left

- Not deployed. Verify live on Excellence after deploy: set a small daily limit
  on a test person, run a Code turn on the server account, see the refusal; the
  same person's own account still runs.
- The 80% warning is shown in the Models panel only, not as a chat banner.
- Live input typed into an already running server-account session is not
  re-checked (it is part of the running turn).

## Chat input chip (2026-10-07)

Owner: people should see it where they type. The chat input toolbar shows a small chip next to Attach (e.g. "1.2M/5M today", the tighter of the two limits; amber from 80%, red at the limit; both limits and resets in the tooltip). Hidden when no limit is set. The Models panel notice stays.

## Per-account limits (2026-10-07)

Each shared account now also has its own per-person limit with per-person overrides; the limit above stays as the overall cap. See [PLAT-693](plat-693.md).
