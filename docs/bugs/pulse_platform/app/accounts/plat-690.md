[← app / accounts](index.md)

# PLAT-690: MCP tools to view and set shared-account token limits

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | app |
| Area | accounts |
| Summary | External MCP get_token_usage (admins and Code reviewers) and set_token_limits (admins, new users:manage scope) for the PLAT-683 per-user limits; audited. |

## What happened

The per-user daily/weekly token limits on the shared server accounts
([PLAT-683](plat-683.md)) could only be viewed and set on the admin Users page.
The owner wants to view and set them from an MCP client (Claude Code connected
to a server).

## Fix

`agent_go/cmd/server/external_token_limits.go`, two external MCP tools:

- `get_token_usage` (read-only). Every active person, or one by `user_id` /
  `email`: `daily_used` / `weekly_used` on server accounts (`global:*`, UTC,
  Monday weeks), `daily_limit` / `weekly_limit` (0 = unlimited), `state`
  ok|warning|over, and reset times, from the same `sharedAccountTokenUsageFor`
  the admin page uses. With `from` / `to` (YYYY-MM-DD) it returns per-person
  shared-account token totals for the range from the cost ledger's
  account-attributed events (input + output, as the limits count, with a
  per-account split). Gate: `code:review` with an admin or Code reviewer account
  (the Code review gate), or `users:manage` with an admin account.
- `set_token_limits` (write). `daily` and/or `weekly` (integer, 0 or null =
  unlimited, omitted = unchanged) for a person by `user_id` or `email`. Gate:
  `users:manage` and an active admin account. It writes through the admin UI's
  own handler (`requireAdmin(handleAdminUpdateUser)`, PUT
  `/api/admin/users/{id}`), so normalization, the save and the `[USERS]` log line
  are the same; the person's usage cache entry is dropped and the reply is their
  new limits and current usage.

Scope: `code:review` is review-only, and `vault:manage` belongs to Vault
administrators (needs the mcp-gateway product), so the write gets a new scope,
`users:manage`. Like `code:review` it is bounded by the account, not by IDs: it
is in the default OAuth scopes but consent and PAT minting offer it only to
admins, and every call re-checks the live account. Existing connections must
reconnect to get it.

Both tools re-check the account on every call (a removed admin or reviewer flag
ends access at once), are hidden from the catalog for everyone else, and record
every call in the Code review audit log (`read_token_usage`,
`set_token_limits` with the target and values, and the token ID), which
`get_code_audit` shows. Only id, username, email, limits and usage are returned.

Test: `TestTokenLimitToolsAdminSetsReviewerReadsOthersRefused`.

## Left

- Not deployed.

## Per-account limits (2026-10-07)

Both tools take `account` (a provider such as `codex-cli`) for the per-account
limits of [PLAT-693](plat-693.md): `get_token_usage` adds each person's
`accounts` and the `account_defaults`; `set_token_limits` with `account` and a
person sets their override on that account (`account_token_limits` through the
same admin handler), with `account` and no person the account's default for
everyone (through `PATCH /api/provider-connections/global:<provider>`).
Audited with the account in the target.
