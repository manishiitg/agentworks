// @vitest-environment happy-dom
import React, { act, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { afterAll, expect, it, vi } from 'vitest'
vi.hoisted(() => vi.stubGlobal('localStorage', { getItem: () => null, setItem: () => {}, removeItem: () => {} }))
vi.mock('./WorkspaceChatsSidebar', () => ({ default: () => null }))
const auth = vi.hoisted(() => ({ user: { id: 'alice', allowed_products: ['work'] }, isMultiUserMode: false }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: any) => select(auth) }))
import { ProductTopBar } from './ProductTopBar'
import { WorkspaceTopToolbar } from './WorkspaceTopToolbar'
import { ChatTabPill } from '../chat/ChatTabPill'
import { TopBarEntitySelector } from '../topbar/TopBarEntitySelector'
import { TooltipProvider } from '../ui/tooltip'
import { useProductNavigationToolbarStore } from '../../stores/useProductNavigationToolbarStore'
import { useAppStore } from '../../stores/useAppStore'
import { useLLMStore } from '../../stores/useLLMStore'
import type { ProductSurface } from '../../products/productSurfaceConfig'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterAll(() => vi.unstubAllGlobals())

it.each((['agentworks', 'work', 'relays'] as ProductSurface[]).flatMap(surface => [false, true].map(active => ({ surface, active }))))('uses the shared title picker in $surface (active=$active)', async ({ surface, active }) => {
  auth.user.allowed_products = [surface]
  useProductNavigationToolbarStore.setState({ target: null, workspaceTrigger: null, pickerOpen: false })
  useAppStore.setState({ adminPage: null, showWorkflowsOverview: false, showSchedulesOverview: false })
  useLLMStore.setState({ showLLMModal: false })
  window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['agentworks', 'work', 'code', 'relays'] }
  const select = vi.fn()
  function Picker() {
    const [open, setOpen] = useState(false)
    return <TopBarEntitySelector label="Company CA" placeholder="Choose a Crew member" open={open} onToggle={() => setOpen(value => !value)} onClose={() => setOpen(false)}><div>Existing workspaces</div></TopBarEntitySelector>
  }
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  try {
    await act(async () => root.render(<TooltipProvider><ProductTopBar><Picker /></ProductTopBar><WorkspaceTopToolbar><ChatTabPill tab={{ tabId: 'main', name: 'Company CA', isStreaming: false, hasRunningBgAgents: false }} isActive={active} isBlank={false} canClose={false} productSurface={surface} onTabClick={select} onCloseTab={() => {}} /></WorkspaceTopToolbar></TooltipProvider>))
    const title = host.querySelector('[data-product-icon]')!.closest('[role="button"]') as HTMLElement
    await act(async () => title.click())
    if (active) {
      expect(select).not.toHaveBeenCalled()
      expect(useProductNavigationToolbarStore.getState().pickerOpen).toBe(true)
    } else {
      expect(select).toHaveBeenCalledWith('main')
      await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Switch workspace"]')!.click())
    }
    expect(host.textContent).toContain('Existing workspaces')
    // The project picker lists projects only; switching products lives elsewhere (owner, 2026-10-29).
    expect(host.querySelectorAll('[aria-label="Products"] button')).toHaveLength(0)
    const trigger = host.querySelector<HTMLButtonElement>('[aria-label="Switch workspace"]')!
    trigger.focus()
    await act(async () => trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })))
    expect(useProductNavigationToolbarStore.getState().pickerOpen).toBe(false)
  } finally { await act(async () => root.unmount()); host.remove(); delete window.__APP_RUNTIME_CONFIG__ }
})
