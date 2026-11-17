// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import AppearanceSettings from './AppearanceSettings'
import AppearanceControl from './AppearanceControl'
import {ProductSettingsMenu} from './ProductSettingsMenu'
import {useProductNavigationToolbarStore} from '../../stores/useProductNavigationToolbarStore'
import { useAppearanceStore } from '../../stores/useAppearanceStore'
import { defaultAppearance, type AppearanceDocument } from '../../stores/appearanceTheme'
import { ThemeProvider } from '../../contexts/ThemeContext.tsx'
const fixture = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), ask: vi.fn(), actor: 'owner', server: 'local', wake: undefined as undefined | (() => void) }))
vi.mock('../../utils/appearanceAgentDraft',()=>({askAppearanceAgent:fixture.ask}))
vi.mock('../../services/api', () => ({ default: { get: fixture.get, put: fixture.put } }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: (state: unknown) => unknown) => select({ user: { id: fixture.actor } }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: (select: (state: unknown) => unknown) => select({ activeWorkspaceId: fixture.server }) }))
vi.mock('../../services/liveFeed', () => ({ liveFeed: { subscribe: (_: unknown, __: unknown, cb: () => void) => { fixture.wake = cb; return () => {} } } }))
let root: ReturnType<typeof createRoot>
let host: HTMLDivElement
const click = async (label: string) => act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === label)!.click())
afterEach(() => { act(() => root?.unmount());host?.remove();fixture.get.mockReset();fixture.put.mockReset();fixture.ask.mockReset();fixture.actor = 'owner';fixture.server = 'local' })

// Only the requested typography controls join the personal picker and agent action.
it('offers only typography controls and drafts other customization through the agent action', async () => {
  fixture.get.mockResolvedValue({ data: { current: defaultAppearance(), saved: [] } })
  fixture.put.mockResolvedValue({ data: { current: defaultAppearance(), saved: [] } })
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host)
  await act(async () => root.render(<ThemeProvider><AppearanceSettings /></ThemeProvider>))
  expect(host.querySelector('textarea, input')).toBeNull()
  expect(host.querySelectorAll('select')).toHaveLength(1)
  expect(host.querySelector('[aria-label="Font"]')).not.toBeNull()
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


it('disables only the script background and preserves the saved theme library',async()=>{
 const theme={...defaultAppearance(),name:'Canvas',background_script:{code:'function render(){}',fps:20}}
 const doc:AppearanceDocument={current:theme,saved:[theme]}
 fixture.get.mockResolvedValue({data:doc})
 fixture.put.mockImplementation(async (_:string,body:{theme:typeof theme})=>({data:{current:body.theme,saved:doc.saved}}))
 useAppearanceStore.getState().resetScope('')
 host=document.createElement('div');document.body.append(host);root=createRoot(host)
 await act(async()=>root.render(<ThemeProvider><AppearanceSettings/></ThemeProvider>))
 await click('Disable script background')
 expect(fixture.put).toHaveBeenCalledWith('/api/appearance',expect.objectContaining({operation:'apply',replace:true,theme:expect.objectContaining({background_script:null,name:'Canvas'})}),{skipSessionContext:true})
 expect(useAppearanceStore.getState().saved[0].background_script).toEqual(theme.background_script)
})

// #2949: both entry points apply only typography, preserve the look, and roll
// back the preview if the server refuses a font or cannot save the preference.
it('changes text size and local font through apply in both pickers without replacing the look', async () => {
  let doc: AppearanceDocument = { current: { ...defaultAppearance(), name: 'Warm', code_font: 'fira-code', css: '[data-app-main]{color:inherit}', materials: { panel: { opacity: .8 } } }, saved: [] }
  doc.saved = [structuredClone(doc.current)]
  fixture.get.mockImplementation(async () => ({ data: structuredClone(doc) }))
  fixture.put.mockImplementation(async (_: string, body: { operation: string; theme: Partial<AppearanceDocument['current']>; replace: boolean }) => {
    doc.current = { ...doc.current, ...body.theme }
    doc.saved[0] = { ...doc.saved[0], ...body.theme }
    return { data: structuredClone(doc) }
  })
  host = document.createElement('div'); document.body.append(host); root = createRoot(host)
  for (const compact of [true, false]) {
    await act(async () => root.render(<ThemeProvider>{compact ? <AppearanceControl compact /> : <AppearanceSettings />}</ThemeProvider>))
    const size = doc.current.interface_size
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Increase text size"]')!.click())
    expect(fixture.put).toHaveBeenLastCalledWith('/api/appearance', { operation: 'apply', theme: { interface_size: size + 1 }, replace: false }, { skipSessionContext: true })
    const select = host.querySelector<HTMLSelectElement>('select[aria-label="Font"]')!
    await act(async () => { select.value = 'avenir-next'; select.dispatchEvent(new Event('change', { bubbles: true })) })
    expect(fixture.put).toHaveBeenLastCalledWith('/api/appearance', { operation: 'apply', theme: { interface_font: 'avenir-next' }, replace: false }, { skipSessionContext: true })
    expect(useAppearanceStore.getState().current.code_font).toBe('fira-code')
    expect(useAppearanceStore.getState().current.css).toBe('[data-app-main]{color:inherit}')
    expect(useAppearanceStore.getState().saved[0].interface_size).toBe(size + 1)
    expect(useAppearanceStore.getState().saved[0].interface_font).toBe('avenir-next')
  }
  fixture.put.mockRejectedValueOnce(new Error('server refused'))
  await act(async () => {
    const select = host.querySelector<HTMLSelectElement>('select[aria-label="Font"]')!
    select.value = 'georgia'; select.dispatchEvent(new Event('change', { bubbles: true }))
  })
  expect(useAppearanceStore.getState().current.interface_font).toBe('avenir-next')
  expect(useAppearanceStore.getState().preview).toBeNull()
  expect(host.querySelector('[role="alert"]')).not.toBeNull()
  for (const [size, label] of [[12, 'Decrease text size'], [20, 'Increase text size']] as const) {
    await act(async () => useAppearanceStore.setState({ current: { ...doc.current, interface_size: size } }))
    expect(host.querySelector<HTMLButtonElement>(`[aria-label="${label}"]`)?.disabled).toBe(true)
  }
})

// #2937: exercise the real menu portal and a physical click on the SVG path,
// not only button.click() on an isolated picker.
it('deletes from the account menu SVG and Settings without changing the current look',async()=>{
 const theme={...defaultAppearance(),name:'Delete fixture'}
 let doc:AppearanceDocument={current:theme,saved:[theme]}
 fixture.get.mockImplementation(async()=>({data:structuredClone(doc)}))
 fixture.put.mockImplementation(async(_:string,body:{operation:string;name?:string})=>{
  if(body.operation==='delete')doc.saved=doc.saved.filter(row=>row.name!==body.name)
  return {data:structuredClone(doc)}
 })
 host=document.createElement('div');document.body.appendChild(host);root=createRoot(host)
 const slot=document.createElement('div');document.body.appendChild(slot)
 useProductNavigationToolbarStore.setState({sidebarSettingsTarget:slot})
 try {
  await act(async()=>root.render(<ThemeProvider><ProductSettingsMenu accountTrigger={<span>Account</span>}><AppearanceControl compact/></ProductSettingsMenu></ThemeProvider>))
  await act(async()=>slot.querySelector<HTMLButtonElement>('[data-product-settings-trigger]')!.click())
  await act(async()=>slot.querySelector('summary')!.click())
  const trash=slot.querySelector<HTMLButtonElement>('[aria-label="Delete saved theme Delete fixture"]')!
  expect(trash.disabled).toBe(false)
  const path=trash.querySelector('path')!
  await act(async()=>{
   for(const name of ['pointerdown','mousedown','pointerup','mouseup','click'])path.dispatchEvent(new MouseEvent(name,{bubbles:true}))
  })
  expect(fixture.put).toHaveBeenCalledWith('/api/appearance',{operation:'delete',name:theme.name},{skipSessionContext:true})
  expect(useAppearanceStore.getState().current).toEqual(theme)
  expect(useAppearanceStore.getState().saved).toEqual([])
  // Same saved library and click path in Settings, without the menu wrapper.
  doc.saved=[theme]
  useAppearanceStore.setState({saved:[theme]})
  await act(async()=>root.render(<ThemeProvider><AppearanceSettings/></ThemeProvider>))
  await act(async()=>host.querySelector<HTMLButtonElement>('[aria-label="Delete saved theme Delete fixture"]')!.click())
  expect(fixture.put).toHaveBeenCalledTimes(2)
  expect(useAppearanceStore.getState().current).toEqual(theme)
 }finally{useProductNavigationToolbarStore.setState({sidebarSettingsTarget:null});slot.remove()}
})
