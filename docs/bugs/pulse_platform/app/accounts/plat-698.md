[← app / accounts](index.md)

# PLAT-698: Slack channel bot usage counts toward the target owner's token limits

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | app |
| Area | accounts |
| Summary | A Slack channel turn's shared-account tokens now count toward, and are limited by, the owner of the workflow, Crew or Code the bot answers for. |

## What happened

Shared-account token limits (PLAT-683 overall cap, PLAT-693 per account) key
on the ledger's `user_id`. A Slack **channel** turn on a workflow runs as a
bot identity (`bot-slack-<hash>`, `claims.Provider == "bot_route"`) that has
no user record, so its usage was neither counted nor limited: a busy channel
could spend a shared account without touching anyone's limit. Slack DMs and
WhatsApp already run as the person and were counted.

Owner decision 2026-10-07: channel usage counts toward the owner of the
target the bot answers for.

## Fix

- **Owner** (`botRouteTokenOwner`, `agent_go/cmd/server/token_limits.go`):
  for a `bot_route` principal, the turn's folder (else the route's) names the
  target. Workflow: `workflowExecutionOwnerUserID` (the same active owner a
  scheduled run uses). Crew: `resolveCrewPath` owner (owner registry). Code:
  `resolveProjectOwner` (owner registry, else path owner). Falls back to the
  route's resource owner; the owner path segment is mapped to its directory
  user ID. Any other turn resolves to "" and is unchanged.
- **Ledger** (`pkg/costledger`): new `billing_user_id` column
  (`Entry.BillingUserID`, migrated with `ensureCostEventColumn`, indexed with
  `occurred_at`). The bot identity stays in `user_id` for audit. A person's
  token usage counts rows with `billing_user_id = person`, or with no billing
  user and `user_id = person`. Empty when the billing user is the user.
  The cost observer takes `WithBillingUser`; handleQuery and delegated
  sub-agents of a channel turn set it.
- **Enforcement**: `admitProviderAccount` checks the owner's limits for a
  channel turn (`providerAccountScope.TokenOwner`, else resolved from ctx).
  Over a limit, handleQuery answers 429 and the bot posts the server's text in
  the thread, e.g. "The owner of workflow Weekly report has used today's 2M
  tokens on the shared Codex account (resets 00:00 UTC); this channel's bot
  turns count toward their limit. Try again after the reset, or ask them or
  an admin to raise it." It never names the owner's email.
- **Views**: Access → Users, `/api/me/token-usage` and MCP `get_token_usage`
  include the bot usage in the owner's numbers, with the bot part as
  `daily_via_bot` / `weekly_via_bot` (overall and per account). The
  `get_token_usage` from/to range totals bill the owner too.
- Personal accounts never count (unchanged: only `global:` account IDs).
- Existing rows are not backfilled: limits are daily/weekly windows, so old
  bot rows age out within a week.

Test: `TestSlackChannelBotTurnCountsTowardTargetOwner`
(`agent_go/cmd/server/token_limits_test.go`).

## Left

- The frontend does not show the `via_bot` share yet (the API returns it).
- Live input sent into an already running channel turn is admitted on the
  live-input path, which only re-checks a named account; a server-default turn
  is checked when the next turn starts.
- Not verified live in Slack (no deploy in this change).
