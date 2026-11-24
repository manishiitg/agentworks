import { beforeEach, describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({
  markTabHistoryLoaded: vi.fn(),
  setTabEvents: vi.fn(),
  addTabEvents: vi.fn(),
  setTabLastEventIndex: vi.fn(),
  setTabHasMoreOlderEvents: vi.fn(),
  setTabHistoryPagination: vi.fn(),
  getTabEvents: vi.fn(),
  getRecentChatEvents: vi.fn(),
}))

vi.mock('../stores/useChatStore', () => ({
  useChatStore: { getState: () => ({
    chatTabs: {},
    markTabHistoryLoaded: mocks.markTabHistoryLoaded,
    setTabEvents: mocks.setTabEvents,
    addTabEvents: mocks.addTabEvents,
    setTabLastEventIndex: mocks.setTabLastEventIndex,
    setTabHasMoreOlderEvents: mocks.setTabHasMoreOlderEvents,
    setTabHistoryPagination: mocks.setTabHistoryPagination,
    getTabEvents: mocks.getTabEvents,
  }) },
}))
vi.mock('../stores/useModeStore', () => ({ useModeStore: { getState: () => ({ setModeCategory: vi.fn() }) } }))
vi.mock('../services/api', () => ({ agentApi: { getRecentChatEvents: mocks.getRecentChatEvents } }))

import { invalidateChatIdentity } from './chatIdentity'
import { hydrateTabEvents } from './sessionRestore'

const response = (events: unknown[], latest = 2) => ({
  events,
  session_status: 'completed',
  last_processed_index: latest,
  latest_sequence: latest,
  oldest_sequence: events.length ? 1 : 0,
  has_more: false,
})

describe('durable SQLite hydration isolation', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    invalidateChatIdentity()
    mocks.getTabEvents.mockReturnValue([])
  })

  it('ignores an older request that resolves after a newer page', async () => {
    let resolveOld!: (value: ReturnType<typeof response>) => void
    mocks.getRecentChatEvents
      .mockImplementationOnce(() => new Promise(done => { resolveOld = done }))
      .mockResolvedValueOnce(response([{ id: 'answer', type: 'streaming_chunk', sequence: 2 }]))

    const oldRequest = hydrateTabEvents('chat')
    await hydrateTabEvents('chat')
    resolveOld(response([{ id: 'question', type: 'user_message', sequence: 1 }], 1))
    await oldRequest

    expect(mocks.setTabEvents).toHaveBeenCalledTimes(1)
    expect(mocks.setTabEvents).toHaveBeenLastCalledWith('chat', [expect.objectContaining({ id: 'answer' })])
  })

  it('does not mark failed or superseded history as loaded (PLAT-746-249)', async () => {
    mocks.getRecentChatEvents.mockRejectedValueOnce(new Error('server unavailable'))
    await expect(hydrateTabEvents('chat')).rejects.toThrow('server unavailable')
    expect(mocks.markTabHistoryLoaded).not.toHaveBeenCalled()
    let resolveFirst!: (value: ReturnType<typeof response>) => void
    let resolveSecond!: (value: ReturnType<typeof response>) => void
    mocks.getRecentChatEvents
      .mockImplementationOnce(() => new Promise(done => { resolveFirst = done }))
      .mockImplementationOnce(() => new Promise(done => { resolveSecond = done }))
    const first = hydrateTabEvents('chat')
    const second = hydrateTabEvents('chat')
    resolveFirst(response([]))
    await first
    expect(mocks.markTabHistoryLoaded).not.toHaveBeenCalled()
    resolveSecond(response([]))
    await second
    expect(mocks.markTabHistoryLoaded).toHaveBeenCalledWith('chat')
  })

  it('rejects a response from the previous account before writing state', async () => {
    let resolve!: (value: ReturnType<typeof response>) => void
    mocks.getRecentChatEvents.mockImplementation(() => new Promise(done => { resolve = done }))
    const pending = hydrateTabEvents('shared-session')
    invalidateChatIdentity()
    resolve(response([{ id: 'private', type: 'user_message' }]))
    await expect(pending).rejects.toThrow('Chat account changed')
    expect(mocks.setTabEvents).not.toHaveBeenCalled()
    expect(mocks.markTabHistoryLoaded).not.toHaveBeenCalled()
  })

  it('keeps the visible transcript on an unchanged reopen, but applies same-ID edits', async () => {
    const cached = [
      { id: 'question', type: 'user_message', sequence: 1, data: { content: 'Question' } },
      { id: 'answer', type: 'user_message', sequence: 2, data: { content: 'Original' } },
    ]
    mocks.getTabEvents.mockReturnValue(cached)
    mocks.getRecentChatEvents.mockResolvedValueOnce(response(structuredClone(cached)))
    await hydrateTabEvents('chat')
    expect(mocks.setTabEvents).not.toHaveBeenCalled()

    const edited = structuredClone(cached)
    edited[1].data.content = 'Updated'
    mocks.getRecentChatEvents.mockResolvedValueOnce(response(edited))
    await hydrateTabEvents('chat')
    expect(mocks.setTabEvents).toHaveBeenCalledWith('chat', edited)
  })

  it('keeps visible steps omitted by a compact refresh of a warm chat', async () => {
    const question = { id: 'question', type: 'user_message', sequence: 1 }
    const start = { id: 'start', type: 'tool_call_start', sequence: 2 }
    const end = { id: 'end', type: 'tool_call_end', sequence: 3 }
    const answer = { id: 'answer', type: 'agent_end', sequence: 4 }
    const latest = { id: 'latest', type: 'user_message', sequence: 5 }
    const compact = [question, answer, latest]
    mocks.getTabEvents.mockReturnValue([question, start, end, answer, latest])
    mocks.getRecentChatEvents.mockResolvedValueOnce(response(structuredClone(compact), 5))
    await hydrateTabEvents('chat', { compact: true })
    expect(mocks.setTabEvents).not.toHaveBeenCalled()

    const next = { id: 'next', type: 'user_message', sequence: 6 }
    const updatedAnswer = { ...answer, data: { content: 'Updated' } }
    mocks.getRecentChatEvents.mockResolvedValueOnce(response([question, updatedAnswer, latest, next], 6))
    await hydrateTabEvents('chat', { compact: true })
    expect(mocks.setTabEvents).toHaveBeenCalledWith('chat', [question, start, end, updatedAnswer, latest, next])
  })

  it('does not recover rewound steps on a compact reopen', async () => {
    const kept = { id: 'kept', type: 'user_message', sequence: 1 }
    const oldQuestion = { id: 'old-question', type: 'user_message', sequence: 2 }
    const oldStep = { id: 'old-step', type: 'tool_call_end', sequence: 3 }
    const oldAnswer = { id: 'old-answer', type: 'agent_end', sequence: 4 }
    const replacement = { id: 'replacement', type: 'user_message', sequence: 5 }
    mocks.getTabEvents.mockReturnValue([kept, oldQuestion, oldStep, oldAnswer])
    mocks.getRecentChatEvents.mockResolvedValueOnce(response([kept, replacement], 5))
    await hydrateTabEvents('chat', { compact: true })
    expect(mocks.setTabEvents).toHaveBeenCalledWith('chat', [kept, replacement])
  })
})

// PLAT-746-988: the goal projection is not a historical page/cursor row.
it('restores a goal snapshot without moving the journal pagination boundary', async () => {
  vi.resetAllMocks()
  invalidateChatIdentity()
  const staleGoal = { id: 'stale-goal', type: 'native_goal_updated', sequence: 1 }
  const newerGoal = { id: 'newer-goal', type: 'native_goal_updated', sequence: 101 }
  mocks.getTabEvents.mockReturnValue([]).mockReturnValueOnce([]).mockReturnValueOnce([staleGoal, newerGoal])
  mocks.getRecentChatEvents.mockResolvedValue({ ...response([{ id: 'tail', type: 'user_message', sequence: 100 }], 100), oldest_sequence: 100, has_more: true,
    native_goal_snapshot: { id: 'old-goal', type: 'native_goal_updated', sequence: 2, data: { update: { goal: { objective: 'Task', status: 'paused' } } } } })
  await hydrateTabEvents('goal-chat')
  const restored = mocks.setTabEvents.mock.calls.at(-1)?.[1] as Array<{ id: string; sequence?: number }>
  expect(restored.find(event => event.id === 'native-goal-snapshot:goal-chat')?.sequence).toBeUndefined()
  expect(mocks.addTabEvents).toHaveBeenCalledWith('goal-chat', [newerGoal])
  expect(mocks.setTabLastEventIndex).toHaveBeenCalledWith('goal-chat', 100)
  expect(mocks.setTabHistoryPagination).toHaveBeenCalledWith('goal-chat', expect.objectContaining({ nextOffset: 100 }))
})
