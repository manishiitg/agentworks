// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { SessionStopButton } from './SessionStopButton'
import { useChatStore } from '../stores/useChatStore'
import type { ChatTab } from '../stores/useChatStore'
import type { PollingEvent } from '../services/api-types'

const original = useChatStore.getState()
afterEach(() => { useChatStore.setState(original, true); vi.unstubAllGlobals() })

// PLAT-699: a Crew chat showed "Working…" (from the turn's events) while the
// tab's isStreaming flag had dropped, and Stop was gone. Stop must follow the
// same signal as "Working…".
it('shows Stop while the transcript says the turn is working, even after the tab flags dropped', async () => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  const event = (type: string): PollingEvent => ({ id: type, type, timestamp: '2026-10-07T10:45:00Z', data: { type, data: {} } }) as PollingEvent
  useChatStore.setState({
    getActiveSessions: vi.fn().mockResolvedValue([]),
    activeSessionsCache: [],
    tabEvents: { 'session-a': [event('user_message')] },
    chatTabs: { a: { tabId: 'a', sessionId: 'session-a', isStreaming: false, hasRunningBgAgents: false, isCompleted: false } as ChatTab },
  })
  const host = document.createElement('div'); document.body.appendChild(host)
  const root = createRoot(host)
  await act(async () => { root.render(<SessionStopButton tabId="a" />) })
  expect(host.querySelector('[data-testid="chat-stop-button"]')).not.toBeNull()

  await act(async () => {
    useChatStore.setState({ tabEvents: { 'session-a': [event('user_message'), event('agent_end')] } })
  })
  expect(host.querySelector('[data-testid="chat-stop-button"]')).toBeNull()
  act(() => root.unmount()); host.remove()
})
