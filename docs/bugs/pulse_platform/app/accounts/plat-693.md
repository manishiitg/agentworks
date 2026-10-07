[← app / accounts](index.md)

# PLAT-693: Per-account token limits on shared server accounts

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | app |
| Area | accounts |
| Summary | Each shared server account (Codex, Muse, Claude Code, ...) has its own per-person daily and weekly token limit, with per-person overrides; reaching it blocks only that account. The PLAT-683 person-wide limit stays as an overall cap. |

## What happened

Owner (2026-10-07): a server has several shared admin accounts
(`global:codex-cli`, `global:muse-cli`, `global:claude-code`, ...). The
[PLAT-683](plat-683.md) limit counted them all together, so one cap had to fit
plans that run out at very different rates. Each account needs its own limit.

## Fix

- **Account defaults** (per person, everyone on that account):
  `config/provider-account-settings.json` `token_limits: {"codex-cli": {daily,
  weekly}}`, next to who may use the account and its allowed models. Admin sets
  it with `PATCH /api/provider-connections/global:<provider>` `{token_limits}`
  (zeros clear it); the account view returns `token_limits`. UI: Providers →
  the admin-managed account → menu → **Limits**; the row shows "Limit per
  person: 2M a day · 10M a week" (or none).
- **Per-person overrides**: users.json `account_token_limits: {"codex-cli":
  {daily, weekly}}`, set with `PUT /api/admin/users/{id}`
  `{account_token_limits: {"codex-cli": {...}}}` (named accounts replaced, an
  entry with no limit removes the override, others untouched). A field set in
  the override replaces that field of the default; an empty field falls back to
  the default.
- **What counts**: only that account's events, cost ledger `account_id ==
  "global:<provider>"` (turns on `server-default:<provider>` are recorded as
  `global:<provider>`). New `costledger.Ledger.AccountTokensByAccount` (one
  query, grouped by account); `AccountTokens` sums it. The 20 s per-person cache
  now holds the per-account map.
- **Enforcement**: the same choke point, `admitProviderAccount` server-account
  branch → `sharedAccountTokenLimitRefusal(ctx, principal, provider)`: the
  account's effective limit first ("You have used today's 2M tokens on the
  shared Codex account (resets 00:00 UTC). Switch to another account in Models
  or ask an admin to raise it."; weekly: "this week's ... resets Monday 00:00
  UTC"), then the unchanged overall cap. No limit anywhere → no ledger read.
  Scheduled runs (`scheduledRunTokenLimitRefusal`) check the workflow's provider
  (else the Goals default provider); a run on the owner's own account is not
  refused.
- **Usage API**: `GET /api/me/token-usage` and each user in `GET
  /api/admin/users` add `accounts: {provider: {label, daily_used, weekly_used,
  daily_limit, weekly_limit, default_limits, override, state}}` for every
  shared account used this week or with a limit.
- **UI**: the chat-input chip and the Models panel notice show the account the
  chat uses (`agentProfileConnectionID` `global:<p>`; empty = the selected
  provider's server account; own account = nothing), or the overall cap when
  that is nearer its limit (`shownTokenFigures` in `utils/tokenLimits.ts`).
  Access → Users keeps the overall Daily/Weekly fields ("All: ...") and adds
  one line per shared account (use vs effective limit, "(own limit)" when
  overridden); clicking it edits the override (placeholder shows the default),
  "+ Limit on an account…" adds one.
- **MCP**: the external tools `get_token_usage` / `set_token_limits`
  ([PLAT-690](plat-690.md)) take `account`: read one account, set a person's
  override on it, or with no person set the account default (through the
  Providers write path). Asserted in
  `TestTokenLimitToolsAdminSetsReviewerReadsOthersRefused`.
- Test: `TestTokenLimitsPerSharedAccount` (Codex default refuses only Codex
  and names it, Claude still admitted; Alice's override beats the default; the
  overall cap still refuses across accounts; usage view shows default vs
  effective).

- **Unlimited override** (owner 2026-10-07): in a person's account override a
  field of `-1` (`TokenLimitUnlimited`) is unlimited even when the account has a
  default; `0`/empty still falls back. `normalizedOverride` keeps `-1` for
  overrides only (the overall cap and account defaults treat it as `0`);
  `effectiveAccountTokenLimits` lets a non-zero override field replace the
  default. UI: the override editor's **Unlimited** button (or typing
  "Unlimited"), the row reads "(unlimited)". MCP `set_token_limits` with
  `account` + person accepts `-1` (or "unlimited"). Test:
  `TestAccountTokenOverrideUnlimitedBeatsDefault`.

## Left

- Not deployed. Verify live on Excellence after deploy: set a small Codex
  default, run a Code turn on the shared Codex account, see the named refusal;
  the same person on Muse still runs.
