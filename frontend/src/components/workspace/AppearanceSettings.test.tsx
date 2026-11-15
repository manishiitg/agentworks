// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import AppearanceSettings from './AppearanceSettings'
import { useAppearanceStore } from '../../stores/useAppearanceStore'
import { defaultAppearance, type AppearanceDocument } from '../../stores/appearanceTheme'
import { ThemeProvider } from '../../contexts/ThemeContext.tsx'
const fixture = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), actor: 'owner', server: 'local', wake: undefined as undefined | (() => void) }))
vi.mock('../../services/api', () => ({ default: { get: fixture.get, put: fixture.put } }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: (state: unknown) => unknown) => select({ user: { id: fixture.actor } }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: (select: (state: unknown) => unknown) => select({ activeWorkspaceId: fixture.server }) }))
vi.mock('../../services/liveFeed', () => ({ liveFeed: { subscribe: (_: unknown, __: unknown, cb: () => void) => { fixture.wake = cb; return () => {} } } }))
let root: ReturnType<typeof createRoot>
let host: HTMLDivElement
const click = async (label: string) => act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === label)!.click())
const select = async (label: string, value: string) => act(async () => {
  const control = [...host.querySelectorAll('select')].find(el => el.closest('label')?.textContent?.includes(label))!
  control.value = value;control.dispatchEvent(new Event('change', { bubbles: true }))
})
afterEach(() => { act(() => root?.unmount());host?.remove();fixture.get.mockReset();fixture.put.mockReset();fixture.actor = 'owner';fixture.server = 'local' })

// Real settings/provider/store: edits preview globally, explicit save persists,
// personal live notices refresh, and a different login cannot retain old themes.
it('previews typography and colours, saves through the personal endpoint and resets on account change', async () => {
  let doc: AppearanceDocument = { current: defaultAppearance(), saved: [] }
  fixture.get.mockImplementation(async () => ({ data: structuredClone(doc) }))
  fixture.put.mockImplementation(async (_path, body) => {
    if (body.theme) doc.current = structuredClone(body.theme)
    if (body.operation === 'save') doc.saved = [structuredClone(doc.current)]
    if (body.operation === 'reset') doc.current = defaultAppearance()
    return { data: structuredClone(doc) }
  })
  host = document.createElement('div');document.body.appendChild(host);root = createRoot(host)
  const render = () => root.render(<ThemeProvider><AppearanceSettings /></ThemeProvider>)
  await act(async () => render())
  await select('Interface font', 'fira-code')
  await select('Code font', 'ibm-plex-mono')
  expect(document.documentElement.style.getPropertyValue('--app-font')).toContain('Fira Code')
  expect(document.documentElement.style.getPropertyValue('--app-code-font')).toContain('IBM Plex Mono')
  expect(fixture.put).not.toHaveBeenCalled()
  await act(async () => { const input = host.querySelector<HTMLInputElement>('[aria-label="Page background"]')!;Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, '#102030'); input.dispatchEvent(new Event('change', { bubbles: true })) })
  expect(document.documentElement.style.getPropertyValue('--background')).not.toBe('')
  await click('Save theme')
  expect(fixture.put).toHaveBeenCalledWith('/api/appearance', expect.objectContaining({ operation: 'save', replace: true, theme: expect.objectContaining({ interface_font: 'fira-code', code_font: 'ibm-plex-mono' }) }), { skipSessionContext: true })
  expect(useAppearanceStore.getState().preview).toBeNull()
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Reset Page background"]')!.click())
  await click('Apply')
  expect(doc.current.colors.dark?.background).toBeUndefined()
  expect(fixture.put).toHaveBeenLastCalledWith('/api/appearance', expect.objectContaining({ operation: 'apply', replace: true }), { skipSessionContext: true })
  expect(document.documentElement.style.getPropertyValue('--background')).toBe('')
  await select('Interface font', 'source-code-pro')
  await click('Discard preview')
  expect(document.documentElement.style.getPropertyValue('--app-font')).toContain('Fira Code')
  doc.current = { ...doc.current, interface_font: 'jetbrains-mono' }
  await act(async () => { fixture.wake?.(); await Promise.resolve() })
  expect(document.documentElement.style.getPropertyValue('--app-font')).toContain('JetBrains Mono')
  fixture.get.mockRejectedValue(new Error('offline'))
  fixture.actor = 'another-person'
  await act(async () => render())
  expect(useAppearanceStore.getState().saved).toEqual([])
  expect(document.documentElement.style.getPropertyValue('--app-font')).toContain('system-ui')
  expect(document.documentElement.style.getPropertyValue('--background')).toBe('')
})
