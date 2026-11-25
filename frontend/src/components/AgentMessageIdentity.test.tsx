// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { beforeEach, expect, it, vi } from 'vitest'
import { AgentMessageIdentity } from './AgentMessageIdentity'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('../services/api', () => ({ default: { get } }))
beforeEach(() => get.mockReset())
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// The row must never use its raw address/transport as a visible name; resolving
// again when the chat is shown picks up a renamed agent without sending to it.
it('shows current server names and keeps raw addresses in the tooltip', async () => {
  get.mockResolvedValueOnce({ data: { label: 'Example Person · Reviewer', address: 'agent:app-opaque' } })
    .mockResolvedValueOnce({ data: { label: 'Example Person · Updated reviewer', address: 'agent:app-opaque' } })
  const host = document.createElement('div'), root = createRoot(host)
  try {
    await act(async () => root.render(<AgentMessageIdentity inboxID="inbox-example" messageID="message" sent />))
    expect(host.querySelector('.truncate')?.textContent).toBe('You → Example Person · Reviewer')
    expect(host.textContent).not.toContain('app-opaque')
    await act(async () => { window.dispatchEvent(new Event('focus')) })
    expect(host.querySelector('.truncate')?.textContent).toBe('You → Example Person · Updated reviewer')
    expect(host.querySelector('[title="agent:app-opaque"]')).not.toBeNull()
    expect(get).toHaveBeenCalledTimes(2)
  } finally { await act(async () => root.unmount()) }
})
