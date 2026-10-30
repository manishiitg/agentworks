// resolveChatSurface — the single, pure 3-state resolver for the chat-pane
// surface. Shared by BOTH product-profile chats and the workflow
// render branches in ChatArea so the "which surface shows" decision lives in
// exactly one place.
//
// This is intentionally pure: no React, no store access — just inputs → enum.
// Callers derive the inputs from their own state (the multi-agent and workflow
// branches compute `hasContent` differently, mirroring today's behavior) and
// then map the returned surface to the concrete JSX.

export type ChatSurface = 'restoring' | 'active' | 'landing'

export interface ChatSurfaceInputs {
  /**
   * A previous session is loading/replaying its events
   * (isRestoringChatSessions for multi-agent, isRestoringWorkflowSessions for
   * workflow).
   */
  isRestoring: boolean
  /**
   * Brief settle window for a freshly-resumed tab so an empty resume doesn't
   * flash the previous-chats list before its first event arrives. Feeds the
   * `restoring` state (multi-agent only; workflow passes false).
   */
  resumeSettling: boolean
  /**
   * A live/replayed conversation has content. Multi-agent uses
   * `hasConversationContent` (user/assistant/completion events); workflow uses
   * `displayEvents.length > 0` (its execution events aren't always
   * "conversation" typed). Either way: real content is present.
   */
  hasContent: boolean
  /** A streaming turn is in flight. */
  isStreaming: boolean
  /**
   * A resumed session whose terminal/execution tree already has content
   * (restoredSessionHasExecutionContent and/or a restored-terminal signal).
   * Such a tab is live even before conversation events replay.
   */
  hasRestoredLiveContent: boolean
  /**
   * The tab is viewing a specific read-only run (scheduled or bot run). Such a
   * tab is NEVER a fresh chat, so it must never fall to `landing`: while empty
   * it stays in `restoring` (events still loading), and once content/terminal
   * arrives it is `active`. This fixes the "schedule-bounce" where opening a
   * scheduled run briefly showed the previous-chats panel.
   */
  isReadOnlyRunView: boolean
}

/**
 * Resolve the chat-pane surface with explicit ordered precedence:
 *   restoring → active → landing
 *
 * - `restoring`: nothing to show yet, but a session is replaying, a resume is
 *   settling, or a read-only run view is still fetching → spinner.
 * - `active`: a live or replayed conversation, an in-flight stream, or restored
 *   terminal/execution content → terminal-or-events (the view toggle decides
 *   which, NOT this resolver).
 * - `landing`: a fresh chat / New Chat with no content → previous-chats panel
 *   (which renders its own "none yet" empty when the list is empty).
 */
export function resolveChatSurface(inputs: ChatSurfaceInputs): ChatSurface {
  const {
    isRestoring,
    resumeSettling,
    hasContent,
    isStreaming,
    hasRestoredLiveContent,
    isReadOnlyRunView,
  } = inputs

  // Any signal that real content exists. A resumed session with terminal/
  // execution content counts even before conversation events replay — this is
  // why the active check (below) must win over a still-set restoring flag.
  const hasLiveOrRestoredContent = hasContent || isStreaming || hasRestoredLiveContent

  // 1) restoring — still empty, but a session is replaying, a resume is
  //    settling, or a read-only run view is loading its events.
  if (!hasLiveOrRestoredContent && (isRestoring || resumeSettling || isReadOnlyRunView)) {
    return 'restoring'
  }

  // 2) active — a live or replayed conversation, an in-flight stream, or
  //    restored terminal/execution content.
  if (hasLiveOrRestoredContent) {
    return 'active'
  }

  // 3) landing — fresh chat / New Chat. A read-only run view never reaches here
  //    (empty → restoring above; with content → active above).
  return 'landing'
}

/**
 * Workflow terminal mode has one additional authoritative signal: a retained
 * tmux pane can be live even after streaming ends and without hydrated events.
 */
export function resolveWorkflowChatSurface(
  inputs: ChatSurfaceInputs,
  hasTerminalSurface: boolean,
  selectionMatchesActiveTab = true,
): ChatSurface {
  // Workflow selection changes synchronously, while resolving/restoring its
  // tab is asynchronous. Never render the previous workflow's transcript in
  // that gap; the selected workflow owns the pane immediately.
  if (!selectionMatchesActiveTab) return 'restoring'

  return resolveChatSurface({
    ...inputs,
    hasRestoredLiveContent: inputs.hasRestoredLiveContent || hasTerminalSurface,
  })
}

/**
 * A tab with a session whose first history load has not finished and that has
 * nothing to show yet must show the loading state, never the new-chat landing
 * screen. A tab without a session id is a brand-new chat (landing at once).
 */
export function isHistoryPending(input: { sessionId?: string | null; historyLoaded: boolean; hasEvents: boolean; isStreaming: boolean }): boolean {
  return !!input.sessionId && !input.historyLoaded && !input.hasEvents
}
