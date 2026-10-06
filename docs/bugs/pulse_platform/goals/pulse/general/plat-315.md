[← goals / pulse / general](index.md)

# PLAT-315 — Pulse slash command resolves the open chat workflow

| Field | Value |
|---|---|
| State | deployed |
| Priority | - |
| Product | goals |
| Area | pulse/general |
| Summary | Running /pulse could display “Open a workflow before running Pulse” with a workflow already open because the command read the file browser's activeFolder. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | Implemented and deployed to RTS; verification scope below |
| Last synchronized | 2026-09-12 |
| Implementation commits | bb7ac6d17 |

## Delivered behavior and verification

Running /pulse could display “Open a workflow before running Pulse” with a workflow already open because the command read the file browser's activeFolder.

ChatInput now passes the resolved command workflow path into CommandContext, using the same workflow resolution as command permission checks. /pulse sends that path to the scheduler rather than directly reading the file browser store.

Validation: all 25 command-registry tests passed, including an empty browser selection with an open workflow, a browser folder pointing elsewhere, and no resolved workflow producing no scheduler request. Type checking and production build passed. Existing unrelated ChatInput lint errors/warnings remain. RTS release and public health were verified; a live Pulse was not launched solely for deployment verification.

## Deployment receipt

Included in RTS app release `bb7ac6d17ec43750e74a5c92d73ef067f66c69bf`
(`bb7ac6d-20260912135437`), with provider `570ede69fb85beef251ddca9792a2e5ad0dfe95d`.
All three services were active and the public health endpoint was healthy after deployment.
Deployment health is distinct from the feature-specific acceptance scope above.

## Follow-up — Pulse toggle blocked on missing metrics

The Pulse pane's switch checked goal metrics and sent a setup chat instead of
saving `pulse_enabled` when none existed, leaving the switch off. The toggle now
saves the requested enabled state directly and reports success/failure. Goal
Progress retains its separate setup action; missing metric data remains explicit.
Hook tests cover successful enablement without goal setup and permission/save
errors with saving state released. Implemented locally; release verification
pending. This does not change backend workflow-write permission requirements.

## 2026-09-12 deployment confirmation

The latest follow-up above is included in RTS release `3a37a1c-20260912143119`
(app `3a37a1c75`). Source revisions and all three services were verified after
activation. The configured-environment Linux sandbox regression passed.
Earlier pending-deployment notes are superseded; feature-specific live acceptance
limits remain as documented. No user accounts/sharing or notification recipients
were changed during verification.
