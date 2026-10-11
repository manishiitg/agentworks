// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { expect, it, vi } from 'vitest'

const fixture = vi.hoisted(() => ({
  messages: [{ message_id: 'queued-1', client_message_id: 'client-1', content: 'Review the settings page.', queue_position: 1 }],
  events: [] as any[],
  remove: vi.fn(), steer: vi.fn(),
}))
vi.mock('../../services/api', () => ({ agentApi: {
  getQueuedMessages: vi.fn(async () => [...fixture.messages]),
  removeQueuedMessage: fixture.remove,
  steerQueuedMessage: fixture.steer,
} }))
vi.mock('../../utils/chatIdentity', () => ({ captureChatIdentity: () => 1, isChatIdentityCurrent: () => true }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: { getState: () => ({
  getTab: () => ({ sessionId: 'chat-1' }),
  getTabEvents: () => fixture.events,
  setTabEvents: (_session: string, events: any[]) => { fixture.events = events },
}) } }))
import { DurableChatQueue } from './DurableChatQueue'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('restores the server queue and keeps a failed remove visible until the server accepts it', async () => {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  let client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const render = () => root.render(<QueryClientProvider client={client}><DurableChatQueue sessionId="chat-1" tabId="A" busy canSteer /></QueryClientProvider>)
  const flush = async () => { await act(async () => { await new Promise(resolve => setTimeout(resolve, 20)) }) }
  try {
    await act(async () => render())
    await flush()
    expect(host.textContent).toContain('Review the settings page.')
    expect(host.querySelector('[title="Sends automatically when the current turn finishes"]')).toBeTruthy()
    expect(host.textContent).toContain('Steer now')
    fixture.remove.mockRejectedValueOnce(new Error('network unavailable'))
    const remove = () => [...host.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent === 'Remove')!
    await act(async () => remove().click())
    await flush()
    expect(host.textContent).toContain('Review the settings page.')
    // A page refresh loses the entire browser query cache. Restore the same
    // durable entry from the server without resubmitting it.
    await act(async () => root.render(null))
    client.clear()
    client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    await act(async () => render())
    await flush()
    expect(host.textContent).toContain('Review the settings page.')
    fixture.remove.mockImplementationOnce(async () => { fixture.messages = [] })
    await act(async () => remove().click())
    await flush()
    expect(fixture.remove).toHaveBeenLastCalledWith('chat-1', 'queued-1')
    expect(host.textContent).not.toContain('Review the settings page.')
    expect(fixture.steer).not.toHaveBeenCalled()
  } finally {
    await act(async () => root.unmount())
    client.clear()
    host.remove()
  }
})
