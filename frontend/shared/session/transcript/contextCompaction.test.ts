import { describe, expect, it } from 'vitest'
import type { PollingEvent } from '../types'
import { buildTranscriptItems } from './terminalEventTranscript'
import { contextCompactionInfo, contextCompactionText } from './contextCompaction'

const ev = (id: string, type: string, data: Record<string, unknown> = {}): PollingEvent =>
  ({ id, type, timestamp: '2026-10-06T10:00:00Z', data: { type, data } }) as unknown as PollingEvent

const compactionRows = (events: PollingEvent[]) => buildTranscriptItems(events)
  .filter(item => item.kind === 'event' && item.event.type === 'context_compaction')
  .map(item => item.kind === 'event' ? contextCompactionText(contextCompactionInfo(item.event)!) : '')

describe('context compaction rows', () => {
  it('keeps one row per compaction at the start position, showing the end once it arrives', () => {
    const start = ev('c1', 'context_compaction', { phase: 'start', compaction_id: 'x' })
    const end = ev('c2', 'context_compaction', { phase: 'end', compaction_id: 'x', duration_ms: 99_000, tokens_before: 384_000, tokens_after: 92_000 })
    expect(compactionRows([ev('u', 'user_message', { content: 'hi' }), start])).toEqual(['Compacting context…'])
    const items = buildTranscriptItems([ev('u', 'user_message', { content: 'hi' }), start, ev('t', 'tool_call_start', { tool_name: 'x' }), end])
    const row = items.findIndex(item => item.kind === 'event' && item.event.type === 'context_compaction')
    expect(items[row].key).toBe('c2')
    expect(row).toBe(1)
    expect(compactionRows([start, end])).toEqual(['Compacted context (1m 39s) · 384k → 92k tokens'])
  })

  it('marks a start left open by a finished turn instead of spinning forever', () => {
    expect(compactionRows([
      ev('c1', 'context_compaction', { phase: 'start', compaction_id: 'x' }),
      ev('u2', 'user_message', { content: 'next' }),
    ])).toEqual(['Context compaction started (no result recorded)'])
  })
})
