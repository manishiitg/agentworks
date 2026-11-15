// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import AppearanceSettings from './AppearanceSettings'
import { useAppearanceStore } from '../../stores/useAppearanceStore'
import { defaultAppearance, type AppearanceDocument } from '../../stores/appearanceTheme'
import { ThemeProvider } from '../../contexts/ThemeContext.tsx'
const fixture = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), copy: vi.fn(), actor: 'owner', server: 'local', wake: undefined as undefined | (() => void) }))
vi.mock('../../services/api', () => ({ default: { get: fixture.get, put: fixture.put } }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: (state: unknown) => unknown) => select({ user: { id: fixture.actor } }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: (select: (state: unknown) => unknown) => select({ activeWorkspaceId: fixture.server }) }))
vi.mock('../../services/liveFeed', () => ({ liveFeed: { subscribe: (_: unknown, __: unknown, cb: () => void) => { fixture.wake = cb; return () => {} } } }))
let root: ReturnType<typeof createRoot>
let host: HTMLDivElement
const click = async (label: string) => act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === label)!.click())
afterEach(() => { act(() => root?.unmount());host?.remove();fixture.get.mockReset();fixture.put.mockReset();fixture.copy.mockReset();fixture.actor = 'owner';fixture.server = 'local' })

// The form was deliberately removed. People copy a request; the same personal
// backend and runtime styling remain authoritative for agents and Reset.
it('copies the editable appearance request and resets through the existing personal endpoint', async () => {
  fixture.get.mockResolvedValue({ data: { current: defaultAppearance(), saved: [] } })
  fixture.put.mockResolvedValue({ data: { current: defaultAppearance(), saved: [] } })
  fixture.copy.mockResolvedValue(undefined)
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: fixture.copy } })
  host = document.createElement('div');document.body.appendChild(host);root = createRoot(host)
  await act(async () => root.render(<ThemeProvider><AppearanceSettings /></ThemeProvider>))
  expect(host.querySelector('select, input[type="range"], input[type="color"]')).toBeNull()
  const prompt = host.querySelector<HTMLTextAreaElement>('#appearance-prompt')!
  const value = prompt.value.replace('[Describe the colours, fonts, spacing or background animation you want.]','A warm light theme with larger text.')
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(prompt,value);prompt.dispatchEvent(new Event('input',{bubbles:true})) })
  await click('Copy prompt')
  expect(fixture.copy).toHaveBeenCalledWith(value)
  expect(value).toContain('settings action=appearance')
  expect(fixture.put).not.toHaveBeenCalled()
  await click('Reset to default')
  expect(fixture.put).toHaveBeenCalledWith('/api/appearance', { operation:'reset' }, { skipSessionContext:true })
})

// Preserve the personal runtime boundary after removing its visual editors.
it('applies agent theme notices globally and clears them before switching accounts', async () => {
  let doc: AppearanceDocument = { current: { ...defaultAppearance(),interface_font:'fira-code',colors:{dark:{background:'#102030'}} },saved:[] }
  fixture.get.mockImplementation(async () => ({ data: structuredClone(doc) }))
  host=document.createElement('div');document.body.appendChild(host);root=createRoot(host)
  const render=()=>root.render(<ThemeProvider><AppearanceSettings /></ThemeProvider>)
  await act(async()=>render())
  expect(document.documentElement.style.getPropertyValue('--app-font')).toContain('Fira Code')
  doc.current={...doc.current,interface_font:'jetbrains-mono'}
  await act(async()=>{fixture.wake?.();await Promise.resolve()})
  expect(document.documentElement.style.getPropertyValue('--app-font')).toContain('JetBrains Mono')
  fixture.get.mockRejectedValue(new Error('offline'));fixture.actor='another-person'
  await act(async()=>render())
  expect(useAppearanceStore.getState().saved).toEqual([])
  expect(document.documentElement.style.getPropertyValue('--app-font')).toContain('system-ui')
  expect(document.documentElement.style.getPropertyValue('--background')).toBe('')
})
