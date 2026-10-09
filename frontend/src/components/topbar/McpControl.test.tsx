// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
const memoryStorage = vi.hoisted(() => {
  const values = new Map<string, string>()
  const storage = {
    get length() { return values.size },
    clear: () => values.clear(),
    getItem: (key: string) => values.get(key) ?? null,
    key: (index: number) => Array.from(values.keys())[index] ?? null,
    removeItem: (key: string) => { values.delete(key) },
    setItem: (key: string, value: string) => { values.set(key, value) },
  }
  Object.defineProperty(globalThis, 'localStorage', { value: storage, configurable: true })
  return storage
})
void memoryStorage
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: vi.fn() }))
import { useAuthStore } from '../../stores/useAuthStore'
import { useAppStore } from '../../stores/useAppStore'
import { TooltipProvider } from '../ui/tooltip'
import McpControl from './McpControl'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => useAppStore.setState({ adminPage: null }))

async function render(state: unknown) {
  // The control reads its user through a selector.
  vi.mocked(useAuthStore).mockImplementation(((selector?: (s: unknown) => unknown) => (selector ? selector(state) : state)) as unknown as typeof useAuthStore)
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  await act(async () => root.render(<TooltipProvider><McpControl /></TooltipProvider>))
  return { host, cleanup: async () => { await act(async () => root.unmount()); host.remove() } }
}

describe('MCP connect control in the top bar', () => {
  it.each([
    ['an admin', { id: 'a', username: 'Owner', is_admin: true }],
    ['a Code reviewer', { id: 'r', username: 'Rev', is_code_reviewer: true }],
    ['an ordinary member', { id: 'm', username: 'Member' }],
  ])('shows for %s and opens the MCP page', async (_name, user) => {
    const { host, cleanup } = await render({ user, isMultiUserMode: true })
    try {
      const button = host.querySelector('button[aria-label="Connect an AI agent (MCP)"]') as HTMLButtonElement
      expect(button).not.toBeNull()
      await act(async () => button.click())
      expect(useAppStore.getState().adminPage).toBe('mcp')
    } finally { await cleanup() }
  })
  it('shows for an ordinary member too, and stays hidden when nobody is signed in', async () => {
    const member = await render({ user: { id: 'm', username: 'Member' }, isMultiUserMode: true })
    try { expect(member.host.querySelector('button[aria-label="Connect an AI agent (MCP)"]')).not.toBeNull() } finally { await member.cleanup() }
    const nobody = await render({ user: null, isMultiUserMode: true })
    try { expect(nobody.host.querySelector('button')).toBeNull() } finally { await nobody.cleanup() }
  })
})
