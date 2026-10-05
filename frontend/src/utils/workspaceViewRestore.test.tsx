// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { useWorkspaceViewPreference } from '../hooks/useWorkspaceViewPreference'
import { useWorkflowFilesViewSync } from '../components/workflow/hooks/useWorkflowFilesViewSync'
import { useWorkflowStore } from '../stores/useWorkflowStore'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { useAppStore } from '../stores/useAppStore'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import { PRODUCT_SURFACES, type ProductSurface } from '../products/productSurfaceConfig'
import { normalizeViewFrom, readWorkspaceViewPreference, writeWorkspaceViewPreference } from './workspaceViewPreference'

vi.hoisted(() => {
  const values = new Map<string, string>()
  const storage = { getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => { values.set(key, String(value)) },
    removeItem: (key: string) => { values.delete(key) }, clear: () => values.clear(),
    key: (index: number) => [...values.keys()][index] ?? null, get length() { return values.size } }
  Object.defineProperty(globalThis, 'localStorage', { value: storage, configurable: true })
  Object.defineProperty(window, 'localStorage', { value: storage, configurable: true })
})

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const normalize = normalizeViewFrom(['default', 'selected'] as const)

it('restores all product toolbar selections after remount and isolates projects and servers', async () => {
  localStorage.clear()
  const host = document.createElement('div')
  let root = createRoot(host)
  function Toolbar({ product }: { product: ProductSurface }) {
    const [view, select] = useWorkspaceViewPreference(product, 'same-project', 'default', normalize)
    return <button data-product={product} aria-pressed={view === 'selected'} onClick={() => select('selected')}>{view}</button>
  }
  const render = () => root.render(<>{PRODUCT_SURFACES.map(product => <Toolbar key={product} product={product} />)}</>)
  try {
    await act(async () => render())
    await act(async () => { for (const button of host.querySelectorAll<HTMLButtonElement>('button')) button.click() })
    expect(host.querySelectorAll('[aria-pressed="true"]')).toHaveLength(PRODUCT_SURFACES.length)
    act(() => root.unmount())
    root = createRoot(host)
    await act(async () => render())
    expect(host.querySelectorAll('[aria-pressed="true"]')).toHaveLength(PRODUCT_SURFACES.length)
    expect(readWorkspaceViewPreference('code', 'other-project', normalize)).toBeNull()
    localStorage.setItem('workspace-connection-store', JSON.stringify({state:{activeWorkspaceId:'other-server'}}))
    expect(readWorkspaceViewPreference('code', 'same-project', normalize)).toBeNull()
    localStorage.removeItem('workspace-connection-store')
    writeWorkspaceViewPreference('code', 'same-project', 'removed-view')
    expect(readWorkspaceViewPreference('code', 'same-project', normalize, () => 'selected')).toBeNull()
    expect(readWorkspaceViewPreference('work', 'same-project', normalize)).toBe('selected')
    expect(readWorkspaceViewPreference('code', 'legacy-project', normalize, () => 'selected')).toBe('selected')
    expect(readWorkspaceViewPreference('code', 'legacy-project', normalize)).toBe('selected')
  } finally { act(() => root.unmount()) }
})

it('keeps the saved Goals view through delayed startup and switches Goals/Relays without resetting execution state', async () => {
  localStorage.clear()
  useProductSurfaceStore.getState().setProductSurface('agentworks')
  useGlobalPresetStore.getState().setActivePreset('workflow', 'restored-workflow')
  useWorkflowStore.setState({ _currentPresetId: null, _currentViewProduct: null, _presetStates: {} })
  useWorkflowStore.getState().switchToPreset('restored-workflow')
  useWorkflowStore.getState().openWorkspaceView('browser')
  // Page-reload ordering: layout flags are available before the manifests.
  useWorkflowStore.setState({ _currentPresetId: null, _currentViewProduct: null, _presetStates: {}, workflowWorkspaceView: null })
  useAppStore.setState({ workspaceMinimized: false })
  function Layout() {
    const workflow = useWorkflowStore()
    const minimized = useAppStore(state => state.workspaceMinimized)
    useWorkflowFilesViewSync({
      enabled: true, activePresetId: 'restored-workflow', restoredPresetId: workflow._currentPresetId,
      workspaceMinimized: minimized, workflowWorkspaceView: workflow.workflowWorkspaceView,
      lastCanvasView: workflow.lastCanvasView, showWorkspacePane: workflow.showWorkspacePane,
      setWorkspaceMinimized: useAppStore.getState().setWorkspaceMinimized,
      setShowWorkspacePane: workflow.setShowWorkspacePane,
      setWorkflowWorkspaceView: workflow.setWorkflowWorkspaceView,
    })
    return <span>{workflow.workflowWorkspaceView ?? 'loading'}</span>
  }
  const host = document.createElement('div')
  const root = createRoot(host)
  try {
    await act(async () => root.render(<Layout />))
    expect(host.textContent).toBe('loading')
    await act(async () => useWorkflowStore.getState().switchToPreset('restored-workflow'))
    expect(host.textContent).toBe('browser')
    expect(useAppStore.getState().workspaceMinimized).toBe(true)
    await act(async () => useWorkflowStore.setState({ selectedRunFolder: 'iteration-7' }))
    await act(async () => {
      useProductSurfaceStore.getState().setProductSurface('relays')
      useWorkflowStore.getState().switchToPreset('restored-workflow')
      useWorkflowStore.getState().openWorkspaceView('identity')
      useProductSurfaceStore.getState().setProductSurface('agentworks')
      useWorkflowStore.getState().switchToPreset('restored-workflow')
    })
    expect(host.textContent).toBe('browser')
    expect(useWorkflowStore.getState().selectedRunFolder).toBe('iteration-7')
    // The actual Files toggle must still work after startup.
    await act(async () => useAppStore.getState().setWorkspaceMinimized(false))
    expect(host.textContent).toBe('files')
  } finally { act(() => root.unmount()) }
})
