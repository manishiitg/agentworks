// @vitest-environment happy-dom
import { act, useEffect } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { ProductSurfaceSwitcher } from './ProductSurfaceSwitcher'
import { ProductTopBar } from './workspace/ProductTopBar'
import { WorkflowModeHandler } from './workflow/WorkflowModeHandler'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import { useModeStore } from '../stores/useModeStore'
import { useAppStore } from '../stores/useAppStore'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { selectWorkflowPreset } from '../utils/workflowNavigation'

vi.hoisted(() => {
  const values = new Map<string, string>()
  const storage = { getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => { values.set(key, value) }, removeItem: (key: string) => { values.delete(key) } }
  Object.defineProperty(globalThis, 'localStorage', { value: storage, configurable: true })
  Object.defineProperty(window, 'localStorage', { value: storage, configurable: true })
})
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// Real sidebar, stores and restoration handler; reproduce WorkSurface's chat
// mode selection and mount effect. Only manifest/file network reads are stubbed.
function CodeChatMount() {
  const mode = useModeStore(state => state.selectedModeCategory)
  useEffect(() => {
    useModeStore.getState().setModeCategory('multi-agent')
    useAppStore.getState().setAgentMode('multi-agent')
  }, [])
  return mode === 'workflow' ? <WorkflowModeHandler onPresetSelected={selectWorkflowPreset} children={null} /> : <p>Code chat</p>
}
function Navigation({ delayedGoalRestore }: { delayedGoalRestore: boolean }) {
  const product = useProductSurfaceStore(state => state.productSurface)
  return <><ProductTopBar sidebar><ProductSurfaceSwitcher /></ProductTopBar>
    <h1>{product}</h1>{product === 'code' && <CodeChatMount />}{product === 'agentworks' && delayedGoalRestore && <WorkflowModeHandler onPresetSelected={selectWorkflowPreset} children={null} />}</>
}

it.each([false, true])('keeps Code selected from the Goals sidebar (delayed old restore: %s)', async delayedGoalRestore => {
  window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['agentworks', 'code'] }
  useProductSurfaceStore.setState({ productSurface: 'agentworks' })
  useModeStore.setState({ selectedModeCategory: 'workflow' })
  useAppStore.setState({ agentMode: 'workflow' })
  const preset = { id: 'sidebar-goal', label: 'Saved goal', query: 'Keep goal', createdAt: Date.now(), agentMode: 'workflow' as const }
  useGlobalPresetStore.setState({ workflowPresets: [preset], activePresetIds: { workflow: preset.id, 'multi-agent': null } })
  let finishRefresh!: () => void
  const manifestReady = new Promise<void>(resolve => { finishRefresh = resolve })
  vi.spyOn(useGlobalPresetStore.getState(), 'refreshPresets').mockImplementation(async () => { if (delayedGoalRestore) await manifestReady })
  vi.spyOn(useWorkspaceStore.getState(), 'fetchFiles').mockResolvedValue(undefined)
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  try {
    await act(async () => root.render(<Navigation delayedGoalRestore={delayedGoalRestore} />))
    for (let attempt = 0; attempt < 2; attempt++) {
      await act(async () => host.querySelector<HTMLButtonElement>('button[aria-haspopup="menu"]')!.click())
      await act(async () => host.querySelector<HTMLButtonElement>('[role="menuitem"][aria-label="Code"]')!.click())
      await act(async () => finishRefresh())
      expect(host.querySelector('h1')?.textContent).toBe('code')
      expect(useModeStore.getState().selectedModeCategory).toBe('multi-agent')
      expect(useGlobalPresetStore.getState().activePresetIds.workflow).toBe(preset.id)
    }
  } finally {
    await act(async () => root.unmount()); host.remove(); delete window.__APP_RUNTIME_CONFIG__; vi.restoreAllMocks()
  }
})
