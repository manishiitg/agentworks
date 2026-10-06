// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { ProductSurfaceSwitcher } from './ProductSurfaceSwitcher'
import { ProductTopBar } from './workspace/ProductTopBar'
import { useWorkflowTabRestore } from '../hooks/useWorkflowTabRestore'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import { useModeStore } from '../stores/useModeStore'
import { useAppStore } from '../stores/useAppStore'
import { useChatStore } from '../stores/useChatStore'
import { useWorkflowStore } from '../stores/useWorkflowStore'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'

vi.hoisted(() => {
  const values = new Map<string, string>()
  const storage = { getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => { values.set(key, value) }, removeItem: (key: string) => { values.delete(key) } }
  Object.defineProperty(globalThis, 'localStorage', { value: storage, configurable: true })
  Object.defineProperty(window, 'localStorage', { value: storage, configurable: true })
})
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// App's production restoration hook runs after the real sidebar changes product.
function Navigation() {
  const product = useProductSurfaceStore(state => state.productSurface)
  const mode = useModeStore(state => state.selectedModeCategory)
  useWorkflowTabRestore(true, product, mode)
  return <><ProductTopBar sidebar><ProductSurfaceSwitcher /></ProductTopBar><h1>{product}</h1></>
}

it.each([false, true])('keeps Relays selected with saved Goal tabs (saved Relay tab: %s)', async hasRelayTab => {
  window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['agentworks', 'relays'] }
  useProductSurfaceStore.setState({ productSurface: 'agentworks' })
  useModeStore.setState({ selectedModeCategory: 'workflow' })
  useAppStore.setState({ agentMode: 'workflow' })
  const goal = { id: 'saved-goal', label: 'Saved goal', query: 'Goal', createdAt: 1, agentMode: 'workflow' as const }
  const relay = { ...goal, id: 'saved-relay', label: 'Saved relay', workflowKind: 'relay' as const }
  useGlobalPresetStore.setState({ workflowPresets: [goal, relay], activePresetIds: { workflow: goal.id, 'multi-agent': null } })
  useChatStore.setState({ chatTabs: {}, activeTabId: null })
  vi.spyOn(useWorkspaceStore.getState(), 'fetchFiles').mockResolvedValue(undefined)
  const goalTabId = await useChatStore.getState().createChatTab('Goal chat', { mode: 'workflow', phaseId: 'workflow-builder', presetQueryId: goal.id }, 'goal-session')
  const relayTabId = hasRelayTab ? await useChatStore.getState().createChatTab('Relay chat', { mode: 'workflow', phaseId: 'workflow-builder', presetQueryId: relay.id }, 'relay-session') : null
  // The Goal is the active, remembered and most recently used workflow tab.
  useChatStore.setState(state => ({ activeTabId: goalTabId, chatTabs: { ...state.chatTabs, [goalTabId]: { ...state.chatTabs[goalTabId], lastAccessedAt: Date.now() + 100 } } }))
  useWorkflowStore.setState({ activeWorkflowTabId: goalTabId })
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  try {
    await act(async () => root.render(<Navigation />))
    const select = async (label: string) => {
      await act(async () => host.querySelector<HTMLButtonElement>('button[aria-haspopup="menu"]')!.click())
      await act(async () => host.querySelector<HTMLButtonElement>(`[role="menuitem"][aria-label="${label}"]`)!.click())
    }
    for (let attempt = 0; attempt < 2; attempt++) {
      await select('Relays')
      expect(host.querySelector('h1')?.textContent).toBe('relays')
      expect(useChatStore.getState().activeTabId).toBe(relayTabId)
      expect(useGlobalPresetStore.getState().activePresetIds.workflow).toBe(hasRelayTab ? relay.id : null)
      // Returning to Goals restores the saved Goal, without deleting either tab.
      await select('Goals')
      expect(host.querySelector('h1')?.textContent).toBe('agentworks')
      expect(useChatStore.getState().activeTabId).toBe(goalTabId)
      expect(useChatStore.getState().chatTabs[goalTabId]).toBeDefined()
    }
  } finally {
    await act(async () => root.unmount()); host.remove(); delete window.__APP_RUNTIME_CONFIG__; vi.restoreAllMocks()
  }
})
