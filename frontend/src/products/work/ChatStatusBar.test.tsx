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
import ChatStatusBar from './ChatStatusBar'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('loads context when opened mid-turn and shows the weekly reset even when the five-hour window is fuller', async () => {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<ChatStatusBar sessionId="chat" busy />))
    expect(read).toHaveBeenCalledWith('chat')
    expect(host.textContent).toContain('7d 13% (resets Nov 3)')
    const chip = host.querySelector('button')!
    expect(chip.getAttribute('aria-label')).toContain('context used')
    expect(chip.title).toContain('7d 13% (resets Nov 3')
  } finally {
    await act(async () => root.unmount()); host.remove()
  }
})
