import { beforeEach, expect, it, vi } from 'vitest'
import type { PollingEvent } from '../services/api-types'
const mocks = vi.hoisted(() => ({
  history: vi.fn(), runtime: vi.fn(), getEvents: vi.fn(), setEvents: vi.fn(), cursor: vi.fn(),
  hasMore: vi.fn(), pagination: vi.fn(), append: vi.fn(),
}))
vi.mock('../services/api', () => ({ agentApi: { getChatHistoryResumeConversation: mocks.history, getSessionEvents: mocks.runtime } }))
vi.mock('../stores/useChatStore', () => ({ useChatStore: { getState: () => ({
  getTabEvents: mocks.getEvents, setTabEvents: mocks.setEvents, setTabLastEventIndex: mocks.cursor,
  setTabHasMoreOlderEvents: mocks.hasMore, setTabHistoryPagination: mocks.pagination,
}) } }))
vi.mock('./sessionRestore', () => ({ appendRestoredLiveTail: mocks.append }))
import { hydrateExecutionConversation } from './executionConversationRestore'
import { invalidateChatIdentity } from './chatIdentity'

beforeEach(() => {
  vi.resetAllMocks()
  mocks.getEvents.mockReturnValue([])
  mocks.runtime.mockResolvedValue({ session_status: 'running', last_processed_index: 42 })
})
const conversation = { session_id: 'schedule', conversation_history: [{ Role: 'ai', Parts: [{ Text: 'Saved reply' }] }], history_pagination: { has_more: true, next_offset: 100 } }

it('shares concurrent opens, establishes the live cursor and preserves events received during the request', async () => {
  let resolve!: (value: typeof conversation) => void
  mocks.history.mockReturnValue(new Promise<typeof conversation>(done => { resolve = done }))
  const first = hydrateExecutionConversation('schedule', 'Workflow/trading')
  const second = hydrateExecutionConversation('schedule', 'Workflow/trading')
  expect(second).toBe(first)
  expect(mocks.history).toHaveBeenCalledOnce()
  const live = { id: 'live-tool', type: 'tool_call_end' } as PollingEvent
  mocks.getEvents.mockReturnValue([live])
  resolve(conversation)
  await first
  expect(mocks.cursor).toHaveBeenCalledWith('schedule', 42)
  expect(mocks.append).toHaveBeenCalledWith('schedule', [live])
  expect(mocks.pagination).toHaveBeenCalledWith('schedule', { hasMore: true, nextOffset: 100 })
})

it('does not apply a previous account’s late restore', async () => {
  let resolve!: (value: typeof conversation) => void
  mocks.history.mockReturnValue(new Promise<typeof conversation>(done => { resolve = done }))
  const request = hydrateExecutionConversation('schedule', 'Workflow/trading')
  invalidateChatIdentity()
  resolve(conversation)
  await expect(request).rejects.toThrow('Chat account changed')
  expect(mocks.setEvents).not.toHaveBeenCalled()
  expect(mocks.cursor).not.toHaveBeenCalled()
})
