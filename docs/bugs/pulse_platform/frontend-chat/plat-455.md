[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-455 — One settled "go to the bottom" per chat switch; no scroll jumping or blinking

| Coordination | Value |
|---|---|
| State | fixed on `main`; not deployed; not looked at in a browser |
| Date | 2026-10-04 |
| Owner | frontend-chat |

## Problem

After Cmd+K (workflow or chat switch) or "Apply in chat", the chat tore the old
transcript down and rebuilt the new one in ~20 small Virtuoso batches over ~0.3 s
while the app fired `chat-scroll-to-bottom` up to ~6 times (QuickSwitcher and
`workflowSessionRestore` each +0/120/400 ms, `workAutomationRunRestore`,
`workspacePaneChat` +50 ms). ChatArea answered each with three more scrolls
(now, +80, +350 ms) and no manual-scroll guard, plus the tab-change effect's two
timers: visible jumping and blinking against a list still being measured, and a
reader's position destroyed.

## Change

- `utils/chatScrollRequest.ts`: `requestChatScrollToBottom()` (auto-scroll on, ONE
  event, no timers) is the only dispatcher; `SettledScroll` is the one owner of the
  scroll itself.
- ChatArea: the event and the tab-change effect both call
  `SettledScroll.request`. Requests coalesce; the scroll (`setAutoScroll(true)` +
  instant `scrollToBottom`) runs once the chat content has been quiet for 120 ms
  (a MutationObserver on its subtree and a ResizeObserver report activity), or 800 ms
  after the first request at the latest. A manual scroll since the request
  (`manualScrollVersionRef`, or an upward wheel / touchmove on the chat content
  inside the window) drops it; a newer explicit request is a new intent.
- QuickSwitcher, `workflowSessionRestore`, `workAutomationRunRestore` use the helper;
  their timed repeats are gone.
- `sendWorkspacePaneMessageToChat`: a message queued into the chat already on screen
  keeps the reader's position when they are scrolled up (`autoScroll` off or
  `transcriptIsFollowing(tabId)` false); into another chat, or one at the bottom, it
  sets auto-scroll and requests the bottom once. The queued message's own send
  still follows the latest, as any send does.

## Tests

`utils/chatScrollRequest.test.ts` (one scroll after settle, coalescing, cap, manual
cancel, helper dispatches once, pane-message intent three ways) and
`components/QuickSwitcher.scrollRequest.test.tsx` (one event, no timed repeats).

## To look at (browser)

Cmd+K between two long workflows and two chats: the new chat should appear once and
land at the bottom without a visible jump. Scroll up right after switching: it must
not be yanked back. "Apply in chat" from a pane while reading the chat higher up: the
position stays until the message sends.

## Left

- In the Virtuoso transcript the outer container is `overflow-hidden`, so ChatArea's
  `scrollToBottom` is a no-op there; the transcript follows through its own saved
  state and `followTranscriptLatest` (sends). A switch into a chat whose saved state
  is "reading" still restores that position; this ticket does not change that.
- Direct `scrollToBottom` calls in ChatArea (send path, new-event auto-scroll) are
  unchanged.
