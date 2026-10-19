// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { expect, it, vi } from 'vitest'
import type { PendingQueuedMessage } from '../../../shared/session/types'

const fixture = vi.hoisted(() => ({
  messages: [{ message_id: 'queued-1', client_message_id: 'client-1', content: 'Review the settings page.', queue_position: 1, queue_reason: 'running', can_steer: true }] as PendingQueuedMessage[],
  events: [] as any[],
  remove: vi.fn(), steer: vi.fn(), retry: vi.fn(),
}))
vi.mock('../../services/api', () => ({ agentApi: {
  getQueuedMessages: vi.fn(async () => [...fixture.messages]),
  removeQueuedMessage: fixture.remove,
  steerQueuedMessage: fixture.steer,
  retryQueuedMessage: fixture.retry,
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
  const render = () => root.render(<QueryClientProvider client={client}><DurableChatQueue sessionId="chat-1" tabId="A" busy /></QueryClientProvider>)
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


it('keeps a failed queued message and its text on reload, retries only on click', async () => {
  fixture.messages = [{ message_id: 'failed-1', client_message_id: 'failed-client', content: 'Explain gravity.', queue_position: 1, delivery_error: 'Check the chat before retrying.' }]
  fixture.events = [{ id: 'user:failed-client', type: 'user_message', data: { data: { content: 'Explain gravity.', metadata: { client_message_id: 'failed-client', confirmation: 'failed' } } } }]
  fixture.retry.mockClear()
  const host = document.createElement('div')
  const root = createRoot(host)
  let client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const render = () => root.render(<QueryClientProvider client={client}><DurableChatQueue sessionId="chat-1" tabId="A" busy={false} /></QueryClientProvider>)
  const flush = async () => { await act(async () => { await new Promise(resolve => setTimeout(resolve, 20)) }) }
  try {
    await act(async () => render())
    await flush()
    expect(host.textContent).toContain('Explain gravity.')
    expect(host.textContent).toContain('Check the chat before retrying.')
    expect(host.textContent).not.toContain('Steer now')
    expect(fixture.retry).not.toHaveBeenCalled()
    await act(async () => root.render(null))
    client.clear()
    client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    await act(async () => render())
    await flush()
    expect(host.textContent).toContain('Explain gravity.')
    fixture.retry.mockRejectedValueOnce(new Error('network unavailable'))
    const retry = () => [...host.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent === 'Retry')!
    await act(async () => retry().click())
    await flush()
    expect(host.textContent).toContain('Explain gravity.')
    fixture.retry.mockImplementationOnce(async () => { fixture.messages = [] })
    await act(async () => retry().click())
    await flush()
    expect(fixture.retry).toHaveBeenLastCalledWith('chat-1', 'failed-1')
    expect(fixture.events[0].data.data.content).toBe('Explain gravity.')
  } finally {
    await act(async () => root.unmount())
    client.clear()
    host.remove()
  }
})

it('says the chat is being updated and offers no Steer now, but keeps Remove so a long update can be withdrawn', async () => {
  fixture.messages = [{ message_id: 'refresh-1', client_message_id: 'refresh-client', content: 'Go on with the plan.', queue_position: 1, refreshing: true }]
  fixture.events = []
  const host = document.createElement('div')
  const root = createRoot(host)
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const flush = async () => { await act(async () => { await new Promise(resolve => setTimeout(resolve, 20)) }) }
  try {
    await act(async () => root.render(<QueryClientProvider client={client}><DurableChatQueue sessionId="chat-1" tabId="A" busy /></QueryClientProvider>))
    await flush()
    expect(host.textContent).toContain('Updating this chat')
    expect(host.textContent).toContain('Go on with the plan.')
    expect(host.textContent).not.toContain('Steer now')
    const remove = [...host.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent === 'Remove')
    expect(remove).toBeTruthy()
    expect(remove!.disabled).toBe(false)
  } finally {
    await act(async () => root.unmount())
    client.clear()
    host.remove()
  }
})

for (const reason of ['starting', 'waiting', undefined] as const) {
  it(`uses server ${reason ?? 'legacy'} queue state without inferring steering from a busy tab`, async () => {
    fixture.messages = [{ message_id: 'state-1', client_message_id: 'state-client', content: 'Follow up', queue_reason: reason, can_steer: false }]
    const host = document.createElement('div')
    const root = createRoot(host)
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    try {
      await act(async () => root.render(<QueryClientProvider client={client}><DurableChatQueue sessionId="chat-1" tabId="A" busy /></QueryClientProvider>))
      await act(async () => { await new Promise(resolve => setTimeout(resolve, 20)) })
      expect(host.textContent).not.toContain('Steer now')
      expect(host.textContent).toContain('Remove')
      expect(host.querySelector('[title="Sends automatically when the current turn finishes"]')).toBeNull()
      if (reason === 'starting') {
        expect(host.textContent).toContain('Starting agent…')
        expect(host.querySelector('[title="The agent is starting or resuming. Your message will send automatically."]')).toBeTruthy()
      }
    } finally {
      await act(async () => root.unmount())
      client.clear()
      host.remove()
    }
  })
}
