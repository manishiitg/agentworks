[← platform / coding-agent-bridge](index.md)

# PLAT-313 — Preserve intermediate CLI agent messages in real-time chat

| Field | Value |
|---|---|
| State | deployed |
| Priority | - |
| Product | coding-agents |
| Area | bridge |
| Summary | Intermediate assistant commentary shown in CLI terminals was missing from the live chat while work continued. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | Implemented and deployed to RTS; verification scope below |
| Last synchronized | 2026-09-12 |
| Implementation commits | app 78f48f76c; provider ddc21f2 |

## Delivered behavior and verification

Intermediate assistant commentary shown in CLI terminals was missing from the live chat while work continued. The issue was not limited to reload/history restoration.

Provider retained-turn progress streaming and app transcript/event handling now preserve intermediate messages across CLI providers. Chunk updates, durable event storage and restored transcript handling were updated together to avoid collapsing distinct messages into the final response.

Evidence: provider progress commit and app transcript_message, chat_history_chunk_collapse and transcriptChunkUpdates regression tests. Included in the verified RTS release. The user reported the original Claude Code live-stream case; an exhaustive fresh live-account acceptance run for every CLI is not claimed. This does not close unrelated completion/timeout lifecycle issues in [PLAT-179](plat-179.md) or [PLAT-116](plat-116.md).

## Deployment receipt

Included in RTS app release `bb7ac6d17ec43750e74a5c92d73ef067f66c69bf`
(`bb7ac6d-20260912135437`), with provider `570ede69fb85beef251ddca9792a2e5ad0dfe95d`.
All three services were active and the public health endpoint was healthy after deployment.
Deployment health is distinct from the feature-specific acceptance scope above.
