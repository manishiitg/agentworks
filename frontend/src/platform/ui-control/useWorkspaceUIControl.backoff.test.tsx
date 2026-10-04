// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const workflowUIControl = vi.hoisted(() => vi.fn())
vi.mock('../../services/api', () => ({
  agentApi: {},
  getApiBaseUrl: () => '',
  getAuthToken: () => null,
  workflowUIControl,
}))
vi.mock('../presentations/usePresentationEvents', () => ({ usePresentationEvents: () => [] }))

import { sessionLooksLive, useWorkspaceUIControl } from './useWorkspaceUIControl'
import { useChatStore } from '../../stores/useChatStore'

// eslint-disable-next-line @typescript-eslint/no-explicit-any
;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const notActive = () => Object.assign(new Error('conflict'), { response: { status: 409, data: 'session_not_active\n' } })
const bindCalls = () => workflowUIControl.mock.calls.filter(([, body]) => body?.operation === 'bind').length

function Probe({ session }: { session: string }) {
  useWorkspaceUIControl(session)
  return null
}

describe('UI control lease for chats without a live session', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    vi.useFakeTimers()
    workflowUIControl.mockReset()
    workflowUIControl.mockRejectedValue(notActive())
    useChatStore.setState({ chatTabs: {}, activeSessionsCache: [] })
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
    vi.useRealTimers()
  })

  it('goes dormant: the backup poll never re-binds an idle chat', async () => {
    await act(async () => { root.render(<Probe session="idle-chat" />) })
    await act(async () => { await vi.advanceTimersByTimeAsync(120_000) })

    // Previously one failed bind per ten-second poll (13 here); now only the mount attempt.
    expect(bindCalls()).toBe(1)
  })

  it('wakes from dormancy when the page becomes visible again', async () => {
    await act(async () => { root.render(<Probe session="idle-chat" />) })
    await act(async () => {
      document.dispatchEvent(new Event('visibilitychange'))
      await vi.advanceTimersByTimeAsync(0)
    })
    expect(bindCalls()).toBe(2)
  })

  it('binds immediately when the chat goes live', async () => {
    await act(async () => { root.render(<Probe session="chat-1" />) })
    expect(bindCalls()).toBe(1)

    workflowUIControl.mockImplementation(async (_session: string, body: { operation: string }) => (
      body.operation === 'bind' ? { binding: 'b', token: 't', workspace: 'w' } : []
    ))
    await act(async () => {
      useChatStore.setState({ activeSessionsCache: [{ session_id: 'chat-1' } as never] })
      await vi.advanceTimersByTimeAsync(0)
    })

    expect(bindCalls()).toBe(2)
  })

  it('derives liveness from a streaming tab or the active-session list', () => {
    expect(sessionLooksLive({ chatTabs: { a: { sessionId: 's', isStreaming: true } }, activeSessionsCache: [] }, 's')).toBe(true)
    expect(sessionLooksLive({ chatTabs: {}, activeSessionsCache: [{ session_id: 's' }] }, 's')).toBe(true)
    expect(sessionLooksLive({ chatTabs: { a: { sessionId: 's', isStreaming: false } }, activeSessionsCache: [] }, 's')).toBe(false)
  })

  it('retries a refused bind while the chat looks live, then connects once the server tracks it', async () => {
    useChatStore.setState({ activeSessionsCache: [{ session_id: 'live-chat' } as never] })
    workflowUIControl.mockImplementation(async (_session: string, body: { operation: string }) => {
      if (body.operation !== 'bind') return []
      if (bindCalls() <= 2) throw notActive()
      return { binding: 'b', token: 't', workspace: 'w' }
    })
    await act(async () => { root.render(<Probe session="live-chat" />) })
    expect(bindCalls()).toBe(1)
    await act(async () => { await vi.advanceTimersByTimeAsync(1_000) })
    expect(bindCalls()).toBe(2)
    await act(async () => { await vi.advanceTimersByTimeAsync(2_000) })
    expect(bindCalls()).toBe(3)
    await act(async () => { await vi.advanceTimersByTimeAsync(60_000) })
    expect(bindCalls()).toBe(3)
  })

  it('stops retrying after a few attempts when the server never tracks the live-looking chat', async () => {
    useChatStore.setState({ activeSessionsCache: [{ session_id: 'live-chat' } as never] })
    await act(async () => { root.render(<Probe session="live-chat" />) })
    await act(async () => { await vi.advanceTimersByTimeAsync(120_000) })
    expect(bindCalls()).toBe(5)
  })
})
