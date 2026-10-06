[← platform / coding-agent-bridge](index.md)

# PLAT-314 — Cursor live-input false 409 from empty composer footer matching

| Field | Value |
|---|---|
| State | deployed |
| Priority | - |
| Product | coding-agents |
| Area | bridge |
| Summary | A Cursor live-input submission reported “input remained in the prompt after submit retry” even when the composer showed the empty Add a follow-up placeholder. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | Implemented and deployed to RTS; verification scope below |
| Last synchronized | 2026-09-12 |
| Implementation commits | provider 570ede6 |

## Delivered behavior and verification

A Cursor live-input submission reported “input remained in the prompt after submit retry” even when the composer showed the empty Add a follow-up placeholder. A short reply such as 1 could match digits in the model/status/runtime-path footer after the final prompt arrow.

Draft detection now recognizes the empty follow-up/message composer before scanning for submitted text. Actual typed drafts still use the existing recovery logic.

Validation: observed-pane regression reproduced the failure before the change. Tests cover short footer-matching inputs and assert zero recovery keys for an empty composer; existing typed-draft/busy-follow-up checks pass. Provider commit is deployed on RTS. The affected user message was not automatically resent, and no claim is made that its original delivery succeeded. Related mechanism in another provider: [PLAT-273](plat-273.md).

## Deployment receipt

Included in RTS app release `bb7ac6d17ec43750e74a5c92d73ef067f66c69bf`
(`bb7ac6d-20260912135437`), with provider `570ede69fb85beef251ddca9792a2e5ad0dfe95d`.
All three services were active and the public health endpoint was healthy after deployment.
Deployment health is distinct from the feature-specific acceptance scope above.
