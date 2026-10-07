[← vault / secrets](index.md)

# PLAT-658: Rotated secrets ignored by a live chat CLI

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | vault |
| Area | secrets |
| Summary | A rotated workflow secret was not picked up while the chat's coding CLI stayed alive: messages were typed into the old process |

## What happened

## Fix

## Left

## What happened

Excellence, 2026-10-07, workflow `briancandidatesourcingpipeline` (Vaibhav): the Unipile DSN, API key and LinkedIn account ID were rotated in the UI at 09:33-09:36 (all three saves reached the shared workflow secret store). The agent kept reading the old values. It has come back several times before.

Cause: the workflow chat ran on Muse in a retained tmux session launched at 07:11. Each following message ("try again", "no i rotated the key…") was delivered as live input into that process (`delivery_source=mcpagent_session`, `sent_to_cli`), which keeps the environment it was launched with. Turn setup, where a changed secret value relaunches the CLI (`CodingAgentScopeFingerprint`), never ran.

## Fix

Saving or deleting a shared workflow secret bumps that workflow's secret version; turn setup records which workflow's secrets a session was launched with. Live delivery to a session whose workflow secrets changed since is refused, so the message starts a normal turn, and the CLI relaunches with the current values.

Workaround before the deploy: press Stop in the chat, then send again.

## Left

- Vault/global secret changes do not bump a version yet (only workflow/project secrets).
- Versions live in memory: a server restart relaunches CLIs anyway.
