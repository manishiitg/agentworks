// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { useChatRuntimeActivity } from './useChatRuntimeActivity'
import { useChatStore } from '../stores/useChatStore'
import type { ChatTab } from '../stores/useChatStore'
import type { ActiveSessionInfo, PollingEvent } from '../services/api-types'

let cleanup: (() => void) | undefined
const original = useChatStore.getState()
afterEach(() => { cleanup?.(); useChatStore.setState(original, true); vi.unstubAllGlobals() })
function Activity({ tabId }: { tabId: string }) {
  const activity = useChatRuntimeActivity(tabId)
  return <div>{activity.state}:{activity.label}</div>
}
it('scopes activity and completion to the displayed chat across switches', async () => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  const refresh = vi.fn().mockResolvedValue([])
  useChatStore.setState({ getActiveSessions: refresh, activeSessionsCache: [], tabEvents: {}, chatTabs: {
    a: { tabId: 'a', sessionId: 'session-a', isStreaming: true } as ChatTab,
    b: { tabId: 'b', sessionId: 'session-b', isCompleted: true } as ChatTab,
  } })
  const host = document.createElement('div'); document.body.appendChild(host)
  const root = createRoot(host)
  cleanup = () => { act(() => root.unmount()); host.remove() }
  const show = async (tabId: string) => { await act(async () => { root.render(<Activity tabId={tabId} />) }) }
  await show('a'); expect(host.textContent).toBe('running:running')
  await show('b'); expect(host.textContent).toBe('ready:idle')
  await act(async () => {
    useChatStore.setState(state => ({ chatTabs: { ...state.chatTabs,
      a: { ...state.chatTabs.a, isStreaming: false, isCompleted: true },
    } }))
  })
  expect(host.textContent).toBe('ready:idle')
  await show('a'); expect(host.textContent).toBe('ready:idle')
  await act(async () => {
    useChatStore.setState(state => ({ chatTabs: { ...state.chatTabs,
      a: { ...state.chatTabs.a, isStreaming: true, isCompleted: false },
    } }))
  })
  expect(host.textContent).toBe('running:running')
  await act(async () => {
    useChatStore.setState(state => ({ chatTabs: { ...state.chatTabs,
      a: { ...state.chatTabs.a, isStreaming: false, isCompleted: true },
    } }))
  })
  expect(host.textContent).toBe('ready:idle')
  expect(refresh).toHaveBeenCalled()
})

it('settles the rendered chat from completion events despite stale tab and session status, then starts the next turn', async () => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  const event = (type: string): PollingEvent => ({ id: type, type, timestamp: '2026-10-01T07:00:00Z', data: { type, data: {} } }) as PollingEvent
  useChatStore.setState({
    getActiveSessions: vi.fn().mockResolvedValue([]),
    activeSessionsCache: [{ session_id: 'session-a', status: 'running' } as ActiveSessionInfo],
    tabEvents: { 'session-a': [event('user_message')] },
    chatTabs: { a: { tabId: 'a', sessionId: 'session-a', isStreaming: true, isCompleted: false } as ChatTab },
  })
  const host = document.createElement('div'); document.body.appendChild(host)
  const root = createRoot(host)
  cleanup = () => { act(() => root.unmount()); host.remove() }
  await act(async () => { root.render(<Activity tabId="a" />) })
  expect(host.textContent).toBe('running:running')
  await act(async () => {
    useChatStore.setState({ tabEvents: { 'session-a': [event('user_message'), event('agent_end')] } })
  })
  expect(host.textContent).toBe('ready:idle')
  // Reconciliation can reassert the old busy state after completion.
  await act(async () => {
    useChatStore.setState({ activeSessionsCache: [{ session_id: 'session-a', status: 'running' } as ActiveSessionInfo] })
  })
  expect(host.textContent).toBe('ready:idle')
  await act(async () => {
    useChatStore.setState({ tabEvents: { 'session-a': [event('agent_end'), { ...event('user_message'), id: 'next-user' }] } })
  })
  expect(host.textContent).toBe('running:running')
})
