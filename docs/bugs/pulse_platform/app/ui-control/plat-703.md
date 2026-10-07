[← app / ui-control](index.md)

# PLAT-703: View-tool actions wake the tab through the live feed

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P3 |
| Product | app |
| Area | ui-control |
| Summary | A queued right-panel view action also wakes the session owner's tabs over `/api/live`, so a tab whose chat SSE dropped syncs at once instead of after the 5-minute lease renewal. |

## What happened

`perform_ui_action` queues the action in the ui-control broker and wakes the
browser with a `workflow.ui-action` presentation event on the chat's SSE. When
that stream was down (dropped connection, reconnecting tab), nothing woke the
pane until its 5-minute backup sync or a visibility change, and the agent got
`expired`/`timeout` after 10 seconds.

## Fix

- Server: a fresh accepted action also publishes a live-feed notice
  `{"kind":"ui_control","session":"<id>"}` (`publishUIControlWake`,
  `livefeed.PublishToUser`). The notice is addressed to the session's owner;
  `/api/live` drops it for every other user (`liveFeedNoticeForUser`). It
  carries no action data; claim and ack are unchanged.
- Client: `useWorkspaceUIControl` subscribes to `ui_control` and syncs when
  the notice names its session (a resync makes a bound tab sync too). While
  bound and the live feed is not live, the backup poll runs every 60s instead
  of 5 minutes.
- Tests: `TestPerformUIActionWakesOwnerThroughLiveFeed` (server publishes,
  owner-only, no data) and the vitest case in
  `useWorkspaceUIControl.backoff.test.tsx` (a notice for its session syncs,
  another session's does not).

## Left

- Not verified live (no deploy in this change).
