import { createRequestCoalescer } from '../services/requestCoalescer'

export type LiveInputSubmissionCoordinator = <T>(
  sessionKey: string,
  message: string,
  submit: () => Promise<T>,
) => Promise<T>

export interface ChatInputLiveRouteDecision {
  hasSession: boolean
  isCodingAgentProvider: boolean
  isWorkflowMode: boolean
  usesStructuredTransport: boolean
}

export interface ChatTransportDecision {
  isInteractiveWorkflowBuilder: boolean
  reportedTransport: string
  providerUsesStructuredTransport: boolean
}

// Workflow Builder is the one deliberate exception to Cursor's normal
// structured transport. Runtime/profile summaries describe the provider-level
// default and can therefore still say "structured" even while the Builder's
// real process is retained in tmux. The chat-specific contract must win.
export function chatUsesStructuredTransport({
  isInteractiveWorkflowBuilder,
  reportedTransport,
  providerUsesStructuredTransport,
}: ChatTransportDecision): boolean {
  if (isInteractiveWorkflowBuilder) return false
  if (reportedTransport === 'structured') return true
  if (reportedTransport === 'tmux') return false
  return providerUsesStructuredTransport
}

// Product surfaces normally keep their compact chat chrome, but Workflow
// Builder is an inspectable retained tmux session even when it is rendered
// inside the AgentWorks product shell.
export function shouldShowLiveTerminalControl(
  liveTerminalOffered: boolean,
  isProductSurface: boolean,
  isInteractiveWorkflowBuilder: boolean,
  allowProductSurface = false,
): boolean {
  return liveTerminalOffered && (!isProductSurface || isInteractiveWorkflowBuilder || allowProductSurface)
}

// Only an interactive transport can accept terminal-style live delivery.
// Structured coding-agent turns resume through the ordinary query route; trying
// /live-input first adds a predictable 404/409 and can race another JSON turn.
export function shouldRouteChatInputToLiveTransport({
  hasSession,
  isCodingAgentProvider,
  isWorkflowMode,
  usesStructuredTransport,
}: ChatInputLiveRouteDecision): boolean {
  return hasSession && !usesStructuredTransport && (isCodingAgentProvider || isWorkflowMode)
}

// A rapid Enter/double-click can invoke ChatInput twice before the first
// response clears the draft. Share the complete submission promise
// for that exact session + message so the HTTP mutation, optimistic event, and
// fallback decision all execute once. A later intentional repeat is allowed
// after the first submission settles.
export function createLiveInputSubmissionCoordinator(): LiveInputSubmissionCoordinator {
  const coalesce = createRequestCoalescer()
  return <T>(sessionKey: string, message: string, submit: () => Promise<T>) => {
    const key = JSON.stringify([sessionKey, message.trim()])
    return coalesce(key, submit)
  }
}

export const liveInputSubmissionCoordinator = createLiveInputSubmissionCoordinator()

// Reattach the event stream at the start of every accepted retained message.
// A retained tmux process can outlive the EventSource that observed its prior
// turn (for example after HMR, sleep, or a completed turn). The Zustand entry
// only records the wrapper object; it does not prove that its underlying SSE
// request is still live.
export function shouldRefreshSessionEventStream(
  _fullTurnStreaming: boolean,
  _hasConnection: boolean,
): boolean {
  return true
}

// The live-input endpoint records the durable user_message before returning its
// acknowledgement. SSE can therefore deliver that event while the submitter is
// still awaiting HTTP. Only add the optimistic copy when the acknowledged
// message ID has not already reached the local event store. Matching by the
// backend ID preserves intentional repeated messages with identical text.
export function shouldAppendOptimisticLiveInputMessage(
  events: unknown[],
  acknowledgedMessageId: string | undefined,
): boolean {
  const wanted = acknowledgedMessageId?.trim()
  if (!wanted) return true

  return !events.some(eventValue => {
    if (!eventValue || typeof eventValue !== 'object') return false
    const event = eventValue as Record<string, unknown>
    if (event.type !== 'user_message') return false
    const outer = event.data && typeof event.data === 'object'
      ? event.data as Record<string, unknown>
      : undefined
    const payload = outer?.data && typeof outer.data === 'object'
      ? outer.data as Record<string, unknown>
      : outer
    const metadata = payload?.metadata && typeof payload.metadata === 'object'
      ? payload.metadata as Record<string, unknown>
      : undefined
    return metadata?.message_id === wanted
  })
}
