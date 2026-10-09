import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { PollingEvent } from '../services/api-types'
import { buildTranscriptItems, selectTerminalEvents } from '../../shared/session/transcript/terminalEventTranscript'
import { intermediateUpdateFromTranscriptChunk } from '../utils/transcriptChunkUpdates'

// server A SDE crew, 2026-09-23: live SSE delivered Claude's narration as the
// projected llm_generation_end `<chunk>-update`, while the durable journal
// holds the same row as a raw transcript streaming_chunk. When both reached a
// tab they rendered as two items with one key and the user saw no text.

const SESSION = 'work:project:cef0edb2'
const CHUNK_ID = `observer:${SESSION}:span:span_streaming_chunk_1790169853931313448:streaming_chunk`
const TEXT = 'Re-triggered the failed deploy; watching it now.'

const createMemoryStorage = (): Storage => {
  const values = new Map<string, string>()
  return {
    get length() { return values.size },
    clear: () => values.clear(),
    getItem: (key) => values.get(key) ?? null,
    key: (index) => Array.from(values.keys())[index] ?? null,
    removeItem: (key) => { values.delete(key) },
    setItem: (key, value) => { values.set(key, value) },
  }
}

const user = {
  id: 'user-1', type: 'user_message', session_id: SESSION, timestamp: '2026-09-23T13:21:50Z', sequence: 1190,
  data: { type: 'user_message', data: { content: 'Redeploy the SDE service' } },
} as unknown as PollingEvent

const durableChunk = {
  id: CHUNK_ID, type: 'streaming_chunk', session_id: SESSION, timestamp: '2026-09-23T13:24:13.930Z', sequence: 1193,
  data: { type: 'streaming_chunk', data: { content: TEXT, source: 'transcript', is_delta: false, is_tool_call: false, chunk_index: 1 } },
} as unknown as PollingEvent

const liveUpdate = (): PollingEvent => {
  const { sequence: _sequence, ...live } = durableChunk as PollingEvent & { sequence?: number }
  return intermediateUpdateFromTranscriptChunk(live as PollingEvent)!
}

const renderedIds = (events: PollingEvent[]) => buildTranscriptItems(selectTerminalEvents(events, null))
  .flatMap(item => item.kind === 'event' ? [item.key] : [])

describe('transcript narration keeps one identity across live and durable delivery', () => {
  beforeEach(() => {
    vi.resetModules()
    vi.stubGlobal('localStorage', createMemoryStorage())
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('renders the text once when a tab holds both carriers', () => {
    for (const events of [[user, liveUpdate(), durableChunk], [user, durableChunk, liveUpdate()]]) {
      expect(renderedIds(events)).toEqual(['user-1', `${CHUNK_ID}-update`])
    }
  })

  it('lets the durable row replace the live row in the store instead of adding a second', async () => {
    const { useChatStore } = await import('./useChatStore')
    const store = useChatStore.getState()
    store._addTabEventsImmediate(SESSION, [user, liveUpdate()])
    store._addTabEventsImmediate(SESSION, [durableChunk])
    const events = useChatStore.getState().getTabEvents(SESSION)
    expect(events.map(event => event.id)).toEqual(['user-1', `${CHUNK_ID}-update`])
    expect(events[1].sequence).toBe(1193)
    expect(renderedIds(events)).toEqual(['user-1', `${CHUNK_ID}-update`])

    // A late live replay of the same row is a duplicate, not a new message.
    store._addTabEventsImmediate(SESSION, [liveUpdate()])
    expect(useChatStore.getState().getTabEvents(SESSION)).toHaveLength(2)
  })

  it('normalizes a hydrated durable page merged with the live tail', async () => {
    const { useChatStore } = await import('./useChatStore')
    const store = useChatStore.getState()
    store.setTabEvents(SESSION, [user, durableChunk, liveUpdate()])
    const events = useChatStore.getState().getTabEvents(SESSION)
    expect(events.map(event => event.id)).toEqual(['user-1', `${CHUNK_ID}-update`])
    expect(events[1].sequence).toBe(1193)
  })
})
