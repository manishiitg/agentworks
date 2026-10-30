// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
vi.hoisted(() => vi.stubGlobal('localStorage', { getItem: () => null, setItem: () => {}, removeItem: () => {} }))
const mocks = vi.hoisted(() => ({ target: vi.fn(), open: vi.fn(), user: { id: 'owner', allowed_products: null as string[] | null }, server: 'local', documents: [] as { id: string; path: string; title: string; order: number; artifact?: boolean }[], status: { apps: [], automations: [], results: [], errors: [] } as any }))
vi.mock('../../hooks/useWorkspaceViewTarget', () => ({ setWorkspaceViewTarget: mocks.target }))
vi.mock('../../services/api', () => ({ default: { post: async () => ({ data: { can_share: true } }) } }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: Object.assign((select: any) => select({ user: mocks.user }), { getState: () => ({ user: mocks.user }) }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ getWorkspaceScopedStorageKey: (key: string) => key, useWorkspaceConnectionStore: Object.assign((select: any) => select({ activeWorkspaceId: mocks.server }), { getState: () => ({ activeWorkspaceId: mocks.server }) }) }))
vi.mock('../../components/integrations/AgentInboxPanel', () => ({ useAgentInboxEnabled: () => true }))
vi.mock('./useWorkspaceStatus', () => ({ useWorkspaceStatus: () => ({ snapshot: mocks.status, refresh: vi.fn() }) }))
vi.mock('../../components/workspace/ChatChangesRow', () => ({ ChatChangesRow: ({ onOpen }: { onOpen: () => void }) => <button onClick={onOpen}>Changes in this chat</button> }))
vi.mock('../../components/workspace/WorkspaceBrowserRow', () => ({ WorkspaceBrowserRow: ({ onClick }: { onClick: () => void }) => <button onClick={onClick}>Browser · Connected</button> }))
vi.mock('../../components/workflow/reportDocuments', () => ({ useReportDocuments: () => ({ documents: mocks.documents }), selectReportDocument: vi.fn() }))
import { selectReportDocument } from '../../components/workflow/reportDocuments'
import { WorkContextPanel } from './WorkContextPanel'
import { workWorkspacePanels } from './workWorkspacePanels'
import { CREW_STARTER_TYPES, crewStarterShortcuts } from './crewStarterTypes'
import { crewProjectMenuItems } from './crewProjectMenu'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
beforeEach(() => {
 mocks.user = { id: 'owner', allowed_products: null }; mocks.server = 'local'; mocks.documents = []
 mocks.status = { apps: [], automations: [], results: [], errors: [] }
 window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['work', 'knowledgebase', 'mcp-gateway', 'bullpen'], gatewayUrl: 'http://example.test' }
})
afterEach(() => { vi.clearAllMocks(); delete window.__APP_RUNTIME_CONFIG__ })
const inventory = (features?: Set<string>) => { const value = workWorkspacePanels({ enabledPanels: features, showShell: true }); return [...value.views, ...value.ops, ...value.setup] }
async function render(mode: 'dev' | 'cowork', check: (host: HTMLDivElement) => Promise<void>) {
 const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
 try { await act(async () => root.render(<WorkContextPanel mode={mode} reviewSessionId="chat" workspacePath="Crew/example" projectId="example" panels={inventory()} onOpen={mocks.open}/>)); await check(host) }
 finally { await act(async () => root.unmount()); host.remove() }
}
it('shows actual workspace status without promoting occasional tools, and keeps All tools searchable', async () => {
 mocks.status.apps = [{ id: 'mcp:a', label: 'Notes account', detail: 'MCP · Connected', destination: 'mcp:connected' }]
 mocks.status.automations = [{ id: 'schedule:s', label: 'Morning summary', detail: 'Schedule · Paused', destination: 'schedules:schedules' }]
 await render('dev', async host => {
  expect(host.querySelector('h2')?.textContent).toBe('Changes')
  expect(host.textContent).toContain('Notes accountMCP · Connected')
  expect(host.textContent).toContain('Morning summarySchedule · Paused')
  for (const label of ['Recent', 'Costs & usage', 'Models', 'Chat history']) expect(host.textContent).not.toContain(label)
  const event = vi.fn(); window.addEventListener('open-quick-switcher', event)
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="All tools"]')!.click())
  expect(event.mock.calls[0][0].detail.query).toBe('@panels '); window.removeEventListener('open-quick-switcher', event)
  const app = [...host.querySelectorAll('button')].find(node => node.textContent?.startsWith('Notes account'))!
  mocks.server = 'another-server'; await act(async () => app.click())
  expect(mocks.open).not.toHaveBeenCalled()
 })
})
it('leads Cowork with finished Results, keeps artifacts unique and opens the admitted dashboard', async () => {
 mocks.documents = [{ id:'brief', title:'Project brief', path:'db/reports/brief.html', order:0, artifact:true }]
 mocks.status.results = [{ path:'db/reports/brief.html', title:'Duplicate', status:'ready' }, { path:'outputs/report.pdf', title:'Report', status:'missing' }]
 await render('cowork', async host => {
  expect(host.querySelector('h2')?.textContent).toBe('Results')
  expect(host.textContent).toContain('ReportMissing file'); expect(host.textContent).not.toContain('Duplicate')
  const artifact = [...host.querySelectorAll('button')].find(node => node.textContent?.startsWith('Project brief'))!
  await act(async () => artifact.click())
  expect(selectReportDocument).toHaveBeenCalledWith('Crew/example','db/reports/brief.html')
  expect(mocks.open).toHaveBeenLastCalledWith('dashboard')
 })
})
it('keeps every starter destination in the admitted All tools inventory and Run mode limited to dashboard', () => {
 const ids = crewProjectMenuItems({ panels: inventory(), projectId:'example', allowedProducts:null }).map(item => item.id)
 for (const starter of CREW_STARTER_TYPES) for (const shortcut of crewStarterShortcuts(starter.id)) expect(ids).toContain(shortcut)
 expect(workWorkspacePanels({ readOnly:true }).views.map(item => item.id)).toEqual(['dashboard'])
 expect(inventory(new Set(['skills'])).map(item => item.id)).not.toContain('files')
})
