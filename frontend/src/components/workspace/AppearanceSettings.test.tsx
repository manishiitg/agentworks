// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import AppearanceSettings from './AppearanceSettings'
import AppearanceControl from './AppearanceControl'
import { useAppearanceStore } from '../../stores/useAppearanceStore'
import { defaultAppearance, type AppearanceDocument } from '../../stores/appearanceTheme'
import { ThemeProvider } from '../../contexts/ThemeContext.tsx'
const fixture = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), ask: vi.fn(), actor: 'owner', server: 'local', wake: undefined as undefined | (() => void) }))
vi.mock('../../utils/appearanceAgentDraft',()=>({askAppearanceAgent:fixture.ask}))
vi.mock('../../services/api', () => ({ default: { get: fixture.get, post: fixture.post, put: fixture.put } }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: (state: unknown) => unknown) => select({ user: { id: fixture.actor } }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: (select: (state: unknown) => unknown) => select({ activeWorkspaceId: fixture.server }) }))
vi.mock('../../services/liveFeed', () => ({ liveFeed: { subscribe: (_: unknown, __: unknown, cb: () => void) => { fixture.wake = cb; return () => {} } } }))
let root: ReturnType<typeof createRoot>
let host: HTMLDivElement
const click = async (label: string) => act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === label)!.click())
afterEach(() => { act(() => root?.unmount());host?.remove();fixture.get.mockReset();fixture.put.mockReset();fixture.post.mockReset();fixture.ask.mockReset();fixture.actor = 'owner';fixture.server = 'local' })

// Agentic-first appearance: only the personal picker and an own-chat draft action.
it('keeps customization out of Settings and drafts through the agent action without saving a theme', async () => {
  fixture.get.mockResolvedValue({ data: { current: defaultAppearance(), saved: [] } })
  fixture.put.mockResolvedValue({ data: { current: defaultAppearance(), saved: [] } })
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host)
  await act(async () => root.render(<ThemeProvider><AppearanceSettings /></ThemeProvider>))
  expect(host.querySelector('textarea, select, input:not([type="file"])')).toBeNull()
  expect(host.textContent).not.toContain('Neon Orbit')
  await click('Ask an agent to change the look')
  expect(fixture.ask).toHaveBeenCalledOnce()
  expect(fixture.put).not.toHaveBeenCalled()
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

// The same real picker in Settings and account menu clears old overrides on Default
// and deletes only a saved name; the current applied look is not silently reset.
it('applies either default and saved themes through the same picker and deletes a saved theme', async () => {
  const warm={...defaultAppearance(),name:'Warm',interface_font:'fira-code'}
  let doc:AppearanceDocument={current:warm,saved:[warm]}
  fixture.get.mockImplementation(async()=>({data:structuredClone(doc)}))
  fixture.put.mockImplementation(async (_:string,body:{operation:string;theme?:typeof warm;name?:string})=>{
    if (body.operation==='delete') doc.saved=doc.saved.filter(theme=>theme.name!==body.name)
    else doc.current=body.theme!
    return {data:structuredClone(doc)}
  })
  host=document.createElement('div');document.body.appendChild(host);root=createRoot(host)
  await act(async()=>root.render(<ThemeProvider><AppearanceControl compact/></ThemeProvider>))
  const appearance=host.querySelector('summary')!
  expect(appearance.parentElement?.hasAttribute('open')).toBe(false)
  await act(async()=>(appearance as HTMLElement).click())
  await click('Default Light')
  expect(fixture.put).toHaveBeenLastCalledWith('/api/appearance',{operation:'apply',theme:{...defaultAppearance(),mode:'light'},replace:true},{skipSessionContext:true})
  expect(useAppearanceStore.getState().saved[0].name).toBe('Warm')
  await click('Default Dark')
  expect(useAppearanceStore.getState().current).toEqual(defaultAppearance())
  await click('Warm')
  expect(fixture.put).toHaveBeenLastCalledWith('/api/appearance',{operation:'apply',theme:warm,replace:true},{skipSessionContext:true})
  await act(async()=>host.querySelector<HTMLButtonElement>('[aria-label="Delete saved theme Warm"]')!.click())
  expect(fixture.put).toHaveBeenLastCalledWith('/api/appearance',{operation:'delete',name:'Warm'},{skipSessionContext:true})
  expect(useAppearanceStore.getState().saved).toEqual([])
  expect(useAppearanceStore.getState().current).toEqual(warm)
})

// Real failure: a custom background stayed behind opaque Light wrappers;
// changing only scene/material fields also did not rerun the scene projection.
it('adopts animated CSS and projects translucent surfaces without a shipped scene in Light', async () => {
  const css = '@keyframes theme-drift { from { background-position: 0% 50%; } to { background-position: 100% 50%; } } @media (prefers-reduced-motion: no-preference) { [data-app-main] { animation: theme-drift 6s linear infinite alternate; } }'
  const doc: AppearanceDocument = { current: { ...defaultAppearance(), mode: 'light', css, materials: { panel: { opacity: .6 } } }, saved: [] }
  fixture.get.mockImplementation(async () => ({ data: structuredClone(doc) }))
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host)
  await act(async () => root.render(<ThemeProvider><AppearanceSettings /></ThemeProvider>))
  expect(document.adoptedStyleSheets.some(sheet => [...sheet.cssRules].some(rule => rule.cssText.includes('@keyframes theme-drift')))).toBe(true)
  expect(document.documentElement.hasAttribute('data-appearance-scene')).toBe(false)
  expect(document.documentElement.hasAttribute('data-appearance-backdrop')).toBe(true)
  expect(document.documentElement.style.getPropertyValue('--aw-panel-opacity')).toBe('0.6')
  doc.current.materials = { panel: { opacity: 1 } }
  await act(async () => { fixture.wake?.(); await Promise.resolve() })
  expect(document.documentElement.hasAttribute('data-appearance-backdrop')).toBe(false)
  expect(document.adoptedStyleSheets.some(sheet => [...sheet.cssRules].some(rule => rule.cssText.includes('@keyframes theme-drift')))).toBe(true)
})

// Custom theme motion needs the real document visibility, without another listener.
// Keep the generic flag accurate when hidden and remove it on account teardown.
it('projects document visibility for personal CSS and clears it on unmount', async () => {
  const original = Object.getOwnPropertyDescriptor(document, 'visibilityState')
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
  const css = '@keyframes custom-transit { to { transform: translateX(10px); } } @media (prefers-reduced-motion: no-preference) { html[data-appearance-visible="true"] [data-app-ambient]::before { animation: custom-transit 12s infinite alternate; } }'
  fixture.get.mockResolvedValue({ data: { current: { ...defaultAppearance(), mode: 'light', css }, saved: [] } })
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host)
  try {
    await act(async () => root.render(<ThemeProvider><AppearanceSettings /></ThemeProvider>))
    expect(document.documentElement.dataset.appearanceVisible).toBe('true')
    expect(document.documentElement.dataset.appearance).toBe('light')
    expect(document.documentElement.hasAttribute('data-appearance-scene')).toBe(false)
    expect(document.adoptedStyleSheets.some(sheet => [...sheet.cssRules].some(rule => rule.cssText.includes('custom-transit')))).toBe(true)
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' })
    await act(async () => document.dispatchEvent(new Event('visibilitychange')))
    expect(document.documentElement.dataset.appearanceVisible).toBe('false')
    await act(async () => root.render(null))
    expect(document.documentElement.hasAttribute('data-appearance-visible')).toBe(false)
    expect(document.adoptedStyleSheets.some(sheet => [...sheet.cssRules].some(rule => rule.cssText.includes('custom-transit')))).toBe(false)
  } finally {
    if (original) Object.defineProperty(document, 'visibilityState', original)
    else Reflect.deleteProperty(document, 'visibilityState')
  }
})

// #2813: choosing bytes uses the existing current personal theme and does not
// expose a Downloads path or wipe unrelated agent-made CSS/colours.
it('applies a selected wallpaper through own upload while preserving the current look', async()=>{
 const original={...defaultAppearance(),name:'Personal',css:'[data-app-main]{color:inherit}',colors:{dark:{accent:'#123456'}}}
 fixture.get.mockResolvedValue({data:{current:original,saved:[]}})
 fixture.post.mockResolvedValue({data:{asset_id:'a'.repeat(64)}})
 fixture.put.mockImplementation(async(_url:string,body:any)=>({data:{current:body.theme,saved:[]}}))
 host=document.createElement('div');document.body.appendChild(host);root=createRoot(host)
 await act(async()=>root.render(<ThemeProvider><AppearanceSettings/></ThemeProvider>))
 const input=host.querySelector<HTMLInputElement>('input[type="file"]')!
 const file=new File(['chosen bytes'],'wallpaper.jpg',{type:'image/jpeg'})
 Object.defineProperty(input,'files',{configurable:true,value:[file]})
 await act(async()=>{input.dispatchEvent(new Event('change',{bubbles:true}));await Promise.resolve();await Promise.resolve()})
 expect(fixture.post.mock.calls[0][0]).toBe('/api/appearance/images')
 expect((fixture.post.mock.calls[0][1] as FormData).get('image')).toBe(file)
 const applied=fixture.put.mock.calls.at(-1)![1].theme
 expect(applied.css).toBe(original.css);expect(applied.colors).toEqual(original.colors)
 expect(applied.background_image).toMatchObject({asset_id:'a'.repeat(64),fit:'cover',position_x:50,position_y:50})
 expect(applied.background_video).toBeNull()
})
