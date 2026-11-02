// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import { useChatStore } from '../../stores/useChatStore'
import { ChatLinkStatus } from './ChatLinkStatus'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('shows the reconnect bar and the server-reported delivery state, nothing when live and unsent (PLAT-856-509)', async () => {
  const host = document.createElement('div'), root = createRoot(host)
  try {
    await act(async () => root.render(<ChatLinkStatus sessionId="s1" />))
    expect(host.textContent).toBe('')
    await act(async () => useChatStore.setState({ sseLink: { s1: 'reconnecting' }, messageStates: { s1: { submission_id: 'm1', state: 'received' } } }))
    expect(host.textContent).toContain('Reconnecting…')
    expect(host.textContent).toContain('Received by your Mac')
    await act(async () => useChatStore.setState({ sseLink: { s1: 'live' }, messageStates: { s1: { submission_id: 'm1', state: 'failed', reason: 'Your Mac is off' } } }))
    expect(host.textContent).not.toContain('Reconnecting')
    expect(host.textContent).toContain('Not delivered: Your Mac is off')
  } finally {
    await act(async () => root.unmount())
  }
})
