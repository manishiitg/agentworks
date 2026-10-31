// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
vi.hoisted(() => vi.stubGlobal('localStorage', { getItem: () => null, setItem: () => {}, removeItem: () => {} }))
const mocks = vi.hoisted(() => ({ target: vi.fn(), open: vi.fn(), user: { id: 'owner', allowed_products: null as string[] | null }, server: 'local', documents: [] as { id: string; path: string; title: string; order: number; artifact?: boolean; motion?: boolean }[], status: { apps: [], automations: [], errors: [] } as any }))
vi.mock('../../hooks/useWorkspaceViewTarget', () => ({ setWorkspaceViewTarget: mocks.target }))
vi.mock('../../services/api', () => ({ default: { post: async () => ({ data: { can_share: true } }) }, getApiBaseUrl: () => '', getAuthToken: () => null, agentApi: {} }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: Object.assign((select: any) => select({ user: mocks.user }), { getState: () => ({ user: mocks.user }) }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ getWorkspaceScopedStorageKey: (key: string) => key, useWorkspaceConnectionStore: Object.assign((select: any) => select({ activeWorkspaceId: mocks.server }), { getState: () => ({ activeWorkspaceId: mocks.server }) }) }))
vi.mock('../../components/integrations/AgentInboxPanel', () => ({ useAgentInboxEnabled: () => true }))
vi.mock('./useWorkspaceStatus', () => ({ useWorkspaceStatus: () => ({ snapshot: mocks.status, refresh: vi.fn() }) }))
vi.mock('../../components/workspace/ChatChangesRow', () => ({ ChatChangesRow: ({ onOpen }: { onOpen: (reference?: { workspacePath: string; sessionId: string; snapshotId: string }) => void }) => <><button onClick={() => onOpen({ workspacePath: 'Crew/example', sessionId: 'chat', snapshotId: 'latest' })}>Latest reply</button><button onClick={() => onOpen()}>All chat changes</button></> }))
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
 mocks.status = { apps: [], automations: [], errors: [] }
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
 mocks.status.apps = [{ id: 'mcp:a', label: 'Notes account', detail: 'MCP · Connected', destination: 'mcp:connected' }, { id: 'mcp:b', label: 'Other account', detail: 'MCP · Needs attention', destination: 'mcp:connected', attention:true }, { id:'google:g', label:'My Google', detail:'Google · Connected', destination:'mcp:gmail' }]
 mocks.status.automations = [{ id: 'schedule:s', label: 'Morning summary', detail: 'Paused', destination: 'schedules:schedules' }]
 await render('dev', async host => {
  expect(host.querySelector('h2')?.textContent).toBe('Changes')
  await act(async () => [...host.querySelectorAll('button')].find(node => node.textContent === 'Latest reply')!.click())
  expect(mocks.target).toHaveBeenLastCalledWith('files', expect.stringContaining('reply:'))
  await act(async () => [...host.querySelectorAll('button')].find(node => node.textContent === 'All chat changes')!.click())
  expect(mocks.target).toHaveBeenLastCalledWith('files', 'review-all-chat')
  expect(host.textContent).toContain('MCPs2 · 1 needs attention')
  expect(host.textContent).not.toContain('Notes account'); expect(host.textContent).not.toContain('Other account')
  expect(host.textContent).toContain('My GoogleGoogle · Connected')
  expect(host.textContent).toContain('Morning summaryPaused')
  for (const label of ['Recent', 'Costs & usage', 'Models', 'Chat history']) expect(host.textContent).not.toContain(label)
  const event = vi.fn(); window.addEventListener('open-quick-switcher', event)
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="All tools"]')!.click())
  expect(event.mock.calls[0][0].detail.query).toBe('@panels '); window.removeEventListener('open-quick-switcher', event)
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Full menu"]')!.click())
  expect([...host.querySelectorAll('summary')].map(node => node.textContent)).toEqual(['Build','Use','Share','Integrations','Setup'])
  for (const label of ['Files','Models','Costs & usage','Terminal','My Slack account','Access & sharing']) expect([...host.querySelectorAll('button')].some(node => node.textContent === label),label).toBe(true)
  const files = [...host.querySelectorAll('button')].find(node => node.textContent === 'Files')!
  await act(async () => files.click()); expect(mocks.open).toHaveBeenLastCalledWith('files')
  await act(async () => [...host.querySelectorAll('button')].find(node => node.textContent === 'Back to workspace')!.click())
  expect(host.querySelector('h2')?.textContent).toBe('Changes')
  expect(host.textContent).not.toContain('Models')
  mocks.open.mockClear()
  const app = [...host.querySelectorAll('button')].find(node => node.textContent?.startsWith('MCPs'))!
  mocks.server = 'another-server'; await act(async () => app.click())
  expect(mocks.open).not.toHaveBeenCalled()
 })
})
it('groups dashboards, motion and Artifacts without a finished-file registry, preserving admission and account guards', async () => {
 mocks.documents = [
  { id:'overview', title:'Overview', path:'db/reports/overview.html', order:0 },
  { id:'motion', title:'Spending history', path:'db/reports/history.html', order:1, motion:true },
  { id:'brief', title:'Project brief', path:'db/reports/brief.html', order:2, artifact:true },
 ]
 await render('cowork', async host => {
  expect(host.textContent).not.toContain('Results')
  expect(host.textContent).not.toContain('Finished files')
  expect([...host.querySelectorAll('summary')].map(node=>node.textContent)).toEqual(['Dashboards1','Motion dashboards1','Artifacts1'])
  for (const [title,path] of [['Overview','overview.html'],['Spending history','history.html'],['Project brief','brief.html']]) {
   const row=[...host.querySelectorAll('button')].find(node=>node.textContent===title)!
   await act(async()=>row.click())
   expect(selectReportDocument).toHaveBeenLastCalledWith('Crew/example',`db/reports/${path}`)
   expect(mocks.open).toHaveBeenLastCalledWith('dashboard')
  }
  mocks.open.mockClear()
  mocks.user={ id:'another-user', allowed_products:null }
  await act(async()=>[...host.querySelectorAll('button')].find(node=>node.textContent==='Project brief')!.click())
  expect(mocks.open).not.toHaveBeenCalled()
 })
 const host=document.createElement('div'); const root=createRoot(host)
 try {
  await act(async()=>root.render(<WorkContextPanel workspacePath="Crew/example" panels={[]} onOpen={mocks.open}/>))
  expect(host.textContent).not.toContain('Overview'); expect(host.textContent).not.toContain('Project brief')
 } finally { await act(async()=>root.unmount()) }
})
it('keeps every starter destination in the admitted All tools inventory and Run mode limited to dashboard', () => {
 const ids = crewProjectMenuItems({ panels: inventory(), projectId:'example', allowedProducts:null }).map(item => item.id)
 for (const starter of CREW_STARTER_TYPES) for (const shortcut of crewStarterShortcuts(starter.id)) expect(ids).toContain(shortcut)
 expect(workWorkspacePanels({ readOnly:true }).views.map(item => item.id)).toEqual(['dashboard'])
 expect(inventory(new Set(['skills'])).map(item => item.id)).not.toContain('files')
})
