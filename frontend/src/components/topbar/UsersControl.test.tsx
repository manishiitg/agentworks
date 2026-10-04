// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi, afterAll } from 'vitest'
vi.hoisted(() => vi.stubGlobal('localStorage', { getItem: () => null, setItem: () => {}, removeItem: () => {} }))
const state = vi.hoisted(() => ({ user: null as null | { is_admin: boolean }, isMultiUserMode: false }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (selector: (s: typeof state) => unknown) => selector(state) }))
const setShowLLMModal = vi.hoisted(() => vi.fn())
vi.mock('../../stores/useLLMStore', () => ({ useLLMStore: { getState: () => ({ setShowLLMModal }) } }))
import UsersControl from './UsersControl'
import { TooltipProvider } from '../ui/tooltip'
import { ProductTopBar } from '../workspace/ProductTopBar'
import { useAppStore } from '../../stores/useAppStore'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterAll(() => vi.unstubAllGlobals())
it.each([false, true])('opens the shared users page for an admin in multi-user mode %s', async (multiUser) => {
  state.user = { is_admin: true }; state.isMultiUserMode = multiUser
  useAppStore.setState({ adminPage: null, showSchedulesOverview: true })
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  try {
    await act(async () => root.render(<ProductTopBar><TooltipProvider><UsersControl /></TooltipProvider></ProductTopBar>))
    const button = host.querySelector<HTMLButtonElement>('[aria-label="Users and access"]')!
    expect(button).not.toBeNull()
    await act(async () => button.click())
    expect(useAppStore.getState().adminPage).toBe('users')
    expect(useAppStore.getState().showSchedulesOverview).toBe(false)
    expect(setShowLLMModal).toHaveBeenCalledWith(false)
    expect(button.getAttribute('aria-pressed')).toBe('true')
  } finally { await act(async () => root.unmount()); host.remove() }
})
it.each([null, { is_admin: false }])('hides account management for an unauthorized identity', async (user) => {
  state.user = user
  const host = document.createElement('div'); const root = createRoot(host)
  try {
    await act(async () => root.render(<TooltipProvider><UsersControl /></TooltipProvider>))
    expect(host.querySelector('button')).toBeNull()
  } finally { await act(async () => root.unmount()) }
})
