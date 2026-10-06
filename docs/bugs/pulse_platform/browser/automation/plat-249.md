[← browser / automation](index.md)

# PLAT-249 — `auto` browser mode omits the built-in host Downloads grant

| Field | Value |
|---|---|
| State | closed |
| Priority | - |
| Product | browser |
| Area | automation |
| Summary | filesystem capability, severity medium. |

| Coordination | Value |
|---|---|
| Assigned agent | Unassigned |
| Ticket state | `fixed` |
| Last synchronized | `2026-08-29` |

- **Priority:** filesystem capability, severity medium.
- **Evidence:** The `jobsearch` manifest used `browser_mode: "auto"` with CDP
  port 9222, but its live shell guard omitted `/Users/mipl/Downloads` entirely.
  The agent then incorrectly substituted legacy workspace `Downloads/`.

## Problem

Three wiring gaps removed the intended grant from the main Builder session. The
host Downloads helper accepted only the literal mode `cdp`; manifests commonly
store `browser_mode: "auto"` with a configured CDP port, but the resolver
returned `auto` before checking that port. In addition, the main Workflow
Builder initialized its Folder Guard without calling the host Downloads grant
helper even though child/background workshop sessions did call it. Finally,
the coding-agent conversation restore path rebuilt the guard independently and
passed raw `auto` to the CDP-only helper, so restarting and continuing the same
Builder conversation still lost the exception.

The failed command targeted `workspace-docs/Downloads`, which is a different,
legacy/general-chat staging folder. Workflow runs use their run-scoped
`execution/Downloads`; host Downloads is a deliberate CDP-only exception.

## Resolution

The host Downloads mode resolver now treats `auto` as unresolved intent. When
a CDP port is configured it resolves to `cdp`, causing the existing session
helper to add the user's host Downloads directory to both `ReadPaths` and
`WritePaths`. The main Builder now applies the same helper as its child
sessions, and the normal/restored coding-agent server paths resolve browser
intent before calling it.

Workflow Builder sessions do not receive root `workspace-docs/Downloads`
access. Workflow-generated files stay in the workflow or its run-scoped
`execution/Downloads`. By owner decision, CDP-enabled workflows now receive
read-write access to the host Downloads directory by default so shell and
browser automation can stage and retrieve the same files. Non-browser and
headless workflows receive no such host grant.

## Verification

- A focused regression test reproduces `browser_mode: "auto"` plus CDP port
  9222, requires it to resolve to `cdp`, and verifies the resulting Builder
  guard contains the host path in both reads and writes with no stale write
  denial.
- Server-mode coverage requires restored `auto` sessions with configured CDP
  candidates to resolve the host Downloads exception to `cdp`, while headless
  sessions remain headless.
- A companion test proves `auto` without a CDP port does not broaden access.
- Focused workflow-agent tests pass.
