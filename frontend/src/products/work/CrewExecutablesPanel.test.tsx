// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { CrewExecutablesPanel } from './CrewExecutablesPanel'
import { CrewWorkflowPage } from './CrewWorkflowPage'
import api from '../../services/api'
import { buildWorkflowPresetsFromManifests } from '../../stores/useGlobalPresetStore'
import type { DiscoveredWorkflow } from '../../services/api-types'
import { useWorkflowStore } from '../../stores/useWorkflowStore'
import { useChatStore } from '../../stores/useChatStore'
import type { ExecutionInspectionTarget } from '../../services/api-types'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const receipt = (run: string, execution: string): ExecutionInspectionTarget => ({ workspace_path: 'Workflow/child', run_folder: run, session_id: 'run-session', execution_id: execution, name: 'Research', started_at: '2026-11-01T10:00:00Z' })
vi.mock('../../services/api', async importOriginal => {
  const actual = await importOriginal<typeof import('../../services/api')>()
  return { ...actual,
  default: { get: async (url: string) => url === '/api/crew-script-file' ? ({ data: { success: true, data: { content: "print('saved script')" } } }) : ({ data: { executables: [{ id: 'child', kind: 'workflow', workspace_path: 'Workflow/child', manifest: { label: 'Reports' } }, { id: 'script', kind: 'script', workspace_path: 'Crew/lab/code/script', manifest: { label: 'Qualify' } }], agents: [], existing_relays: [{id:'retained-relay',label:'Existing Relay'}] } }), post: vi.fn(async () => ({data:{}})), put: vi.fn(async () => ({data:{success:true}})) },
  agentApi: { ...actual.agentApi,
    loadWorkspaceState: async () => ({ success: true, data: { run_folders: [{ name: 'run-1' }, { name: 'run-2' }], phases: [], variables_manifest: null } }),
    getWorkspaceStepInspections: async () => ({ items: [ { inspection: receipt('run-1', 'first-execution'), status: 'completed' }, { inspection: receipt('run-2', 'second-execution'), status: 'completed' } ], next_cursor: null }),
  },
  }
})
vi.mock('../../stores/useWorkflowManifestStore', () => ({ useWorkflowManifestStore: { getState: () => ({ refreshWorkflows: async () => {} }) } }))
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: () => {} }))
// View boundaries are lazy-loaded; assert the identity delivered to each one,
// while using the actual Crew navigation and workspace-state hook.
vi.mock('../../components/workflow/canvas/WorkspaceViewHost', () => ({ default: ({ workspacePath }: { workspacePath: string }) => <div data-view="steps" data-workspace={workspacePath} /> }))
vi.mock('../../components/workflow/ExecutionLogsPopup', () => ({ default: ({ workspacePath, runFolder }: { workspacePath: string; runFolder: string }) => <div data-view="logs" data-workspace={workspacePath} data-run={runFolder} /> }))
vi.mock('../../components/workflow/CostsPopup', () => ({ default: ({ workspacePath, selectedRunFolder, runOnly }: { workspacePath: string; selectedRunFolder: string; runOnly: boolean }) => <div data-view="costs" data-workspace={workspacePath} data-run={selectedRunFolder} data-run-only={String(runOnly)} /> }))
vi.mock('../../components/chat/StepInspectionPanel', () => ({ default: ({ target }: { target: ExecutionInspectionTarget }) => <div data-view="inspect" data-execution={target.execution_id} /> }))

// PLAT-746-265: child editor navigation must not replace its Crew chat or leak
// run selection into another workflow. Identically named steps need exact receipts.
it('keeps the Crew chat and unrelated workflow state while inspecting an exact child run', async () => {
  useChatStore.setState({ activeTabId: 'crew-chat' })
  useWorkflowStore.setState({ selectedRunFolder: 'unrelated-run', runFolders: [{ name: 'unrelated-run' }] })
  const host = document.createElement('div')
  const root = createRoot(host)
  const flush = () => act(async () => { await new Promise(resolve => setTimeout(resolve, 0)) })
  const click = async (text: string) => {
    const button = [...host.querySelectorAll('button')].find(item => item.textContent?.trim().startsWith(text))
    expect(button).toBeTruthy()
    await act(async () => button!.click())
    await flush()
  }
  try {
    await act(async () => root.render(<CrewWorkflowPage projectId="crew" chatTabId="crew-chat" />))
    await flush()
    // The workflow form has one kind; retained Relays link to their existing
    // data is retained without a link or a stale standalone selection.
    expect(host.querySelector('a[href="/relays/retained-relay"]')).toBeNull()
    const manifests = [
      {crew_id:'crew',workspace_path:'Workflow/workflow',manifest:{id:'workflow',kind:'workflow',label:'Workflow'}},
      {crew_id:'crew',workspace_path:'Workflow/retained-relay',manifest:{id:'retained-relay',kind:'relay',label:'Existing Relay'}},
    ] as unknown as DiscoveredWorkflow[]
    expect(buildWorkflowPresetsFromManifests(manifests).map(preset => preset.id)).toEqual([])
    expect(host.querySelector('[data-view="steps"]')?.getAttribute('data-workspace')).toBe('Workflow/child')
    expect([...host.querySelectorAll('button')].some(button => button.textContent?.trim() === 'Settings')).toBe(false)
    expect(api.post).not.toHaveBeenCalled()
    expect(useChatStore.getState().activeTabId).toBe('crew-chat')
    expect(useWorkflowStore.getState().selectedRunFolder).toBe('unrelated-run')
    expect(useWorkflowStore.getState().runFolders.map(folder => folder.name)).toEqual(['unrelated-run'])
    await click('Runs')
    await click('run-2')
    await click('Full log')
    expect(host.querySelector('[data-view="logs"]')?.getAttribute('data-workspace')).toBe('Workflow/child')
    expect(host.querySelector('[data-view="logs"]')?.getAttribute('data-run')).toBe('run-2')
    await click('Cost details')
    expect(host.querySelector('[data-view="costs"]')?.getAttribute('data-run')).toBe('run-2')
    expect(host.querySelector('[data-view="costs"]')?.getAttribute('data-run-only')).toBe('true')
    // Only this run's receipts are listed; opening the step shows the exact execution.
    await click('Research')
    expect(host.querySelector('[data-view="inspect"]')?.getAttribute('data-execution')).toBe('second-execution')
    expect(useChatStore.getState().activeTabId).toBe('crew-chat')
    expect(useWorkflowStore.getState().selectedRunFolder).toBe('unrelated-run')
  } finally { await act(async () => root.unmount()) }
})

// PLAT-746-265: a standalone script opens code directly and saves to the
// owning-Crew source endpoint, without manufacturing a workflow plan.
it('opens and saves the shared script source from its Crew', async () => {
  const host = document.createElement('div'); const root = createRoot(host)
  const flush = () => act(async () => { await new Promise(resolve => setTimeout(resolve, 0)) })
  try {
    await act(async () => root.render(<CrewExecutablesPanel projectId="crew" chatTabId="crew-chat" kind="script" />)); await flush()
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent?.startsWith('Qualify'))!.click()); await flush()
    expect(host.querySelector('[data-view="steps"]')).toBeNull()
    expect(host.querySelector('textarea')?.value).toBe("print('saved script')")
    const save = [...host.querySelectorAll('button')].find(button => button.textContent?.trim() === 'Save')!
    await act(async () => save.click()); await flush()
    expect(api.put).toHaveBeenCalledWith('/api/crew-script-file', { content: "print('saved script')" }, { params: { workspace_path: 'Crew/lab/code/script', file: 'main.py' } })
  } finally { await act(async () => root.unmount()) }
})

// The Scripts tab lists the Python a workflow's own steps run (server-provided), not only saved Crew scripts.
it('lists workflow step scripts in the Scripts tab with where each is used', async () => {
  const spy = vi.spyOn(api, 'get').mockResolvedValueOnce({ data: { executables: [], step_scripts: [{ workflow_id: 'child', workflow: 'Reports', step_id: 's1', step: 'Normalize statement' }], agents: [] } })
  const host = document.createElement('div')
  const root = createRoot(host)
  try {
    await act(async () => root.render(<CrewExecutablesPanel projectId="crew" chatTabId="crew-chat" kind="script" />))
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)) })
    expect(host.textContent).toContain('Step: Normalize statement')
    expect(host.textContent).not.toContain('No scripts yet')
  } finally {
    spy.mockRestore()
    await act(async () => root.unmount())
  }
})
