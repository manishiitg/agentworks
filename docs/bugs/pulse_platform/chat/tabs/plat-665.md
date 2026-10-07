[← chat / tabs](index.md)

# PLAT-665: New chat button needs three clicks

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | chat |
| Area | tabs |
| Summary | The + new-chat button first reloads an old chat, then the current one, before opening a new chat: three clicks |

## What happened

Confida agent log, 2026-10-06 13:55-13:56 CEST (workflow chat of saurabh.khatri): click 1 stopped the
current chat `pat-2ecd...-d6a714c0`, the tab got a fresh session, and within the same second the pane
subscribed to an older chat `pat-bea978...-7340dfd3` (SSE `since=5099`, so a tab for it was already in
the browser). Click 2 stopped that one and the pane went back to `pat-2ecd...` (`since=17742`). Click 3
finally stayed on a fresh chat.

Cause: the workflow chat strip keeps one interactive Chat per workflow
(`selectWorkflowTabsForStrip`) and sorted blank Builder tabs last, before it looked at which tab was
active. New chat leaves the active tab blank on purpose, so any hidden Builder tab of the same workflow
that still had events won the strip, and the "migrate the old Workshop + Chat pair" effect in
`WorkflowChatTabs` then activated that hidden tab. Each click blanked one more tab, so it took as many
clicks as there were Builder tabs with content.

## Fix

- `frontend/src/components/workflow/workflowTabStripSelection.ts`: the active Chat always wins; blank
  and recency only order the other tabs.
- `frontend/src/components/workflow/WorkflowChatTabs.tsx`: removed the effect that moved focus to a
  hidden duplicate (it can no longer fire, and it is what jumped to the old chat).
- Test: `workflowTabStripSelection.test.ts` "keeps a blank active Chat after New chat instead of an
  older chat with events".

## Left

- Not tried live in the browser; check on Confida after the next deploy (one click on + gives an empty
  chat; the old chats stay in Previous chats).

## Report

#agent_works, 2026-10-06 17:25 (Confida, workflow confida-qa-testing): pressing + to start a new chat reloaded an old chat, then the current chat, and only the third click started a new chat.
