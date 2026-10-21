// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
const state = vi.hoisted(() => ({ enabled: true, canWrite: true, open: vi.fn() }))
vi.mock('../../hooks/useCanWriteWorkflow', () => ({ useCanWriteWorkflow: () => state.canWrite }))
vi.mock('../../stores/useWorkflowStore', () => ({ useWorkflowStore: (select: any) => select({ openWorkspaceView: state.open }) }))
vi.mock('../../stores/useWorkflowManifestStore', () => ({ useWorkflowManifestStore: (select: any) => select({ workflows: [{ workspace_path: 'Workflow/example', manifest: { pulse: { enabled: state.enabled } } }] }) }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: any) => select({ isMultiUserMode: false, user: { allowed_products: ['agentworks', 'relays'] } }) }))
vi.mock('./hooks/usePendingDecisionCount', () => ({ usePendingDecisionState: () => ({ loaded: true, count: 2 }) }))
vi.mock('./ShareDashboardButton', () => ({ ShareDashboardButton: () => <button>Share</button> }))
vi.mock('../workspace/WorkspaceBrowserRow', () => ({ WorkspaceBrowserRow: ({ onClick }: { onClick: () => void }) => <button onClick={onClick}>Browser</button> }))
vi.mock('../workspace/useProjectStatus', () => ({ useProjectStatus: () => ({}) }))
vi.mock('../workspace/WorkspaceAgentBridgeRows', () => ({ WorkspaceAgentBridgeRows: () => null }))
vi.mock('./reportDocuments', () => ({ useReportDocuments: () => ({ documents: [], defaultPath: 'db/reports/index.html' }), selectReportDocument: vi.fn() }))
import { WorkflowContextPanel } from './WorkflowContextPanel'
import { RelayContextPanel } from './RelayContextPanel'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('places enabled Pulse above the groups; Off hides it and Relay has none', async () => {
  const host = document.createElement('div'), root = createRoot(host)
  state.canWrite = true; state.enabled = true
  try {
    await act(async () => root.render(<WorkflowContextPanel workspacePath="Workflow/example" />))
    expect([...host.querySelectorAll('h2,summary')].map(node => node.textContent)).toEqual(['Your work', 'Share', 'Connections', 'Setup'])
    expect(host.querySelector('button')?.textContent).toBe('Pulse')
    const ops = [...host.querySelectorAll('details')].find(item => item.querySelector('summary')?.textContent === 'Setup')!
    expect(ops.open).toBe(false)
    expect(ops.textContent).toContain('Publish')
    expect(ops.textContent).toContain('Notify')
    state.enabled = false
    await act(async () => root.render(<WorkflowContextPanel workspacePath="Workflow/example" />))
    expect([...host.querySelectorAll('button')].filter(button => button.textContent === 'Pulse')).toHaveLength(0)
    expect([...host.querySelectorAll('details')].find(item => item.querySelector('summary')?.textContent === 'Setup')?.textContent).toContain('Turn Pulse on')
    await act(async () => root.render(<RelayContextPanel workspacePath="Workflow/example" />))
    for (const label of ['Pulse', 'Publish', 'Notify', 'Schedules', 'Functions']) expect(host.textContent).not.toContain(label)
    const webhooks = [...host.querySelectorAll('button')].find(button => button.textContent === 'Webhooks')!
    await act(async () => webhooks.click())
    expect(state.open).toHaveBeenLastCalledWith('workshop', 'triggers')
    state.canWrite = false
    await act(async () => root.render(<RelayContextPanel workspacePath="Workflow/example" />))
    expect(host.textContent).toBe('')
  } finally { state.canWrite = true; await act(async () => root.unmount()) }
})
