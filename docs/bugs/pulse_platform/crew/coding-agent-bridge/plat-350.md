[← crew / coding-agent-bridge](index.md)

# PLAT-350 — Crew identity is role and purpose; the retained-session fingerprint tracks it, not skills

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | crew |
| Area | coding-agent-bridge |
| Summary | makes Crew role and purpose required and user-owned, removes the user-editable instructions field (behavior stays in the system-owned product template), and renders the project description into the prompt as `Purpose:`. |

| Field | Value |
|---|---|
| Status | `implemented locally; deployment and live acceptance pending` |
| Priority | P1 Crew correctness |
| Owner | server agent runtime (runner-up: frontend-chat for the identity panel) |
| Reported | 2026-09-22 |
| Related | (none) |

## Problem

Three defects in how a Crew's identity reaches its agent:

1. Role and instructions were optional and purpose (the project
   description) never reached the system prompt at all, so Crews ran
   with no defined role or mission.
2. The retained native-session fingerprint (`agentProfileSessionKey`)
   ignored identity. A resumed native CLI keeps its launch-time system
   prompt, so role/purpose edits never reached an already-running
   retained session.
3. Skills were fingerprinted by full content, so every skill edit
   relaunched retained native sessions — pointless, because skills are
   workspace markdown the agent re-reads from disk on demand
   (`loadOneAttachable` + `AttachSkill` run per query).

## Fix

- Users own role and purpose only. The user-editable instructions field
  is removed from the `set_work_identity` tool, the identity panel, and
  the prompt; behavior rules live in the system-owned product template.
  Legacy stored instructions go inert and are dropped on next save.
- `WORK_IDENTITY` renders `Purpose:` (from `product.json` description)
  above icon/name/role. Role and purpose are required: the first-chat
  gate asks for both, and the setup banner opens Identity and sends the
  setup question through the shared pane-to-chat queue.
- The fingerprint is now definition + MCP server selection + Crew role
  and purpose (`WORK_IDENTITY_KEY`). Skills are out: editing, adding,
  or removing a skill no longer relaunches retained sessions, while
  identity edits do. Icon/name edits stay out of the key so cosmetic
  changes don't churn sessions.

Commits: `83fb4708f` (purpose into prompt), `cbb9b4a61` (identity
refactor + fingerprint), `c3b7aecd7` (banner via shared queue).
Deploy relaunches existing retained sessions once against the new key
format.

## Verification

- `go test ./internal/workproduct/` green, including the reloaded
  `WORK_IDENTITY`/`WORK_IDENTITY_KEY` assertions.
- `TestAgentProfileSessionKeyTracksDefinitionAndIdentity`,
  `TestAgentProfileSessionKeyTracksMCPSelection`,
  `TestResolveAgentProfileUsesSavedMCPScopeBeforeRetainedDelivery` pass.
- `tsc -b`, eslint, and 50 frontend vitest cases pass.
- Full `cmd/server` suite shows only the 4 pre-existing failures,
  byte-identical on the pristine tree.
- Live acceptance pending: edit a Crew's role mid-session and confirm
  the retained native session relaunches with the new identity, and
  edit a skill and confirm it does not.

## Register notes

[PLAT-350](plat-350.md) makes Crew role and
purpose required and user-owned, removes the user-editable instructions field
(behavior stays in the system-owned product template), and renders the project
description into the prompt as `Purpose:`. The retained native-session
fingerprint is now definition + MCP servers + role/purpose, so identity edits
relaunch retained sessions; skills are out of the key because the agent
re-reads them from disk. The setup banner opens Identity and asks for both
via the shared pane-to-chat queue. Implemented locally; live acceptance
pending.
