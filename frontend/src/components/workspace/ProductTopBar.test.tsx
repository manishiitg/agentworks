// @vitest-environment happy-dom
import React, { act, useEffect } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterAll, afterEach, beforeEach, expect, it, vi } from 'vitest'
vi.hoisted(() => vi.stubGlobal('localStorage', { getItem: () => null, setItem: () => {}, removeItem: () => {} }))
import { AppShell } from './AppShell'
import { ProductTopBar, ProductTopBarActions } from './ProductTopBar'
import { WorkspaceTopToolbar } from './WorkspaceTopToolbar'
import { WorkspacePageHeader } from './WorkspacePageHeader'
import { ProductSettingsMenu } from './ProductSettingsMenu'
import { useAppStore } from '../../stores/useAppStore'
import { useLLMStore } from '../../stores/useLLMStore'
import { useProductNavigationToolbarStore } from '../../stores/useProductNavigationToolbarStore'
import { useWorkspaceReturnStore } from '../../stores/useWorkspaceReturnStore'

vi.mock('./AppQuickSwitcher', () => ({ AppQuickSwitcher: () => null }))
vi.mock('./WorkspaceChatsSidebar', () => ({ default: ({ toggle, footer }: { toggle?: React.ReactNode; footer?: React.ReactNode }) => <div>{toggle}My chats{footer}</div> }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const saved = new Map<string, string>()
const roots: Root[] = []
afterAll(() => vi.unstubAllGlobals())
beforeEach(() => {
  saved.clear()
  useAppStore.setState({ showWorkflowsOverview: false, showSchedulesOverview: false, adminPage: null })
  useLLMStore.setState({ showLLMModal: false })
  useProductNavigationToolbarStore.setState({ target: null, targetKind: null, leadingTarget: null, workspaceTrigger: null, pickerOpen: false, compactSettingsTarget: null, sidebarWidth: 0 })
  useWorkspaceReturnStore.setState({ restore: null })
  window.history.replaceState({}, '', '/crew')
  Object.defineProperty(window, 'localStorage', { configurable: true, value: {
    getItem: (key: string) => saved.get(key) ?? null,
    setItem: (key: string, value: string) => saved.set(key, value),
  } })
})
afterEach(async () => {
  await act(async () => { roots.splice(0).forEach(root => root.unmount()) })
  document.body.innerHTML = ''
  vi.useRealTimers()
})
async function render(children: React.ReactNode = <ProductTopBarActions><button>Account</button></ProductTopBarActions>, toolbar = true) {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host); roots.push(root)
  await act(async () => root.render(<AppShell><ProductTopBar>{children}</ProductTopBar>{toolbar && <WorkspaceTopToolbar><span>Existing chat tabs</span></WorkspaceTopToolbar>}</AppShell>))
  return host
}
it('moves controls into the existing workspace toolbar and leaves only chats on the left', async () => {
  const host = await render()
  const toolbar = host.querySelector('[data-terminal-focus-chrome="toolbar"]')!
  expect(toolbar.textContent).toContain('Existing chat tabs')
  expect(toolbar.querySelector('[aria-label="Product navigation"]')!.textContent).toContain('Account')
  expect(host.querySelector('[aria-label="Chat navigation"]')!.textContent).toContain('My chats')
  expect(host.querySelectorAll('header')).toHaveLength(0)
  expect(host.querySelectorAll('[aria-label="Product navigation"]')).toHaveLength(1)
})
it('closes only chats, keeps top controls accessible, and restores the preference after remount', async () => {
  const host = await render()
  const toggle = host.querySelector<HTMLButtonElement>('[data-navigation-toggle]')!
  await act(async () => toggle.click())
  expect(saved.get('product_navigation_mode')).toBe('auto-hide')
  expect(toggle.getAttribute('aria-label')).toBe('Open sidebar')
  expect(host.querySelector('[aria-label="Chat navigation"]')?.className).toContain('hidden')
  expect([...host.querySelectorAll('[aria-label="Navigation rail"] button')].map(button => button.getAttribute('aria-label'))).toEqual(['Home', 'Open sidebar', 'Search all chats (Ctrl+K or Command+K)'])
  expect(host.querySelector('[aria-label="Navigation rail"] [aria-label="Home"]')).not.toBeNull()
  const launcher = host.querySelector('[data-product-navigation-leading-slot] [data-navigation-toggle]')!
  expect(launcher.textContent).toBe('')
  expect(launcher.getAttribute('title')).toBe('Open sidebar')
  expect(launcher.getAttribute('aria-label')).toBe('Open sidebar')
  expect(launcher.parentElement?.className).toContain('md:hidden')
  expect(host.querySelector('[data-product-navigation-slot]')!.textContent).toContain('Account')
  expect(host.querySelector('[data-product-navigation-slot] [data-navigation-toggle]')).toBeNull()
  const toolbar = host.querySelector('[data-terminal-focus-chrome="toolbar"]')!
  expect(toolbar.firstElementChild?.hasAttribute('data-product-navigation-leading-slot')).toBe(true)
  expect(toolbar.querySelector('[data-runtime-brand]')).toBeNull()
  const showHome = vi.fn()
  window.addEventListener('popstate', showHome)
  const first = roots.shift()!; await act(async () => first.unmount())
  const restored = await render()
  expect(restored.querySelector('[data-navigation-toggle]')?.getAttribute('aria-expanded')).toBe('false')
  await act(async () => restored.querySelector<HTMLButtonElement>('[aria-label="Home"]')!.click())
  expect(showHome).toHaveBeenCalledTimes(1)
  expect(window.location.pathname).toBe('/home')
  expect(saved.get('product_navigation_mode')).toBe('auto-hide')
  await act(async () => restored.querySelector<HTMLButtonElement>('[data-navigation-toggle]')!.click())
  expect(showHome).toHaveBeenCalledTimes(1)
  window.removeEventListener('popstate', showHome)
  expect(saved.get('product_navigation_mode')).toBe('fixed')
  expect(restored.querySelector('[aria-label="Chat navigation"]')?.className).not.toContain('hidden')
  expect(restored.querySelector('[aria-label="Navigation rail"]')).toBeNull()
})
it('does not remount live product controls when the chat sidebar opens or closes', async () => {
  const mounted = vi.fn(), stopped = vi.fn()
  function LiveMonitor() { useEffect(() => { mounted(); return stopped }, []); return <button>Active work</button> }
  const host = await render(<LiveMonitor />, false)
  const toggle = host.querySelector<HTMLButtonElement>('[data-navigation-toggle]')!
  await act(async () => toggle.click())
  await act(async () => toggle.click())
  expect(mounted).toHaveBeenCalledTimes(1)
  expect(stopped).not.toHaveBeenCalled()
})
it('stays open after inactivity and focus changes until explicitly closed', async () => {
  vi.useFakeTimers()
  const host = await render()
  await act(async () => vi.advanceTimersByTime(12 * 60 * 60 * 1000))
  await act(async () => { window.dispatchEvent(new Event('focus')); document.dispatchEvent(new Event('visibilitychange')) })
  expect(host.querySelector('[data-navigation-toggle]')?.getAttribute('aria-expanded')).toBe('true')
})
it('keeps navigation available on pages that hide the workspace toolbar and clears detached slots', async () => {
  const host = await render()
  const target = useProductNavigationToolbarStore.getState().target
  expect(target).not.toBeNull()
  await act(async () => useAppStore.setState({ adminPage: 'users' }))
  expect(useProductNavigationToolbarStore.getState().target).toBeNull()
  expect(useProductNavigationToolbarStore.getState().leadingTarget).toBeNull()
  expect(host.querySelector('[aria-label="Product navigation"]')!.textContent).toContain('Account')
  await act(async () => useAppStore.setState({ adminPage: null }))
  expect(host.querySelector('[data-product-navigation-slot] [aria-label="Product navigation"]')).not.toBeNull()
  await act(async () => roots.shift()!.unmount())
  expect(useProductNavigationToolbarStore.getState().target).toBeNull()
  expect(useProductNavigationToolbarStore.getState().leadingTarget).toBeNull()
})
it('uses the open default for an unknown saved navigation preference', async () => {
  saved.set('product_navigation_mode', 'retired')
  const host = await render()
  expect(host.querySelector('[data-navigation-toggle]')?.getAttribute('aria-expanded')).toBe('true')
})

it('resizes the work list within bounds and remembers the width after remount', async () => {
  const host = await render()
  const separator = host.querySelector<HTMLElement>('[aria-label="Resize your work list"]')!
  expect(separator.getAttribute('aria-valuenow')).toBe('260')
  await act(async () => separator.dispatchEvent(new KeyboardEvent('keydown', { key: 'End', bubbles: true })))
  expect(separator.getAttribute('aria-valuenow')).toBe('360')
  await act(async () => separator.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true })))
  expect(separator.getAttribute('aria-valuenow')).toBe('360')
  await act(async () => roots.shift()!.unmount())
  const restored = await render()
  expect(restored.querySelector('[role="separator"]')?.getAttribute('aria-valuenow')).toBe('360')
})

it('registers Setup only in the visible sidebar and releases its slot on hide and unmount', async () => {
  const host = await render()
  const slot = host.querySelector('[data-sidebar-setup-slot]')!
  expect(useProductNavigationToolbarStore.getState().sidebarSettingsTarget).toBe(slot)
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Close sidebar"]')!.click())
  expect(useProductNavigationToolbarStore.getState().sidebarSettingsTarget).toBeNull()
  expect(useProductNavigationToolbarStore.getState().compactSettingsTarget).toBe(host.querySelector('[data-compact-setup-slot]'))
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Open sidebar"]')!.click())
  expect(useProductNavigationToolbarStore.getState().sidebarSettingsTarget).toBe(slot)
  expect(useProductNavigationToolbarStore.getState().compactSettingsTarget).toBeNull()
  await act(async () => roots.shift()!.unmount())
  expect(useProductNavigationToolbarStore.getState().sidebarSettingsTarget).toBeNull()
  expect(useProductNavigationToolbarStore.getState().compactSettingsTarget).toBeNull()
})

it('mounts the collapsed account trigger in the rail above its shortcuts instead of the lower workspace toolbar', async () => {
  const host = await render(<ProductSettingsMenu accountTrigger={<span>Account</span>}><button role="menuitem" data-product-navigation-action>Connect AI tools</button></ProductSettingsMenu>)
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Close sidebar"]')!.click())
  const trigger = host.querySelector<HTMLButtonElement>('[aria-label="Account & settings"]')!
  expect(trigger.closest('[data-compact-setup-slot]')).not.toBeNull()
  expect(trigger.closest('[data-terminal-focus-chrome="toolbar"]')).toBeNull()
  await act(async () => trigger.click())
  const menu = host.querySelector<HTMLElement>('[role="menu"]')!
  expect(menu.hidden).toBe(false)
  expect(trigger.getAttribute('aria-expanded')).toBe('true')
  expect(host.querySelector('[aria-label="Home"]')).not.toBeNull()
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Open sidebar"]')!.click())
  expect(host.querySelector('[data-sidebar-setup-slot] [aria-label="Account & settings"]')).not.toBeNull()
  expect(useProductNavigationToolbarStore.getState().compactSettingsTarget).toBeNull()
})

it('places global-page navigation beside the back action in the existing header', async () => {
  useAppStore.setState({ adminPage: 'mcp' })
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host); roots.push(root)
  await act(async () => root.render(<AppShell>
    <ProductTopBar><ProductTopBarActions><button>Account</button></ProductTopBarActions></ProductTopBar>
    <WorkspaceTopToolbar><span>Hidden workspace</span></WorkspaceTopToolbar>
    <header><WorkspaceTopToolbar navigationKind="page"><button>Back to Crew</button><h1>Connect AI tools</h1></WorkspaceTopToolbar></header>
  </AppShell>))
  const page = host.querySelector('header')!
  expect(useProductNavigationToolbarStore.getState().targetKind).toBe('page')
  expect(page.querySelector('[data-product-navigation-slot]')!.textContent).toContain('Account')
  const toggle = host.querySelector<HTMLButtonElement>('[data-navigation-toggle]')!
  await act(async () => toggle.click())
  expect(page.querySelector('[data-product-navigation-leading-slot] [data-navigation-toggle]')).not.toBeNull()
  expect(page.querySelector('[data-product-navigation-leading-slot] [aria-label="Home"]')).toBeNull()
  expect(host.querySelector('[aria-label="Navigation rail"] [aria-label="Home"]')).not.toBeNull()
  expect(page.querySelector('h1')!.textContent).toBe('Connect AI tools')
  expect(host.querySelectorAll('header')).toHaveLength(1)
  expect(host.querySelector('[data-terminal-focus-chrome="toolbar"]')!.querySelector('[data-product-navigation-slot]')).toBeNull()
})

it('keeps retained hidden pages from taking navigation away from the active header', async () => {
  function RetainedPages() {
    const activity = useAppStore(state => state.showWorkflowsOverview)
    const providers = useLLMStore(state => state.showLLMModal)
    return <>
      <WorkspaceTopToolbar>Workspace</WorkspaceTopToolbar>
      <div hidden={!activity || providers}><WorkspacePageHeader page="activity" title="Activity" /></div>
      <div hidden={!providers}><WorkspacePageHeader page="providers" title="AI accounts" /></div>
    </>
  }
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host); roots.push(root)
  await act(async () => root.render(<AppShell><ProductTopBar><button>Account</button></ProductTopBar><RetainedPages /></AppShell>))
  const headers = host.querySelectorAll('header')
  const ownsNavigation = (header: Element) => Boolean(header.querySelector('[aria-label="Product navigation"]'))
  expect(ownsNavigation(headers[0])).toBe(false)
  expect(ownsNavigation(headers[1])).toBe(false)
  expect(host.querySelector('[data-terminal-focus-chrome="toolbar"] [aria-label="Product navigation"]')).not.toBeNull()
  await act(async () => useAppStore.setState({ showWorkflowsOverview: true }))
  expect(ownsNavigation(headers[0])).toBe(true)
  expect(ownsNavigation(headers[1])).toBe(false)
  await act(async () => useLLMStore.setState({ showLLMModal: true }))
  expect(ownsNavigation(headers[0])).toBe(false)
  expect(ownsNavigation(headers[1])).toBe(true)
  expect(headers[1].querySelector('h1')!.textContent).toBe('AI accounts')
  expect(headers[1].textContent).not.toContain('Settings')
  expect(headers[1].querySelector('[aria-label="Back to workspace"]')!.textContent).toBe('')
  await act(async () => useLLMStore.setState({ showLLMModal: false }))
  expect(ownsNavigation(headers[0])).toBe(true)
  await act(async () => useAppStore.setState({ showWorkflowsOverview: false }))
  expect(ownsNavigation(headers[0])).toBe(false)
  expect(ownsNavigation(headers[1])).toBe(false)
  expect(host.querySelector('[data-terminal-focus-chrome="toolbar"] [aria-label="Product navigation"]')).not.toBeNull()
  expect(host.querySelectorAll('[aria-label="Product navigation"]')).toHaveLength(1)
})

it('keeps deployment branding out of the toolbar when the sidebar is closed', async () => {
  window.__APP_RUNTIME_CONFIG__ = { appName: 'Example', markUrl: '/brand/example.svg' }
  try {
    const host = await render()
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Close sidebar"]')!.click())
    expect(host.querySelector('[data-product-navigation-leading-slot] [data-runtime-brand]')).toBeNull()
    expect(host.querySelector('[aria-label="Product navigation"] [data-runtime-brand]')).toBeNull()
  } finally { delete window.__APP_RUNTIME_CONFIG__ }
})


it('keeps the left menu and its scroll position when a product header/content is replaced', async () => {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host); roots.push(root)
  const page = (name: string) => <AppShell><section key={name}><ProductTopBar><button>{name} actions</button></ProductTopBar><WorkspaceTopToolbar>{name}</WorkspaceTopToolbar></section></AppShell>
  await act(async () => root.render(page('Crew')))
  const sidebar = host.querySelector('[aria-label="Chat navigation"]')!
  sidebar.scrollTop = 125
  await act(async () => root.render(page('Dashboards')))
  expect(host.querySelector('[aria-label="Chat navigation"]')).toBe(sidebar)
  expect(sidebar.scrollTop).toBe(125)
  expect(host.querySelectorAll('[aria-label="Chat navigation"]')).toHaveLength(1)
  expect(host.querySelector('[data-app-main]')!.textContent).toContain('Dashboards')
})
