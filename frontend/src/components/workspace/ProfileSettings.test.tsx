// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import ProfileSettings from './ProfileSettings'
const state = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), set: vi.fn() }))
vi.mock('../../services/api', () => ({ default: { get: state.get, put: state.put } }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: Object.assign((select: (s: unknown) => unknown) => select({ user: { id: 'owner', username: 'Example' } }), { setState: state.set }) }))
vi.mock('../ui/Avatar', () => ({ Avatar: () => <span /> }))
let root: ReturnType<typeof createRoot>, host: HTMLDivElement
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => { act(() => root?.unmount()); host?.remove(); vi.clearAllMocks() })
// The settings screen and chat change the caller's account through the same
// service; the screen never chooses another person or mutates Team access.
it('saves an offered icon and removes it without a target user or project', async () => {
  state.get.mockResolvedValue({ data: {} }); state.put.mockResolvedValue({ data: { avatar_icon: 'rocket', avatar_color: 'blue' } })
  host = document.createElement('div'); document.body.append(host); root = createRoot(host)
  await act(async () => root.render(<ProfileSettings />))
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Use rocket icon"]')!.click())
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Save picture')!.click())
  expect(state.put.mock.calls[0]).toEqual(['/api/account/avatar', { operation: 'set', icon: 'rocket', color: 'blue' }, { skipSessionContext: true }])
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Remove picture')!.click())
  expect(state.put.mock.calls[1][1]).toEqual({ operation: 'remove' }); expect(state.set).toHaveBeenCalled()
})
