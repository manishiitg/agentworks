// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import AppearanceSettings from './AppearanceSettings'
import AppearanceControl from './AppearanceControl'
import DeepSpaceBackground from '../appearance/DeepSpaceBackground'
import { useAppearanceStore } from '../../stores/useAppearanceStore'
import { defaultAppearance, type AppearanceDocument } from '../../stores/appearanceTheme'
import { ThemeProvider } from '../../contexts/ThemeContext.tsx'
const fixture = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), copy: vi.fn(), open: vi.fn(), actor: 'owner', server: 'local', wake: undefined as undefined | (() => void) }))
vi.mock('../../utils/workspaceSettingsNavigation',()=>({openWorkspaceSettings:fixture.open}))
vi.mock('../../services/api', () => ({ default: { get: fixture.get, put: fixture.put } }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: (state: unknown) => unknown) => select({ user: { id: fixture.actor } }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: (select: (state: unknown) => unknown) => select({ activeWorkspaceId: fixture.server }) }))
vi.mock('../../services/liveFeed', () => ({ liveFeed: { subscribe: (_: unknown, __: unknown, cb: () => void) => { fixture.wake = cb; return () => {} } } }))
let root: ReturnType<typeof createRoot>
let host: HTMLDivElement
const click = async (label: string) => act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === label)!.click())
afterEach(() => { act(() => root?.unmount());host?.remove();fixture.get.mockReset();fixture.put.mockReset();fixture.copy.mockReset();fixture.open.mockReset();fixture.actor = 'owner';fixture.server = 'local' })

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
  let doc: AppearanceDocument = { current: { ...defaultAppearance(),interface_font:'fira-code',colors:{dark:{background:'#102030',accent:'#aaffdd',border:'#112233'}} },saved:[] }
  fixture.get.mockImplementation(async () => ({ data: structuredClone(doc) }))
  host=document.createElement('div');document.body.appendChild(host);root=createRoot(host)
  const render=()=>root.render(<ThemeProvider><AppearanceSettings /></ThemeProvider>)
  await act(async()=>render())
  expect(document.documentElement.style.getPropertyValue('--app-font')).toContain('Fira Code')
  expect(document.documentElement.style.getPropertyValue('--primary-foreground')).toBe('0 0% 0%')
  expect(document.documentElement.style.getPropertyValue('--input')).toBe(document.documentElement.style.getPropertyValue('--border'))
  doc.current={...doc.current,interface_font:'jetbrains-mono'}
  await act(async()=>{fixture.wake?.();await Promise.resolve()})
  expect(document.documentElement.style.getPropertyValue('--app-font')).toContain('JetBrains Mono')
  fixture.get.mockRejectedValue(new Error('offline'));fixture.actor='another-person'
  await act(async()=>render())
  expect(useAppearanceStore.getState().saved).toEqual([])
  expect(document.documentElement.style.getPropertyValue('--app-font')).toContain('system-ui')
  expect(document.documentElement.style.getPropertyValue('--background')).toBe('')
  expect(document.documentElement.style.getPropertyValue('--input')).toBe('')
  expect(document.documentElement.style.getPropertyValue('--primary-foreground')).toBe('')
})

// The actual account control applies the same saved document as Appearance.
it('selects agent-saved themes and restores Default without deleting presets', async () => {
  const warm={...defaultAppearance(),name:'Warm',interface_font:'fira-code'}
  let doc:AppearanceDocument={current:warm,saved:[warm]}
  fixture.get.mockImplementation(async()=>({data:structuredClone(doc)}))
  fixture.put.mockImplementation(async (_:string,body:{operation:string;theme?:typeof warm})=>{doc.current=body.operation==='reset'?defaultAppearance():body.theme!;return {data:structuredClone(doc)}})
  host=document.createElement('div');document.body.appendChild(host);root=createRoot(host)
  await act(async()=>root.render(<ThemeProvider><AppearanceControl compact/><AppearanceSettings/></ThemeProvider>))
  const appearance = host.querySelector('summary')!
  expect(appearance.textContent).toContain('Warm')
  expect(appearance.parentElement?.hasAttribute('open')).toBe(false)
  await act(async () => (appearance as HTMLElement).click())
  expect(appearance.parentElement?.hasAttribute('open')).toBe(true)
  await click('Customize with an agent')
  expect(fixture.open).toHaveBeenCalledWith('connections','appearance')
  await click('Default')
  expect(fixture.put).toHaveBeenLastCalledWith('/api/appearance',{operation:'reset'},{skipSessionContext:true})
  expect(useAppearanceStore.getState().saved[0].name).toBe('Warm')
  const warmButton=[...host.querySelectorAll<HTMLButtonElement>('details button')].find(button=>button.textContent==='Warm')!
  await act(async()=>warmButton.click())
  expect(fixture.put).toHaveBeenLastCalledWith('/api/appearance',{operation:'apply',theme:warm,replace:true},{skipSessionContext:true})
  await click('Try Neon Orbit')
  expect(useAppearanceStore.getState().current.scene).toBe('neon-orbit')
  expect(useAppearanceStore.getState().saved.some(theme=>theme.name==='Warm')).toBe(true)
})

// Hidden pages must freeze the decorative scene; leaving it must remove its listener.
it('pauses the passive space scene on document hiding and releases its visibility subscription', async () => {
  const original = Object.getOwnPropertyDescriptor(document, 'visibilityState')
  const add = vi.spyOn(document, 'addEventListener')
  const remove = vi.spyOn(document, 'removeEventListener')
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host)
  try {
    await act(async () => root.render(<DeepSpaceBackground />))
    const scene = document.querySelector('.aw-deep-space')!
    expect(scene.getAttribute('data-visible')).toBe('true')
    expect(scene.querySelector('canvas,button,input,[tabindex]')).toBeNull()
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' })
    await act(async () => document.dispatchEvent(new Event('visibilitychange')))
    expect(scene.getAttribute('data-visible')).toBe('false')
    const subscription = add.mock.calls.find(([type]) => type === 'visibilitychange')!
    await act(async () => root.render(null))
    expect(remove).toHaveBeenCalledWith('visibilitychange', subscription[1])
    expect(document.querySelector('.aw-deep-space')).toBeNull()
  } finally {
    if (original) Object.defineProperty(document, 'visibilityState', original)
    else Reflect.deleteProperty(document, 'visibilityState')
    add.mockRestore(); remove.mockRestore()
  }
})

// Real failure: scene:none + a custom background stayed behind opaque Light wrappers;
// changing only scene/material fields also did not rerun the scene projection.
it('adopts animated CSS and projects translucent surfaces without a shipped scene in Light', async () => {
  const css = '@keyframes theme-drift { from { background-position: 0% 50%; } to { background-position: 100% 50%; } } @media (prefers-reduced-motion: no-preference) { [data-app-main] { animation: theme-drift 6s linear infinite alternate; } }'
  const doc: AppearanceDocument = { current: { ...defaultAppearance(), mode: 'light', css, materials: { panel: { opacity: .6 } } }, saved: [] }
  fixture.get.mockImplementation(async () => ({ data: structuredClone(doc) }))
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host)
  await act(async () => root.render(<ThemeProvider><AppearanceSettings /></ThemeProvider>))
  expect(document.adoptedStyleSheets.some(sheet => [...sheet.cssRules].some(rule => rule.cssText.includes('@keyframes theme-drift')))).toBe(true)
  expect(document.documentElement.dataset.appearanceScene).toBe('none')
  expect(document.documentElement.hasAttribute('data-appearance-scene-active')).toBe(true)
  expect(document.documentElement.style.getPropertyValue('--aw-panel-opacity')).toBe('0.6')
  doc.current.materials = { panel: { opacity: 1 } }
  await act(async () => { fixture.wake?.(); await Promise.resolve() })
  expect(document.documentElement.hasAttribute('data-appearance-scene-active')).toBe(false)
  expect(document.adoptedStyleSheets.some(sheet => [...sheet.cssRules].some(rule => rule.cssText.includes('@keyframes theme-drift')))).toBe(true)
})
