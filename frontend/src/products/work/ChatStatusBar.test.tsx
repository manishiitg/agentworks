// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'

const read = vi.hoisted(() => vi.fn(async () => ({
  session_id: 'chat', used_tokens: 200000, window_tokens: 1000000,
  limits: [
    { label: '5h', used_percent: 99, resets_at: '2026-10-30T12:00:00Z' },
    { label: '7d', used_percent: 13, resets_at: '2026-11-03T12:00:00Z' },
  ], can_compact: false, can_fresh: false,
})))
vi.mock('../../services/api', () => ({ agentApi: { getChatContext: read } }))
import { chatContextChangedEvent } from './ChatContextItems'
import ChatStatusBar from './ChatStatusBar'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('loads context when opened mid-turn and follows a context change from the menu', async () => {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<ChatStatusBar sessionId="chat" busy />))
    expect(read).toHaveBeenCalledWith('chat')
    const chip = host.querySelector('button')!
    expect(chip.textContent).toContain('200k')
    await act(async () => window.dispatchEvent(new CustomEvent(chatContextChangedEvent, { detail: { session_id: 'chat', used_tokens: 738693, can_compact: false, can_fresh: false } })))
    expect(chip.textContent).toContain('739k')
  } finally {
    await act(async () => root.unmount()); host.remove()
  }
})
