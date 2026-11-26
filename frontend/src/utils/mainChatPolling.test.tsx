// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { agentApi, authApi } from '../services/api'
import type { ChatContextView } from '../services/api-types'
import { invalidateChatIdentity } from './chatIdentity'
import { fetchChatContextShared } from '../products/work/useChatContextView'
import { SharedTokenUsageChip, SharedTokenUsageNotice } from '../components/providers/SharedTokenUsageNotice'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

describe('main chat polling and fetch deduplication (#4227)', () => {
  afterEach(() => {
    vi.restoreAllMocks()
    invalidateChatIdentity()
  })

  it('deduplicates concurrent and fresh fetchChatContextShared calls and invalidates on identity change', async () => {
    const view: ChatContextView = {
      session_id: 'sess-context-1',
      used_tokens: 1200,
    } as ChatContextView
    const spy = vi.spyOn(agentApi, 'getChatContext').mockResolvedValue(view)

    const [first, second] = await Promise.all([
      fetchChatContextShared('sess-context-1'),
      fetchChatContextShared('sess-context-1'),
    ])
    expect(first).toBe(view)
    expect(second).toBe(view)
    expect(spy).toHaveBeenCalledTimes(1)

    const third = await fetchChatContextShared('sess-context-1')
    expect(third).toBe(view)
    expect(spy).toHaveBeenCalledTimes(1)

    invalidateChatIdentity()
    await fetchChatContextShared('sess-context-1')
    expect(spy).toHaveBeenCalledTimes(2)
  })

  it('does not fetch shared token usage when chat runs on the user own account (accountProvider === null)', async () => {
    const spy = vi.spyOn(authApi, 'getMyTokenUsage').mockResolvedValue({
      day_resets_at: '2026-11-27T00:00:00Z',
      week_resets_at: '2026-12-01T00:00:00Z',
    } as never)

    const host = document.createElement('div')
    const root = createRoot(host)
    try {
      await act(async () => {
        root.render(
          <>
            <SharedTokenUsageChip accountProvider={null} sessionId="own-session" />
            <SharedTokenUsageNotice accountProvider={null} />
          </>
        )
      })
      expect(spy).not.toHaveBeenCalled()
      expect(host.textContent).toContain('Your own account')
    } finally {
      await act(async () => root.unmount())
    }
  })
})
