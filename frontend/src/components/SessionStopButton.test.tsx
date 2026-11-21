// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'

const cancel = vi.fn(async () => {})
vi.mock('../services/api', () => ({ agentApi: { cancelCurrentTurn: cancel, stopSession: vi.fn() } }))
vi.mock('../hooks/useTabTurnActive', () => ({ useTabTurnActive: () => true }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// #3188: an agent that ignores Stop keeps the chat working; 10 s later the chat offers Force stop.
it('offers Force stop when the agent is still working 10 seconds after Stop', async () => {
  vi.useFakeTimers()
  const { useChatStore } = await import('../stores/useChatStore')
  const { SessionStopButton } = await import('./SessionStopButton')
  useChatStore.setState(state => ({ chatTabs: { ...state.chatTabs, t1: { tabId: 't1', sessionId: 's1', metadata: {} } as never } }))
  const host = document.createElement('div'), root = createRoot(host)
  try {
    await act(async () => root.render(<SessionStopButton tabId="t1" />))
    await act(async () => host.querySelector<HTMLButtonElement>('[data-testid="chat-stop-button"]')!.click())
    expect(cancel).toHaveBeenLastCalledWith('s1')
    expect(host.querySelector('[data-testid="chat-force-stop-button"]')).toBeNull()
    await act(async () => { vi.advanceTimersByTime(10_000) })
    const force = host.querySelector<HTMLButtonElement>('[data-testid="chat-force-stop-button"]')
    expect(force).toBeTruthy()
    await act(async () => force!.click())
    expect(cancel).toHaveBeenLastCalledWith('s1', true)
  } finally {
    await act(async () => root.unmount())
    vi.useRealTimers()
  }
})
