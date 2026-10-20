// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({ target: vi.fn(), open: vi.fn() }))
vi.mock('../../hooks/useWorkspaceViewTarget', () => ({ setWorkspaceViewTarget: mocks.target }))
vi.mock('../../services/api', () => ({ agentApi: { listReportHumanInputs: async () => ({ success: true, inputs: [] }) } }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: any) => select({ user: { allowed_products: ['work'] } }) }))
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: () => {} }))
vi.mock('../../components/workflow/ShareDashboardButton', () => ({ ShareDashboardButton: () => <button>Share</button> }))
vi.mock('../../components/workspace/WorkspaceBrowserRow', () => ({ WorkspaceBrowserRow: ({ onClick }: { onClick: () => void }) => <button onClick={onClick}>Browser</button> }))
vi.mock('../../components/workflow/reportDocuments', () => ({ useReportDocuments: () => ({ documents: [], defaultPath: 'db/reports/index.html' }), selectReportDocument: vi.fn() }))
import { WorkContextPanel } from './WorkContextPanel'
import { workWorkspacePanels } from './workWorkspacePanels'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('shows one grouped menu, keeps Ops collapsed and routes moved destinations', async () => {
  const host = document.createElement('div'), root = createRoot(host)
  const inventory = workWorkspacePanels()
  try {
    await act(async () => root.render(<WorkContextPanel workspacePath="Crew/example" projectId="example" panels={[...inventory.views, ...inventory.ops, ...inventory.setup]} onOpen={mocks.open} />))
    expect([...host.querySelectorAll('h2,summary')].map(node => node.textContent)).toEqual(['Use & share', 'Connections', 'Ops'])
    expect(host.querySelector('details')?.open).toBe(false)
    const buttons = [...host.querySelectorAll('button')]
    for (const label of ['Dashboard', 'Share', 'Schedules', 'Watchers', 'Webhooks', 'Functions', 'Slack', 'Memory & skills', 'Chat history', 'Settings']) expect(buttons.filter(button => button.textContent === label)).toHaveLength(1)
    expect(host.textContent).not.toContain('More tools')
    expect(host.textContent).not.toContain('Edit shortcuts')
    await act(async () => buttons.find(button => button.textContent === 'Webhooks')!.click())
    expect(mocks.target).toHaveBeenLastCalledWith('workshop', 'triggers')
    expect(mocks.open).toHaveBeenLastCalledWith('schedules')
    await act(async () => buttons.find(button => button.textContent === 'Chat history')!.click())
    expect(mocks.open).toHaveBeenLastCalledWith('history')
  } finally { await act(async () => root.unmount()) }
})

it('honors server feature inventory after moving Skills and history', () => {
  const features = new Set(['skills', 'schedules'])
  const inventory = workWorkspacePanels({ enabledPanels: features })
  const ids = [...inventory.views, ...inventory.ops, ...inventory.setup].map(item => item.id)
  expect(ids).toContain('memory')
  expect(ids).toContain('history')
  expect(ids).not.toContain('dashboard')
  expect(ids).not.toContain('shell')
  expect(workWorkspacePanels({ readOnly: true }).views.map(item => item.id)).toEqual(['dashboard'])
})
