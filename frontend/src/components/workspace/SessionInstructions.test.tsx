// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
const get = vi.hoisted(() => vi.fn())
vi.mock('../../services/api', () => ({ default: { get } }))
import { SessionInstructions } from './SessionInstructions'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
it('reads only the selected chat snapshot and closes on a chat switch', async () => {
  get.mockResolvedValue({ data: { content: '## Relay Builder\nUse relay-builder' } })
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<SessionInstructions sessionId="relay/chat" />))
    await act(async () => host.querySelector<HTMLButtonElement>('button')!.click())
    expect(get).toHaveBeenCalledWith('/api/sessions/relay%2Fchat/instructions')
    const dialog = document.querySelector('[role="dialog"]')!
    expect(dialog.textContent).toContain('Use relay-builder')
    expect(dialog.querySelector('textarea,input')).toBeNull()
    await act(async () => root.render(<SessionInstructions sessionId="other-chat" />))
    expect(document.querySelector('[role="dialog"]')).toBeNull()
  } finally { await act(async () => root.unmount()); host.remove() }
})
