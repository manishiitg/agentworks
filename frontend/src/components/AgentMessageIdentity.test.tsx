import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, expect, it, vi } from 'vitest'
import { AgentMessageIdentity } from './AgentMessageIdentity'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('../services/api', () => ({ default: { get } }))
beforeEach(() => get.mockReset())

// The row must never use its raw address/transport as a visible name; resolving
// again when the chat is shown picks up a renamed agent without sending to it.
it('shows current server names and keeps raw addresses in the tooltip', async () => {
  get.mockResolvedValueOnce({ data: { label: 'Example Person · Reviewer', address: 'agent:app-opaque' } })
    .mockResolvedValueOnce({ data: { label: 'Example Person · Updated reviewer', address: 'agent:app-opaque' } })
  const { container } = render(<AgentMessageIdentity inboxID="inbox-example" messageID="message" sent />)
  await screen.findByText('You → Example Person · Reviewer')
  expect(container.textContent).not.toContain('app-opaque')
  fireEvent.focus(window)
  await screen.findByText('You → Example Person · Updated reviewer')
  expect(container.querySelector('[title="agent:app-opaque"]')).not.toBeNull()
  await waitFor(() => expect(get).toHaveBeenCalledTimes(2))
})
