[← platform / security-sandbox](index.md)

# PLAT-395 — Chats read other workflows only when attached

| Field | Value |
|---|---|
| State | deployed |
| Priority | - |
| Product | sandbox |
| Area | security |
| Summary | fixed on `main`, not deployed: the blanket read grant on `Workflow/` is gone; a chat reads its own workflow and attached ones (bridge tools and the CLI sandbox alike). |

| Coordination | Value |
|---|---|
| State | fixed on `main`; not deployed |
| Date | 2026-10-03 |
| Owner | security-sandbox |
| Related | PLAT-394 (Seatbelt on a Mac), PLAT-364 |

## Found

The owner's sandbox self-test in a salesoutreach Builder chat (Codex, Native
agent tools on) read another workflow's `workflow.json`. Every chat's folder
guard granted read on the whole `Workflow/` tree (`server.go`, `delegation.go`,
and the bridge shell in `tool_setup.go`); Seatbelt and Landlock enforced that
grant as written.

## Decision (owner, 2026-10-03)

A chat reads its own workflow and the workflows attached to it (`#workflow`
mentions, Builder attachments), nothing else under `Workflow/`.

## Done

- `Workflow/` removed from the three read-grant lists; attached workflows still
  arrive as read-only folders, the chat's own workflow as its write grant.
- Generic chat's prompt no longer says `ls Workflow/`; it asks the user to
  attach the workflow.

## Found again in owner testing (2026-10-04)

- A Codex Builder chat still listed all of `Workflow/`: workflow Builder chats
  took their read root from `tokenSessionWorkflowReadRoot`, which scoped only
  API-token sessions and gave app sessions the whole tree. It now returns the
  chat's own workflow for every session, and nothing for an unresolved folder.
  The other guard setters (workflow runs, external Builder, Work, project
  delegation) already granted only their own folder.

## Left

- Owner re-runs the sandbox self-test: step 6 should now be refused.

## Register notes

[PLAT-395](plat-395.md), fixed on `main`, not
deployed: the blanket read grant on `Workflow/` is gone; a chat reads its own
workflow and attached ones (bridge tools and the CLI sandbox alike).
