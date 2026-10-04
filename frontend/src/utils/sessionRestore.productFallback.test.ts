import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const getRecentChatEvents = vi.fn()
vi.mock('../services/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../services/api')>()
  return { ...actual, agentApi: { ...actual.agentApi, getRecentChatEvents } }
})

const createMemoryStorage = (): Storage => {
  const values = new Map<string, string>()
  return {
    get length() { return values.size }, clear: () => values.clear(),
    getItem: key => values.get(key) ?? null,
    key: index => Array.from(values.keys())[index] ?? null,
    removeItem: key => { values.delete(key) },
    setItem: (key, value) => { values.set(key, value) },
  }
}

describe('product chat SQLite restore', () => {
  beforeEach(() => {
    vi.resetModules()
    vi.stubGlobal('localStorage', createMemoryStorage())
    getRecentChatEvents.mockReset()
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('uses the same durable event page for product chats', async () => {
    getRecentChatEvents.mockResolvedValue({
      events: [{ id: 'reply', type: 'streaming_chunk', sequence: 7 }],
      session_status: 'completed', oldest_sequence: 7, latest_sequence: 7,
      last_processed_index: 7, has_more: false,
    })
    const { useChatStore, waitForChatStoreHydration } = await import('../stores/useChatStore')
    const { restoreSession } = await import('./sessionRestore')
    await waitForChatStoreHydration()
    const sessionId = 'video-studio:project:launch'
    const workspacePath = 'Chats/Video Studio/projects/launch'
    const tabId = await useChatStore.getState().createChatTab('Launch', {
      mode: 'multi-agent', agentProfileId: 'video-studio', agentProfileWorkspace: workspacePath,
    }, sessionId)

    await expect(restoreSession(sessionId, { source: 'product-open', workspacePath })).resolves.toBe(tabId)
    // A product chat is an interactive chat: it restores through the compact view.
    expect(getRecentChatEvents).toHaveBeenCalledWith(sessionId, workspacePath, true)
    expect(useChatStore.getState().getTabEvents(sessionId)).toEqual([
      expect.objectContaining({ id: 'reply', sequence: 7 }),
    ])
  })
})
