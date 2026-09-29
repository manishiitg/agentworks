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
import UsersControl from './UsersControl'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => useAppStore.setState({ adminPage: null, showSchedulesOverview: false, showWorkflowsOverview: false }))

async function render(state: unknown) {
  vi.mocked(useAuthStore).mockReturnValue(state as ReturnType<typeof useAuthStore>)
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  await act(async () => root.render(<TooltipProvider><UsersControl /></TooltipProvider>))
  return { host, cleanup: async () => { await act(async () => root.unmount()); host.remove() } }
}

describe('Users control in the top bar', () => {
  it('shows only to an admin on a multi-user server, and opens the Users page', async () => {
    useAppStore.setState({ showSchedulesOverview: true })
    const { host, cleanup } = await render({ user: { id: 'a', username: 'Owner', is_admin: true }, isMultiUserMode: true })
    try {
      const button = host.querySelector('button[aria-label="Users and access"]') as HTMLButtonElement
      expect(button).not.toBeNull()
      await act(async () => button.click())
      // A full page, and only one at a time: Schedules closes.
      expect(useAppStore.getState().adminPage).toBe('users')
      expect(useAppStore.getState().showSchedulesOverview).toBe(false)
    } finally { await cleanup() }
  })
  it.each([
    ['a member', { user: { id: 'm', username: 'Member' }, isMultiUserMode: true }],
    ['an admin of a single-user install', { user: { id: 'a', username: 'Owner', is_admin: true }, isMultiUserMode: false }],
    ['nobody signed in', { user: null, isMultiUserMode: true }],
  ])('stays hidden for %s', async (_name, state) => {
    const { host, cleanup } = await render(state)
    try {
      expect(host.querySelector('button')).toBeNull()
    } finally { await cleanup() }
  })
})
