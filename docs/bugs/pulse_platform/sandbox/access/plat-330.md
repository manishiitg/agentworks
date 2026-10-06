[← sandbox / access](index.md)

# PLAT-330 — Read-only workflow users can leave durable suggestions for owners

| Field | Value |
|---|---|
| State | open |
| Priority | - |
| Product | sandbox |
| Area | access |
| Summary | tracks the bounded suggestion tool for Run/read-only workflow users. |

| Coordination | Value |
|---|---|
| State | Implemented and regression-tested locally; deployment and live acceptance pending |
| Date | 2026-09-18 |
| Owner | security-sandbox |
| Related subsystem | frontend-chat (existing human decisions panel) |

## Problem and authorized behavior

Run mode, read-only workflow users and workflow bot users cannot edit workflow design, but need to leave requested improvements for owners. Do not grant editing access or duplicate the existing decision store and UI.

`submit_workflow_suggestion` captures the active workflow at registration and accepts suggestion text, an optional reason and an optional step ID. It rechecks authenticated workflow visibility and workflow allowlists on every invocation. Bot route claims remain restricted to their workflow; the external actor is attributed separately from the bot execution principal.

Suggestions append to the existing workflow SQLite human decision records with source `user_suggestion`. The existing panel labels them as User suggestion. A new suggestion cannot replace a previous suggestion. Other creation APIs cannot forge suggestion records by setting their source.

Only a current workflow owner can answer, dismiss or consume a suggestion; bot route sessions cannot approve. Acceptance is a review decision, not unattended implementation authority. The record carries `no_change`; scheduled decision preflight explicitly leaves user suggestions for manual handling. Implementation needs a bounded owner request in Builder.

## Contract compatibility

- Product YAML declares the capability and tool in Builder and Run; Markdown prompts explain submission and review.
- This is a bounded append-only permission exception, not general human-input creation or workflow mutation permission.
- Existing human decisions, their approval/apply contracts and reviewer identities remain unchanged. User suggestion is an intake source, not a new Pulse reviewer module.
- Shared bot credential, MCP management and workflow sharing permissions are unchanged.
- Crew projects retain their existing private project permissions; this feature targets AgentWorks workflows.

## Evidence and acceptance

`workflow_suggestion_tool_test.go` covers authenticated reader submission, anonymous/unshared rejection, empty text, durable attribution, owner review, reader approval/dismissal/consumption rejection, overwrite rejection, absence of unattended decision turns and bot cross-workflow rejection. Actual Builder/Run product surface E2E and focused human-input tests pass. Frontend TypeScript passes.

Live acceptance after deployment: a reader submits from testing; an owner sees the suggestion after reload, accepts or declines it, and no workflow changes or automatic fixer run occur. Repeat from a routed Slack thread. Do not mark this deployed based on service health alone.

Related: [PLAT-262](plat-262.md), [PLAT-317](plat-317.md), [PLAT-278](../../app/navigation/plat-278.md), [PLAT-326](../../goals/pulse/findings/plat-326.md).

## Register notes

[PLAT-330](plat-330.md) tracks the bounded suggestion tool for Run/read-only workflow users. The [PLAT-262 follow-up](plat-262.md) fixes permission fingerprint checks before retained CLI delivery; [PLAT-324](../../crew/chat-reliability/plat-324.md) records same-chat continuity and [PLAT-102](../../coding-agents/sessions/plat-102.md) records the fast-path compatibility boundary. These changes are tested locally; deployment and live acceptance are pending.
