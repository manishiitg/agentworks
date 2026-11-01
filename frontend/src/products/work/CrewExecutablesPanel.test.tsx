// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { CrewExecutablesPanel } from './CrewExecutablesPanel'
import { useWorkflowStore } from '../../stores/useWorkflowStore'
import { useChatStore } from '../../stores/useChatStore'
import type { ExecutionInspectionTarget } from '../../services/api-types'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const receipt = (run: string, execution: string): ExecutionInspectionTarget => ({ workspace_path: 'Workflow/child', run_folder: run, session_id: 'run-session', execution_id: execution, name: 'Research', started_at: '2026-11-01T10:00:00Z' })
vi.mock('../../services/api', async importOriginal => {
  const actual = await importOriginal<typeof import('../../services/api')>()
  return { ...actual,
  default: { get: async () => ({ data: { executables: [{ id: 'child', kind: 'workflow', workspace_path: 'Workflow/child', manifest: { label: 'Reports' } }], agents: [] } }) },
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
    const button = [...host.querySelectorAll('button')].find(item => item.textContent?.trim() === text)
    expect(button).toBeTruthy()
    await act(async () => button!.click())
    await flush()
  }
  try {
    await act(async () => root.render(<CrewExecutablesPanel projectId="crew" chatTabId="crew-chat" />))
    await flush()
    await click('ReportsWorkflow')
    expect(useChatStore.getState().activeTabId).toBe('crew-chat')
    expect(useWorkflowStore.getState().selectedRunFolder).toBe('unrelated-run')
    expect(useWorkflowStore.getState().runFolders.map(folder => folder.name)).toEqual(['unrelated-run'])
    await click('Runs')
    const select = host.querySelector<HTMLSelectElement>('[aria-label="Workflow run"]')!
    await act(async () => { select.value = 'run-2'; select.dispatchEvent(new Event('change', { bubbles: true })) })
    await flush()
    expect(host.querySelector('[data-view="logs"]')?.getAttribute('data-workspace')).toBe('Workflow/child')
    expect(host.querySelector('[data-view="logs"]')?.getAttribute('data-run')).toBe('run-2')
    await click('Costs')
    expect(host.querySelector('[data-view="costs"]')?.getAttribute('data-run')).toBe('run-2')
    expect(host.querySelector('[data-view="costs"]')?.getAttribute('data-run-only')).toBe('true')
    await click('Inspect')
    const step = [...host.querySelectorAll('button')].find(button => button.textContent?.includes('Research'))!
    await act(async () => step.click())
    await flush()
    expect(host.querySelector('[data-view="inspect"]')?.getAttribute('data-execution')).toBe('second-execution')
    expect(useChatStore.getState().activeTabId).toBe('crew-chat')
    expect(useWorkflowStore.getState().selectedRunFolder).toBe('unrelated-run')
  } finally { await act(async () => root.unmount()) }
})
