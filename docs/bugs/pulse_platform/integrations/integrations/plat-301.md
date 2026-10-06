[← integrations / integrations](index.md)

# PLAT-301 — MCP catalog name collision can strand a user's custom connector

| Field | Value |
|---|---|
| State | open |
| Priority | P3 |
| Product | integrations |
| Area | integrations |
| Summary | — PR #191 (merged) made the "Add via JSON" editor treat any overlay entry whose name matches one of the 113 base catalog connectors as a connection record, but the unchanged runtime merge (`loadMergedConfig`) still reso… |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | Open — identified during PR review, not yet fixed |
| Last synchronized | 2026-09-08 |
| Priority | P3 edge case (low likelihood, real when it hits) |

## Problem

PR #191 (merged, `71d769413`) expanded the base MCP connector catalog from 46
to 113 servers and, in the same change, made `handleGetMCPConfig` /
`handleSaveMCPConfig` (`mcp_config_routes.go`) treat any overlay
(`mcp_servers_clean_user.json`) entry whose name matches a base catalog name
as a connection record — invisible in the "Add via JSON" editor and rejected
outright if the user tries to save over it ("X is a built-in connector and
cannot be edited here").

That is correct for the common case: an overlay entry with a base name really
is how "this catalog connector is connected" is represented
(`persistOAuthConfig`, `oauth_routes.go:812`). But `loadMergedConfig`
(same file, unchanged by #191) has always resolved a name collision the other
way — **the overlay entry wins over the base entry** at actual connection
time:

```go
// Add base servers first
for name, server := range api.mcpConfig.MCPServers {
    mergedConfig.MCPServers[name] = server
}
// Add user servers (these will override base servers with same name)
for name, server := range userConfig.MCPServers {
    mergedConfig.MCPServers[name] = server
}
```

So a user who already had a genuinely custom server named e.g. `Figma`,
`Dropbox`, `Postman`, `Todoist`, `Zendesk`, `Plain`, or `Close` — all newly
reserved by #191's 67 additions — now hits an inconsistency:

- **Connections keep working.** `loadMergedConfig` still resolves their own
  custom entry (overlay wins), so nothing breaks at runtime today.
- **The editor permanently hides and locks it.** `customServers()` now
  classifies that overlay entry as a base connection record. It disappears
  from `GET`, and any attempt to edit or remove it via `POST` is rejected as
  a reserved name.
- **The new official connector is unreachable for that user.** The base
  catalog's real Figma/Dropbox/etc. entry can never win the merge while the
  colliding overlay entry exists, so #191's whole point — offering that
  connector — silently fails for exactly this user.

Not a regression in #191's own logic (verified correct in isolation during
review — [PR #191](https://github.com/manishiitg/coding-agent-loop/pull/191));
it exposes a latent inconsistency between two pre-existing/new pieces of code
that disagree on precedence, newly reachable because the reserved-name
surface grew 2.4x (46 → 113).

## Likelihood

Requires an *exact* name match between a user's pre-existing custom server
and one of the 67 newly reserved names. Plausible for generic-sounding names
picked before they were reserved (Figma, Dropbox, Postman, Todoist, Zendesk,
Plain, Close); unlikely for anything more idiosyncratic. No telemetry exists
yet to say how many, if any, live installs are affected.

## Proposed fix (not implemented)

Pick one and make `handleGetMCPConfig`/`handleSaveMCPConfig` and
`loadMergedConfig` agree:

1. Make the editor's precedence match the merge's: an overlay entry always
   wins, so a colliding one is treated as the user's own (current behavior
   before #191) — reintroduces the original data-loss bug #191 fixed for the
   catalog-expansion case specifically, so probably not this one.
2. Make the merge's precedence match the editor's: base always wins on a
   name collision, so a stranded custom entry becomes visibly a dead file
   nothing resolves to, at least surfacing rather than silently shadowing.
3. Detect the collision explicitly at config-load time (or on next catalog
   sync) and rename/flag the user's overlay entry rather than leaving it
   ambiguous — most user-friendly, more work.
4. At minimum, log/warn on server startup when an overlay entry's name
   matches a base catalog name AND that overlay entry does not look like a
   plain connection record (e.g. carries fields persistOAuthConfig would not
   set), so an operator can catch it before a user reports "my server
   disappeared."

## Verification

- [ ] Not yet reproduced against a live install — analysis only, from reading
      `mcp_config_routes.go`'s `loadMergedConfig`/`customServers`/
      `handleSaveMCPConfig` together.
- [ ] No fix implemented yet.

## Deployment

N/A — tracking only. No code changed for this ticket.

## Register notes

[PLAT-301](plat-301.md) — PR #191 (merged) made the "Add via
JSON" editor treat any overlay entry whose name matches one of the 113 base
catalog connectors as a connection record, but the unchanged runtime merge
(`loadMergedConfig`) still resolves a same-name collision the other way
(overlay wins). A user whose pre-existing custom server happened to be named
e.g. Figma, Dropbox, or Postman — all newly reserved by #191 — now has that
entry permanently hidden and uneditable in the UI while it keeps running
underneath, and can never reach the new official connector of the same name.
Open, not yet fixed; identified during PR review, not reproduced live.
