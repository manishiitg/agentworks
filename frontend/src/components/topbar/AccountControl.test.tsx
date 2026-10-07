// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: vi.fn() }))
vi.mock('./ChangePasswordDialog', () => ({ default: () => null }))
import { useAuthStore } from '../../stores/useAuthStore'
import { TooltipProvider } from '../ui/tooltip'
import AccountControl from './AccountControl'
import { APP_VERSION } from '../../version'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
beforeEach(() => {
  const values = new Map<string, string>()
  vi.stubGlobal('localStorage', {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
  })
})
afterEach(() => vi.unstubAllGlobals())

describe('Account menu availability', () => {
  it.each([false, true])('keeps token controls out of the account menu in multi-user mode %s', async (isMultiUserMode) => {
    vi.mocked(useAuthStore).mockReturnValue({ user: { id: 'user', username: 'Alex' }, isMultiUserMode, logout: vi.fn() })
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    try {
      await act(async () => root.render(<TooltipProvider><AccountControl /></TooltipProvider>))
      const trigger = host.querySelector('button')!
      expect(trigger.getAttribute('aria-label')).toBe(isMultiUserMode ? 'Account: Alex' : 'Account: Local account')
      await act(async () => trigger.click())
      expect(host.textContent).toContain(`AgentWorks v${APP_VERSION}`)
      expect(host.textContent).not.toContain('Access tokens')
      expect(host.textContent?.includes('Change password')).toBe(isMultiUserMode)
      expect(host.textContent).not.toContain('Users & access')
      expect(host.textContent?.includes('Sign out')).toBe(isMultiUserMode)
    } finally { await act(async () => root.unmount()); host.remove() }
  })
  it('keeps user management out of the account menu even for an admin', async () => {
    vi.mocked(useAuthStore).mockReturnValue({ user: { id: 'admin', username: 'Owner', is_admin: true }, isMultiUserMode: true, logout: vi.fn() })
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    try {
      await act(async () => root.render(<TooltipProvider><AccountControl /></TooltipProvider>))
      await act(async () => host.querySelector('button')!.click())
      expect(host.textContent).not.toContain('Users & access')
    } finally { await act(async () => root.unmount()); host.remove() }
  })
  it('opens help and keyboard shortcuts from the menu and closes it after selection', async () => {
    vi.mocked(useAuthStore).mockReturnValue({ user: { id: 'user', username: 'Alex' }, isMultiUserMode: false, logout: vi.fn() })
    const onOpenWalkthrough = vi.fn()
    const onOpenShortcuts = vi.fn()
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    try {
      await act(async () => root.render(<TooltipProvider><AccountControl onOpenWalkthrough={onOpenWalkthrough} onOpenShortcuts={onOpenShortcuts} /></TooltipProvider>))
      expect(host.textContent).not.toContain('Keyboard shortcuts')
      await act(async () => host.querySelector('button')!.click())
      await act(async () => Array.from(host.querySelectorAll('button')).find(b => b.textContent?.includes('Keyboard shortcuts'))!.click())
      expect(onOpenShortcuts).toHaveBeenCalledOnce()
      expect(host.querySelector('[role="menu"]')).toBeNull()
      await act(async () => host.querySelector('button')!.click())
      await act(async () => Array.from(host.querySelectorAll('button')).find(b => b.textContent?.includes('Help & walkthrough'))!.click())
      expect(onOpenWalkthrough).toHaveBeenCalledOnce()
      expect(host.querySelector('[role="menu"]')).toBeNull()
    } finally { await act(async () => root.unmount()); host.remove() }
  })
  it('waits for the local account session to initialize', async () => {
    vi.mocked(useAuthStore).mockReturnValue({ user: null, isMultiUserMode: false, logout: vi.fn() })
    const host = document.createElement('div'); const root = createRoot(host)
    try {
      await act(async () => root.render(<TooltipProvider><AccountControl /></TooltipProvider>))
      expect(host.querySelector('button')).toBeNull()
    } finally { await act(async () => root.unmount()) }
  })
})
