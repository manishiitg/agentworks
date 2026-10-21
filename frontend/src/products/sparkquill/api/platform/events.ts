// SparkQuill's view of the platform's session events: the shared session
// client (frontend/shared/session) does the transport, the foreground
// filter and the presentation parsing; what lives here is the product's
// own mapping from a session's events to the stored transcript the
// activity history reads (turns themselves render in the shared ChatArea).
import type { StoredMsg } from '../../stores/types'
import { eventBelongsToSession, isForegroundSessionEvent } from '../../../../../shared/session';
import type { PollingEvent } from '../../../../../shared/session';

/** The shapes the UI code below reads; aliases of the shared contract. */
export type PlatformEvent = PollingEvent

/**
 * A coding-agent turn's whole assistant text (narration before tool calls
 * included), recorded by the provider in the generation metadata. The
 * conversation shows this; `final_result` stays the last message only, the
 * way workflows consume it.
 */
export function assistantTurnText(p: Record<string, unknown>): string {
  const meta = p.metadata as Record<string, unknown> | undefined
  const turn = meta?.assistant_turn_text
  return typeof turn === 'string' ? turn.trim() : ''
}

/**
 * The stored conversation a restored event stream describes: user turns, one
 * assistant message per turn (the recorded turn text, else every restored
 * assistant update joined, else final_result), and the product cards that
 * survive a reload. Used for history after a reload or a server restart, so
 * it must agree with what the live turn showed.
 */
export function messagesFromEvents(events: PlatformEvent[], sessionID: string): StoredMsg[] {
  const messages: StoredMsg[] = []
  let turnText = ''
  let pieces: string[] = []
  for (const e of events) {
    const type = e.type ?? e.data?.type
    const p = payloadOf(e)
    if (type === 'user_message' && typeof p.content === 'string') {
      messages.push({ role: 'user', text: p.content })
      turnText = ''; pieces = []
      continue
    }
    if (type === 'product_interaction') {
      const payload = (p.payload ?? {}) as Record<string, unknown>
      if (p.kind === 'celebrate') messages.push({ role: 'tool', tool: 'celebrate', stars: Number(payload.stars ?? 1), reason: String(payload.reason ?? '') })
      if (p.kind === 'scene' && typeof payload.html === 'string') messages.push({ role: 'tool', tool: 'scene', html: payload.html })
      continue
    }
    if (!isMainEvent(e, sessionID)) continue
    if (type === 'llm_generation_end') {
      turnText = assistantTurnText(p) || turnText
      if (typeof p.content === 'string' && p.content.trim()) pieces.push(p.content.trim())
      continue
    }
    if (type === 'unified_completion' && typeof p.final_result === 'string' && p.final_result.trim()) {
      messages.push({ role: 'assistant', text: turnText || pieces.join('\n\n') || p.final_result })
      turnText = ''; pieces = []
    }
  }
  return messages
}

export function payloadOf(e: PlatformEvent): Record<string, unknown> {
  return ((e.data as { data?: Record<string, unknown> } | undefined)?.data ?? {}) as Record<string, unknown>
}

/** Foreground-agent events of this session only, the way AgentWorks decides it. */
export function isMainEvent(e: PlatformEvent, sessionID: string): boolean {
  if (sessionID && !eventBelongsToSession(sessionID, e)) return false
  const data = e.data as { component?: unknown; correlation_id?: unknown } | undefined
  return isForegroundSessionEvent(e, data?.component, data?.correlation_id)
}
