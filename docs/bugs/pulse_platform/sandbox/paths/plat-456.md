[← sandbox / paths](index.md)

# PLAT-456 — The raw workspace proxy classified the path string it was given: an absolute or backslash spelling of a protected folder passed its gates

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | sandbox |
| Area | paths |
| Summary | fixed on `main`, not deployed. |

| Coordination | Value |
|---|---|
| State | fixed on main (found and fixed while gating `Crew/<id>`, PLAT-442 step 4), not deployed |
| Priority | P1 |
| Date | 2026-10-04 |
| Owner | security-sandbox |

## Finding

`/api/wp` (the browser's hop to the workspace service) refuses cross-user paths and gates `config/`, `_system/`, `Workflow/<id>` and now `Crew/<id>`
by their FIRST path segment. It classified the string it was given. The workspace service does not: `utils.SanitizeInputPath` strips the docs root off
an absolute path, so `<docs root>/config/users.json` and `<docs root>/Workflow/<private>/planning/plan.json` name the same files as `config/users.json`
and `Workflow/<private>/...` and were not recognised by the gate (their first segment was a segment of the host path). A backslash spelling
(`Crew\<id>\...`) was not recognised by the Crew gate either (`_users\<other>\...` already was, through `workspaceref.Parse`).

Found by the gate tests written first for `Crew/<id>` (`crew_shared_root_gate_test.go`): the backslash rows failed at once; the absolute-path rows were
written to check the neighbours and failed on `config/`, `_system/` and `Workflow/<id>`. Reproduced with the test below on the unchanged gate; it needs the
docs root's path (not secret on a host that shows it in an error or a tool result, but not something a browser normally knows), so this was not shown to be
reachable from a browser by an attacker who does not know it. Not reproduced against a running server.

## Fix

`workspaceProxyCleanPath` (workspace_proxy.go) turns every path argument into the workspace-relative path the service will read (docs root stripped,
`\` as a separator, dots collapsed) and every gate (`workspaceProxyPathIsOtherUser`, `workspaceProxyPolicy.denies`) classifies that. Tests:
`TestSharedCrewRootGate*` (every request shape: URL routes, query parameters, JSON body fields including nested and array ones, multipart, odd spellings),
`TestWorkspaceProxyAbsoluteSpellingsDoNotBypassProtectedFolders`.

## Left

- The candidate document roots are `WORKSPACE_DOCS_PATH`, `WORKSPACE_SHELL_ROOT` and `/app/workspace-docs`. A workspace service whose `docs-dir` is a root the
  agent server does not know (a remote workspace configured differently) still strips its own root; the agent server and the service share the setting on
  every deployment in use.
- See PLAT-457 (links).

## Register notes

[PLAT-456](plat-456.md), P1, fixed on `main`, not deployed. The gates classified the path string; the workspace service strips its
docs root and reads the same file. Every gate now classifies the workspace-relative path.
