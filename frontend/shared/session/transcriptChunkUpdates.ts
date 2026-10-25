import type { PollingEvent } from './types'

// A coding CLI (codex, claude-code) narrates while it works: "I'll separate
// original posts from replies and count both by day", then tool calls, then
// the answer. The backend carries each of those narrations as a whole-message
// streaming_chunk (source "transcript", is_delta false). Streaming packets only
// feed the transient live buffer, so the moment the next chunk or the
// completion replaced it, that sentence was gone from the chat -- while the
// tmux pane still showed it. The durable restore already turns the same
// messages into intermediate llm_generation_end rows (restore.ts, "preserve
// every readable update"); this makes the live path produce the identical row
// so a turn reads the same whether you watched it or reloaded it. The final
// chunk repeats the answer and is dropped against the completion card by
// dropAnswersRepeatedByCompletionCard.
export function intermediateUpdateFromTranscriptChunk(event: PollingEvent): PollingEvent | null {
  if (event.type !== 'streaming_chunk' || !event.id) return null
  const outer = (event.data && typeof event.data === 'object' ? event.data : {}) as Record<string, unknown>
  const inner = (outer.data && typeof outer.data === 'object' ? outer.data : outer) as Record<string, unknown>
  const metadata = (inner.metadata && typeof inner.metadata === 'object' ? inner.metadata : {}) as Record<string, unknown>

  const source = String(inner.source ?? outer.source ?? metadata.source ?? '').trim().toLowerCase()
  if (source !== 'transcript') return null
  if (inner.is_delta === true || outer.is_delta === true) return null
  if (metadata.kind === 'terminal' || metadata.replace === true) return null
  if (inner.is_tool_call === true) return null
  const content = typeof inner.content === 'string' ? inner.content.trim() : ''
  if (!content) return null

  const timestamp = event.timestamp || (typeof inner.timestamp === 'string' ? inner.timestamp : new Date().toISOString())
  return {
    ...event,
    id: `${event.id}-update`,
    type: 'llm_generation_end',
    timestamp,
    data: {
      ...outer,
      type: 'llm_generation_end',
      data: {
        ...inner,
        content,
        result: content,
        status: 'completed',
        restored_intermediate_update: metadata.native_continuation !== true,
      },
    } as PollingEvent['data'],
  }
}

// Live SSE delivers a narration row already projected (id `<chunk>-update`),
// while a durable page, catch-up or persisted tab carries the same row as its
// raw streaming_chunk. Projected here they share one id, and two transcript
// items with one key let React/Virtuoso omit the text. Keep one row per id at
// the first row's position; the durable carrier (it has a sequence) wins.
export function normalizeTranscriptChunkEvents(events: PollingEvent[]): PollingEvent[] {
  const positions = new Map<string, number>()
  const normalized: PollingEvent[] = []
  for (const raw of events) {
    const event = intermediateUpdateFromTranscriptChunk(raw) || raw
    const inner = (event.data as { data?: Record<string, unknown> } | undefined)?.data
    const metadata = inner?.metadata as Record<string, unknown> | undefined
    const nativeReply = event.type === 'llm_generation_end' && metadata?.native_continuation === true
    if (!isTranscriptChunkUpdate(event) && !nativeReply) {
      normalized.push(event)
      continue
    }
    const seen = positions.get(event.id!)
    if (seen === undefined) {
      positions.set(event.id!, normalized.length)
      normalized.push(event)
    } else if (normalized[seen].sequence === undefined && event.sequence !== undefined) {
      normalized[seen] = event
    }
  }
  return normalized
}

export function isTranscriptChunkUpdate(event: PollingEvent): boolean {
  if (event.type !== 'llm_generation_end' || !event.id?.endsWith('-update')) return false
  const inner = (event.data as { data?: Record<string, unknown> } | undefined)?.data
  return inner?.restored_intermediate_update === true
}
