import { describe, expect, it, vi } from 'vitest'
vi.mock('../services/api', () => ({ default: {}, agentApi: {} }))
vi.mock('../stores/useChatStore', () => ({ useChatStore: { getState: () => ({ chatTabs: {}, addToast: () => {} }) } }))
vi.mock('./forkChatTab', () => ({ forkChatTab: async () => true }))
import { btwAnswerFromEvents, btwChatNote, btwView, showBtwHistory, useBtwStore } from './btwSideQuestion'
import type { PollingEvent } from '../services/api-types'

const event = (id: string, type: string, data: Record<string, unknown>): PollingEvent => ({ id, type, timestamp: '2026-11-27T12:00:00Z', data: { data } } as unknown as PollingEvent)

describe('/btw side questions', () => {
  it('reads only the answer to the side question, not the copied conversation', () => {
    const events = [
      event('1', 'user_message', { content: 'earlier question in the chat' }),
      event('2', 'unified_completion', { content: 'earlier answer the copy already had' }),
      event('3', 'user_message', { content: 'is the retry safe?' }),
      event('4', 'unified_completion', { content: 'Yes: it only repeats calls that never reached the server.' }),
    ]
    expect(btwAnswerFromEvents(events)).toEqual({ answer: 'Yes: it only repeats calls that never reached the server.', error: '' })
    expect(btwAnswerFromEvents(events.slice(0, 3)).answer).toBe('')
  })

  it('adds the question and answer to the chat as quoted text', () => {
    expect(btwChatNote({ question: 'why?', answer: 'because\nof this' })).toBe('By the way, I asked this on the side:\n> why?\n\nThe answer was:\n> because\n> of this')
  })

  it('lists earlier side questions only when there are some', () => {
    expect(showBtwHistory('tab-a')).toBe(false)
    useBtwStore.setState({ byTab: { 'tab-a': { items: [{ id: 'x', question: 'q', status: 'done', answer: 'a', at: 1 }], open: null } } })
    expect(showBtwHistory('tab-a')).toBe(true)
    expect(btwView(useBtwStore.getState().byTab['tab-a'])?.kind).toBe('history')
  })
})
