// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: vi.fn() }))
vi.mock('./McpConnectDialog', () => ({ default: ({ codeReview }: { codeReview: boolean }) => <div role="dialog">mcp dialog review={String(codeReview)}</div> }))
import { useAuthStore } from '../../stores/useAuthStore'
import { TooltipProvider } from '../ui/tooltip'
import McpControl from './McpControl'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

async function render(state: unknown) {
  vi.mocked(useAuthStore).mockReturnValue(state as ReturnType<typeof useAuthStore>)
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  await act(async () => root.render(<TooltipProvider><McpControl /></TooltipProvider>))
  return { host, cleanup: async () => { await act(async () => root.unmount()); host.remove() } }
}

describe('MCP connect control in the top bar', () => {
  it.each([
    ['an admin', { id: 'a', username: 'Owner', is_admin: true }, 'true'],
    ['a Code reviewer', { id: 'r', username: 'Rev', is_code_reviewer: true }, 'true'],
  ])('shows for %s and opens the connect dialog', async (_name, user, review) => {
    const { host, cleanup } = await render({ user, isMultiUserMode: true })
    try {
      const button = host.querySelector('button[aria-label="Connect an AI agent (MCP)"]') as HTMLButtonElement
      expect(button).not.toBeNull()
      await act(async () => button.click())
      await act(async () => { await Promise.resolve() })
      expect(document.body.textContent).toContain(`mcp dialog review=${review}`)
    } finally { await cleanup() }
  })
  it('stays hidden for an ordinary member and when nobody is signed in', async () => {
    for (const state of [{ user: { id: 'm', username: 'Member' }, isMultiUserMode: true }, { user: null, isMultiUserMode: true }]) {
      const { host, cleanup } = await render(state)
      try { expect(host.querySelector('button')).toBeNull() } finally { await cleanup() }
    }
  })
})
