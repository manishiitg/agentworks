[← integrations / integrations](index.md)

# PLAT-300 — Gmail send-only default and Google Workspace (Drive/Sheets/Docs/Slides/Calendar) connections

| Field | Value |
|---|---|
| State | deployed |
| Priority | P2 |
| Product | integrations |
| Area | integrations |
| Summary | makes Gmail send-only the default (with an opt-in read toggle) and fixes the identity/status gap that left a send-only connection unable to name itself or report as authenticated. |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | Implemented and deployed (confida) — 2026-09-20 follow-up implemented locally, tests pass, not yet pushed/deployed |
| Last synchronized | 2026-09-20 |
| Priority | P2 platform capability |

## Problem

The Gmail channel requested `gmail.readonly` by default even though every
existing use (Pulse and workflow `notify_user`) only ever sends, and a
send-only login could not report itself as authenticated or name its own
address — `gmail.users.getProfile` needs a read scope, so a send-only token
showed "Address not known yet" and, on the gog backend, read as needing
reconnect. Separately, whichever CLI backend actually serves a deployment
(`gws` or `gog`) was hardcoded as "gws" in the settings UI, so a gog-backed
deployment (gog is the only backend that supports anything beyond Gmail) saw
a permanently wrong "gws not installed" status. Beyond Gmail, there was no way
for a connection to authorize Drive, Sheets, Docs, Slides, or Calendar, and no
agent-facing way to use them even if there were.

## Contract and implementation

- **Send-only default, opt-in read.** `GmailConnection.AllowReadAccess`
  (default false) requests `gmail.send` + `userinfo.email` only; checking
  "Also allow reading this mailbox" in the UI adds `gmail.readonly`. Fixed at
  consent time, like every scope grant here — widening it means reconnecting.
- **Identity for a send-only token.** Both backends now resolve identity via
  Google's `tokeninfo` endpoint (`googleTokenInfo`) instead of
  `gmail.users.getProfile`: tokeninfo returns the granted scopes and, when
  `userinfo.email` was granted (always), the address — and it works
  regardless of read access. `computeAuthStatusGog` and `computeAuthStatus`'s
  server-managed-token branch both use it, with `getProfile` kept only as a
  fallback (tokeninfo unreachable, or the `--account`/`--client` path with no
  raw token to introspect).
- **Backend-aware UI labels.** `GmailAuthStatus.Backend` ("gws"/"gog") is set
  by whichever path computed the status; the settings UI reads it instead of
  hardcoding "gws" / `@googleworkspace/cli` in the Connection card and the
  "no account connected" banner.
- **Google Workspace service grants.** `GmailConnection.Services
  []GoogleServiceGrant` (`{service, write}`) lets a connection additionally
  request Drive, Sheets, Docs, Slides, or Calendar, each independently
  read-only (default) or read+write. The scope catalog
  (`services.GoogleServiceCatalog()`) is the single source of the OAuth scope
  URIs per service/level; a new `GET /api/human-feedback/gmail/service-catalog`
  endpoint keeps the UI's checkbox list from drifting from it. Set on create
  only — like `AllowReadAccess`, changing it means removing and re-adding the
  connection.
- **`google_workspace_cli` agent tool.** Rather than a bespoke tool per
  Drive/Sheets/etc. operation, the agent gets one tool that passes its chosen
  `gog` arguments straight through (`services.RunGoogleCLI`). The server
  resolves the target connection (default or named), injects its access
  token, rejects `--access-token`/`--account`/`--client`/`--home` if the
  caller tries to set them, and appends `--readonly` whenever the connection's
  grant for that service is read-only — enforcement is gogcli's own flag, not
  argument sniffing. The token never appears in tool-call arguments or the
  agent's shell. Independent of `GmailConfig.UseGogBackend`: that flag only
  picks which backend serves Gmail send/status, but Drive/Sheets/etc. have no
  `gws` equivalent at all, so this tool always resolves to `gog`.

## Verification

- [x] `go test ./agent_go/cmd/server/...` passes (server, services,
      virtual-tools packages), including new
      `TestComputeAuthStatusGogSendOnlyTokenIsAuthenticatedAndNamedViaTokenInfo`
      and `TestComputeAuthStatusGogReadOnlyTokenIsAuthenticatedButLacksSendScope`.
- [x] `go build ./agent_go/...` and frontend `tsc --noEmit` both clean.
- [x] Deployed to confida (`confida-c36503a-...`); `/api/health` reports
      healthy post-deploy.
- [x] confida's `gmail-config.json` confirmed to carry `use_gog_backend: true`
      across a redeploy (data is separate from the release).
- [ ] A live connection has not yet been created with a Drive/Sheets/etc.
      grant, so `google_workspace_cli` has not been exercised end-to-end
      against a real Google account.
- [ ] Existing pre-feature connections (e.g. confida's
      `manish.prakash@excellencetechnologies.com`) still need reconnecting to
      pick up any new service grants — expected, not a bug, matching how
      `AllowReadAccess` already behaves.

## Deployment

Shipped as part of confida's `agent_go` + frontend bundle
(`deploy/cf/deploy-cf.sh`, remote-main mode). Not evaluated
against RTS or Dominion — those deployments are managed separately.

## Follow-up (2026-09-20): imported gog accounts report empty scopes

After an "update permission" re-OAuth, Google shows success ("Connected as
manish.prakash@excellencestechnologies.com — you can close this tab") but
the app reports every permission revoked and the connection not Ready.

Evidence: with the backend's `GOG_HOME`, `gog auth list --check --json`
shows the imported account `valid: true` with `scopes: []`. The credential
is live; only gog's scope metadata is empty. Root cause: refresh tokens
imported from our own OAuth flow (`ImportRefreshTokenIntoGog`) carry no
scope metadata in gog's store, and the status path reads the empty list as
"gmail.send missing" (`computeAuthStatusGog` checked-account branch), which
clears `HasGmailSendScope` and the Ready flag. The completion path also
overwrites the connection's stored scopes with that empty observation.

Fix (implemented 2026-09-20, not yet pushed/deployed):
`CompleteGmailOAuth` returns the consent-time `tokeninfo` scopes, the
callback route threads them into `CompleteGogConnection`, which records
them via `resolveCompletionScopes` (granted > gog metadata > keep stored,
never wipe with empty); `computeAuthStatusGog`'s checked-account branch
falls back to the connection's stored scopes when gog reports empty on a
valid account. Regression tests:
`TestGogStatusFallsBackToStoredScopesWhenGogReportsNone` (failed before,
passes after),
`TestResolveCompletionScopesNeverWipesStoredScopes`, and a granted-scopes
assertion in `TestOAuthCallbackStoresOnlyInGog`. `go test
./cmd/server/services/` and `go test ./cmd/server/` both pass.

## Decision history

- 2026-09-08: scope truth read live at status time (`tokeninfo`, then
  `getProfile` fallback, then gog `--check` metadata). Belief: gog's
  account record carries granted scopes.
- 2026-09-20: supersedes the gog-metadata belief for imported accounts —
  `gog auth list --check` returns `valid: true` with `scopes: []` for
  externally imported refresh tokens, so consent-time `tokeninfo` scopes
  persisted on the connection become the scope source of truth, with live
  gog metadata as a non-empty-only refinement. This continues the running
  gog reliability thread (backend naming, send-only identity, shared
  store/`GOG_HOME` in PLAT-312): gog owns the tokens, but our status and
  scope reporting cannot trust its metadata alone.

## Register notes

[PLAT-300](plat-300.md) makes Gmail send-only the default (with
an opt-in read toggle) and fixes the identity/status gap that left a
send-only connection unable to name itself or report as authenticated. Adds
per-service Drive/Sheets/Docs/Slides/Calendar grants (read-only default,
opt-in write) to a connection, plus a `google_workspace_cli` agent tool that
runs `gog` directly under the resolved connection's credential rather than a
bespoke tool per operation. Implemented and deployed to confida; no live
Drive/Sheets connection exercised yet. 2026-09-20 follow-up implemented
locally (not yet pushed/deployed): imported gog accounts report
`valid: true` with empty scopes, so post-re-OAuth status read as fully
revoked; fix is consent-time `tokeninfo` scope persist plus
stored-scopes fallback, with regression tests passing.
