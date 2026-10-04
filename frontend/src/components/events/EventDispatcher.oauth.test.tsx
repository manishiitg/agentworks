// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import { EventDispatcher } from './EventDispatcher'
import type { PollingEvent } from '../../services/api-types'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('shows Vault OAuth outcomes even before a retained agent can continue the chat', async () => {
  const container = document.createElement('div')
  const root = createRoot(container)
  for (const status of ['completed', 'failed']) {
    const event = {
      type: 'synthetic_turn_ready',
      data: {
        type: 'synthetic_turn_ready',
        data: {
          agent_id: 'vault-oauth:c-11111111:attempt-one',
          name: 'Notion',
          status,
          message: 'internal agent instruction',
        },
      },
    } as unknown as PollingEvent
    await act(async () => root.render(<EventDispatcher event={event} />))
    expect(container.textContent).toContain(status === 'completed'
      ? 'Notion connected to Vault'
      : 'Notion could not connect to Vault')
    expect(container.textContent).not.toContain('internal agent instruction')
    expect(container.querySelector('[role="status"]')).not.toBeNull()
  }
  await act(async () => root.unmount())
})

it('shows private sign-in results and keeps unrelated synthetic events hidden', async () => {
  const container = document.createElement('div')
  const root = createRoot(container)
  for (const status of ['completed', 'failed']) {
    const event = {
      type: 'synthetic_turn_ready', data: { type: 'synthetic_turn_ready', data: {
        agent_id: 'private-oauth:notion:attempt-two', name: 'notion', status,
        message: 'internal instruction containing a private credential key',
      } },
    } as unknown as PollingEvent
    await act(async () => root.render(<EventDispatcher event={event} />))
    expect(container.textContent).toContain(status === 'completed' ? 'notion sign-in completed' : 'notion sign-in failed or expired')
    expect(container.textContent).not.toContain('private credential key')
    await act(async () => root.render(<EventDispatcher event={{ ...event, data: { type: 'synthetic_turn_ready', data: { agent_id: 'background-task', status } } } as unknown as PollingEvent} />))
    expect(container.textContent).toBe('')
  }
  await act(async () => root.unmount())
})
