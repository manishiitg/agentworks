[← platform / frontend-chat](index.md)

# PLAT-109 — switching workflows does not reliably restore and select the workflow's Chat

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | platform |
| Area | frontend-chat |
| Summary | Selecting a different workflow does not reliably open that workflow's normal |

| Field | Value |
|---|---|
| Status | `root cause confirmed; PLAT-348 fix implemented locally, deployment pending` |
| Priority | P1 |
| Owner | frontend workflow switch, chat-index hydration, and tab selection |
| Reported | 2026-08-15 |
| Related | [PLAT-026](plat-026.md), [PLAT-095](../scheduler-runs/plat-095.md)), [PLAT-106](plat-106.md), [PLAT-107](plat-107.md), [PLAT-348](../performance/plat-348.md) |

## Problem

Selecting a different workflow does not reliably open that workflow's normal
Chat. The UI can instead show an empty `No chats yet` state, retain a tab or
terminal selection from the previous workflow, or focus a running Schedule or
runtime child. Refreshing the page then reveals the workflow's existing chats,
proving that the durable chat history existed and the empty state was false.

The user-visible contract is simple: switching to a workflow must immediately
show that workflow's canonical interactive Chat in Formatted mode. Schedule and
background-runtime tabs may remain visible alongside it, but they must not
replace the default Chat selection merely because they are active or arrived
first during hydration.

## Why this is a separate ticket

- PLAT-106 prevents events owned by Schedule session A from rendering in Chat
  session B.
- PLAT-107 prevents a sequential main turn or event-only node from becoming a
  phantom child terminal and stealing the pane.
- This issue occurs one level earlier: after the workflow identity changes, the
  frontend does not atomically restore the destination workflow's chat list and
  select its canonical Chat.

Those protections can all be correct while the workflow switch still renders
an empty or runtime-selected state.

## Suspected boundary — verify before changing code

The likely race spans `WorkflowLayout`, `WorkflowChatTabs`,
`useResumePreviousChat`, and `useChatStore`:

1. the selected workflow/preset changes;
2. old workflow tabs and selection are cleared or filtered;
3. runtime projection may publish Schedule/child tabs immediately;
4. durable chat-index/history hydration completes asynchronously;
5. selection is not recomputed when the destination Chat arrives, or an older
   request is allowed to write after the workflow has changed again.

Do not treat this as a confirmed root cause until a trace records workflow ID,
request generation, restored chat IDs, projected runtime tabs, and selected tab
at each boundary. The fact that browser refresh repairs the view strongly
indicates a client hydration/selection race rather than missing persisted data.

## Required repair

1. Introduce one workflow-switch transaction keyed by workflow ID and a
   monotonically increasing request generation.
2. Load or reuse that workflow's durable Chat index, resolve its canonical
   interactive Chat, and commit tabs plus selection together. A stale response
   for the previously selected workflow must be discarded.
3. Keep Chat and Schedule as independent tabs. Runtime projection may add or
   update Schedule/background tabs but may not override the destination
   workflow's initial Chat selection.
4. Show `No chats yet` only after the current workflow's chat-index request has
   completed successfully and returned no chats. Never show it as an
   intermediate loading fallback.
5. Open restored interactive Chat in Formatted mode by default. Raw terminal
   mode remains an explicit user choice, not a side effect of restore timing.
6. Use one selection function for workflow selector, global monitor navigation,
   browser refresh, and back/forward restoration so the same workflow cannot
   open differently depending on entry point.

## P0 regression coverage

The frontend integration test must use deferred responses so it proves ordering
rather than a synchronous happy path:

1. Workflow A has a running Schedule and an existing Chat; workflow B has an
   existing Chat.
2. Open A, then switch to B before A's delayed history response returns.
3. B immediately settles on B's Chat in Formatted mode; A's late response
   cannot replace its tabs, events, or selection.
4. Switch back to A. A's Chat is selected while its Schedule remains visible as
   a separate tab.
5. A workflow with genuinely no chats shows an explicit loading state first and
   `No chats yet` only after the empty response completes.
6. Repeat through global-monitor navigation and direct workflow selection; both
   produce the same tabs and selected Chat without a page refresh.

## Acceptance

1. Switching to any workflow with durable chats selects one of that workflow's
   interactive Chat tabs without refresh.
2. No tab, event, terminal, or delayed request from the previous workflow is
   visible after the switch commits.
3. A running Schedule stays visible beside Chat but never steals initial focus.
4. Formatted mode is the default for a restored Chat.
5. `No chats yet` is truthful and never flashes while history is loading.
6. The deferred-response integration test and a live multi-workflow switch both
   pass.

## 2026-09-21 confirmed production cause and repair

RTS live evidence replaced the earlier unverified race hypothesis with a
specific fan-out. The server returned 45 retained sessions, all completed; 43
were workflow sessions and 42 belonged to `rtsprreviweer`. Those rows are kept
for 24-hour conversation/terminal continuity, but workflow open interpreted
them as live runtime work and performed per-session resolution and hydration.
The underlying chat, dashboard and Pulse endpoints completed in 1–55 ms.

[PLAT-348](../performance/plat-348.md) records the performance incident and
implementation. Reconnect now filters through the canonical live-activity rule
before per-session work, terminal retained rows cannot set restored tabs back
to streaming, and workflow-keyed loading state prevents the new-chat guide or
the previous workflow transcript from rendering while durable selection is
unresolved. Focused tests and the TypeScript build pass. Deployment and live
rapid-switch acceptance remain pending.
