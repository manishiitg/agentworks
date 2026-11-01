// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
vi.hoisted(() => vi.stubGlobal('localStorage', { getItem: () => null, setItem: () => {}, removeItem: () => {} }))
const mocks = vi.hoisted(() => ({ target: vi.fn(), open: vi.fn(), user: { id: 'owner', allowed_products: null as string[] | null }, server: 'local', share: { can_share:true, mode:'private', people:[] as { id:string }[], unknown_emails:[] as string[] }, browser: { connected:true, label:'Connected', name:'Chrome' }, documents: [] as { id: string; path: string; title: string; order: number; artifact?: boolean; motion?: boolean }[], status: { apps: [], automations: [], errors: [], loaded: ['mcp:connected'] } as any }))
vi.mock('../../hooks/useWorkspaceViewTarget', () => ({ setWorkspaceViewTarget: mocks.target }))
vi.mock('../../services/api', () => ({ default: { post: async () => ({ data: mocks.share }) }, getApiBaseUrl: () => '', getAuthToken: () => null, agentApi: {} }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: Object.assign((select: any) => select({ user: mocks.user }), { getState: () => ({ user: mocks.user }) }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ getWorkspaceScopedStorageKey: (key: string) => key, useWorkspaceConnectionStore: Object.assign((select: any) => select({ activeWorkspaceId: mocks.server }), { getState: () => ({ activeWorkspaceId: mocks.server }) }) }))
vi.mock('../../components/integrations/AgentInboxPanel', () => ({ useAgentInboxEnabled: () => true }))
vi.mock('./useWorkspaceStatus', () => ({ useWorkspaceStatus: () => ({ snapshot: mocks.status, refresh: vi.fn() }) }))
vi.mock('../../components/workspace/ChatChangesRow', () => ({ ChatChangesRow: ({ onOpen }: { onOpen: (reference?: { workspacePath: string; sessionId: string; snapshotId: string }) => void }) => <button onClick={() => onOpen({ workspacePath: 'Crew/example', sessionId: 'chat', snapshotId: 'latest' })}>Changes</button> }))
vi.mock('../../hooks/useBrowserToolbarConnection', () => ({ useBrowserToolbarConnection: () => mocks.browser }))
vi.mock('../../components/workflow/reportDocuments', () => ({ useReportDocuments: () => ({ documents: mocks.documents }), selectReportDocument: vi.fn() }))
import { selectReportDocument } from '../../components/workflow/reportDocuments'
import { WorkContextPanel } from './WorkContextPanel'
import { workWorkspacePanels } from './workWorkspacePanels'
import { CREW_STARTER_TYPES, crewStarterShortcuts } from './crewStarterTypes'
import { crewProjectMenuItems } from './crewProjectMenu'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
beforeEach(() => {
 mocks.user = { id: 'owner', allowed_products: null }; mocks.server = 'local'; mocks.documents = []
 mocks.share = { can_share:true, mode:'private', people:[], unknown_emails:[] }
 mocks.status = { apps: [], automations: [], errors: [], loaded: ['mcp:connected'] }; mocks.browser = { connected:true, label:'Connected', name:'Chrome' }
 window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['work', 'knowledgebase', 'mcp-gateway', 'bullpen'], gatewayUrl: 'http://example.test' }
})
afterEach(() => { vi.clearAllMocks(); delete window.__APP_RUNTIME_CONFIG__ })
const accessDestinations = [['Local laptop','laptop'],['Project sync','sync'],['Linked projects & folders','folders'],['Chats from your computer','agents'],['AgentBridge','agentbridge']]
const inventory = (features?: Set<string>) => { const value = workWorkspacePanels({ enabledPanels: features, showShell: true }); return [...value.views, ...value.ops, ...value.setup] }
async function render(mode: 'dev' | 'cowork', check: (host: HTMLDivElement) => Promise<void>) {
 const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
 try { await act(async () => root.render(<WorkContextPanel mode={mode} reviewSessionId="chat" workspacePath="Crew/example" projectId="example" panels={inventory()} onOpen={mocks.open}/>)); await check(host) }
 finally { await act(async () => root.unmount()); host.remove() }
}
it('shows actual workspace status without promoting occasional tools, and keeps All tools searchable', async () => {
 mocks.status.apps = [{ id: 'mcp:a', label: 'Notes account', detail: 'MCP · Connected', connected:true, destination: 'mcp:connected' }, { id: 'mcp:b', label: 'Other account', detail: 'MCP · Needs attention', destination: 'mcp:connected', attention:true }, { id:'google:g', label:'Google', detail:'person@example.com · Connected', connected:true, destination:'mcp:gmail' }, { id:'slack:s', label:'My Slack account', detail:'Personal Slack · Connected', connected:true, destination:'mcp:slack-account' }, { id:'bot:b', label:'Project helper', detail:'Project helper · Identity verified', connected:true, destination:'mcp:slack-bot' }, { id:'whatsapp', label:'WhatsApp',detail:'Connected',connected:true,destination:'mcp:whatsapp' }, {id:'github',label:'GitHub',detail:'chosen-account · Connected',connected:true,destination:'mcp:github'}]
 mocks.status.automations = [{ id: 'schedule:s', label: 'Morning summary', detail: 'Paused', destination: 'schedules:schedules' }, { id:'schedule:next',label:'Next summary',detail:'Jan 1, 8:00 AM',nextRunAt:'2099-01-01T08:00:00Z',destination:'schedules:schedules' }]
 await render('dev', async host => {
  await act(async () => [...host.querySelectorAll('button')].find(node => node.textContent === 'Changes')!.click())
  expect(mocks.target).toHaveBeenLastCalledWith('files', expect.stringContaining('reply:'))
  expect(host.textContent).not.toContain('person@example.com')
  expect(host.textContent).not.toContain('Morning summary')
  expect(host.querySelector('[aria-label="Automations"]')?.textContent).toContain('2 · Jan 1, 8:00 AM')
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Slack bot · Project helper · Identity verified"]')!.click())
  expect(mocks.target).toHaveBeenLastCalledWith('mcp', 'slack-bot')
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="WhatsApp · Connected"]')!.click())
  expect(mocks.target).toHaveBeenLastCalledWith('mcp', 'whatsapp')
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="GitHub · chosen-account · Connected"]')!.click())
  expect(mocks.target).toHaveBeenLastCalledWith('mcp', 'github')
  expect(host.querySelector('[aria-label="Google · person@example.com · Connected"]')).not.toBeNull()
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Google · person@example.com · Connected"]')!.click())
  expect(mocks.target).toHaveBeenLastCalledWith('mcp', 'gmail')
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Expand connections"]')!.click())
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Automations"]')!.click())
  expect(host.textContent).toContain('MCPs1 connected · 1 need attention')
  expect(host.textContent).not.toContain('Notes account'); expect(host.textContent).not.toContain('Other account')
  expect(host.textContent).toContain('Googleperson@example.com · Connected')
  expect(host.textContent).toContain('Morning summaryPaused')
  expect(host.textContent).toContain('Slack botProject helper · Identity verified')
  expect(host.textContent).toContain('My Slack accountPersonal Slack · Connected')
  for (const label of ['Recent', 'Costs & usage', 'Models', 'Chat history']) expect(host.textContent).not.toContain(label)
  const event = vi.fn(); window.addEventListener('open-quick-switcher', event)
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="All tools"]')!.click())
  expect(event.mock.calls[0][0].detail.query).toBe('@panels '); window.removeEventListener('open-quick-switcher', event)
  const connections = host.querySelector('[aria-label="Connections"]')!.closest('section')!
  const manage = [...connections.querySelectorAll('button')].find(node => node.textContent === 'Manage integrations')!
  mocks.open.mockClear(); mocks.target.mockClear()
  await act(async () => manage.click())
  expect(mocks.open).not.toHaveBeenCalled(); expect(mocks.target).not.toHaveBeenCalled()
  for (const [label] of accessDestinations) expect([...host.querySelectorAll('button')].some(node => node.querySelector('span')?.textContent === label)).toBe(false)
  for (const [label, target] of [['Google apps','gmail'],['My Slack account','slack-account'],['GitHub','github'],['MCPs','connected'],['WhatsApp','whatsapp']]) {
   const row = [...host.querySelectorAll('button')].find(node => node.querySelector('span')?.textContent === label)!
   expect(row.textContent).toContain(label === 'MCPs' ? '1 connected · 1 need attention' : 'Connected')
   await act(async () => row.click())
   expect(mocks.target).toHaveBeenLastCalledWith('mcp', target)
   expect(mocks.open).toHaveBeenLastCalledWith('mcp')
  }
  const browser = [...host.querySelectorAll('button')].find(node => node.querySelector('span')?.textContent === 'Browser')!
  expect(browser.textContent).toContain('Chrome · Connected')
  await act(async () => browser.click()); expect(mocks.open).toHaveBeenLastCalledWith('browser')
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Back to workspace"]')!.click())
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Full menu"]')!.click())
  expect([...host.querySelectorAll('details')].find(node => node.querySelector('summary')?.textContent === 'Build')?.open).toBe(true)
  expect([...host.querySelectorAll('summary')].map(node => node.textContent)).toEqual(['Build','Use','Share','Integrations','Setup'])
  for (const label of ['Files','Models','Costs & usage','Terminal','My Slack account','GitHub','Access & sharing']) expect([...host.querySelectorAll('button')].some(node => node.textContent === label),label).toBe(true)
  expect(host.querySelector('[aria-label="Back to workspace"]')?.textContent).toBe('Full menu')
  expect(host.textContent).not.toContain('Back to workspace')
  const files = [...host.querySelectorAll('button')].find(node => node.textContent === 'Files')!
  await act(async () => files.click()); expect(mocks.open).toHaveBeenLastCalledWith('files')
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Back to workspace"]')!.click())
  expect(host.textContent).not.toContain('Models')
  mocks.open.mockClear()
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Expand connections"]')!.click())
  const staleManage = [...host.querySelectorAll('button')].find(node => node.textContent === 'Manage integrations')!
  mocks.server = 'another-server'; await act(async () => staleManage.click())
  expect(mocks.open).not.toHaveBeenCalled()
  expect(host.querySelector('[aria-label="Back to workspace"]')).toBeNull()
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
  expect(host.querySelector('[aria-label="Dashboards"]')?.textContent).toBe('Dashboards3')
  expect(host.textContent).not.toContain('Overview')
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Dashboards"]')!.click())
  expect(host.querySelector('[aria-label="Artifacts"]')?.textContent).toContain('Project brief')
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
  expect(host.textContent).not.toContain('Manage integrations')
  expect(host.querySelector('[aria-label="Automations"]')).toBeNull()
 } finally { await act(async()=>root.unmount()) }
})
it('keeps every starter destination in the admitted All tools inventory and Run mode limited to dashboard', () => {
 const ids = crewProjectMenuItems({ panels: inventory(), projectId:'example', allowedProducts:null }).map(item => item.id)
 for (const starter of CREW_STARTER_TYPES) for (const shortcut of crewStarterShortcuts(starter.id)) expect(ids).toContain(shortcut)
 expect(workWorkspacePanels({ readOnly:true }).views.map(item => item.id)).toEqual(['dashboard'])
 expect(inventory(new Set(['skills'])).map(item => item.id)).not.toContain('files')
 const menu = (mode:'dev'|'cowork', features?:Set<string>) => crewProjectMenuItems({ panels:inventory(features), enabledPanels:features, mode }).map(item=>item.id)
 expect(menu('dev',new Set(['secrets']))).toContain('mcp:github')
 expect(menu('cowork',new Set(['secrets']))).not.toContain('mcp:github')
 expect(menu('dev',new Set(['bots']))).not.toContain('mcp:github')
})

it('keeps offline connections out of the icon strip but reachable through expansion', async () => {
 mocks.browser = { connected:false, label:'Browser offline', name:'Chrome' }
 mocks.status.apps = [
  { id:'google:offline',label:'Google',detail:'person@example.com · Needs attention',connected:false,attention:true,destination:'mcp:gmail' },
  { id:'whatsapp',label:'WhatsApp',detail:'Not connected',connected:false,attention:true,destination:'mcp:whatsapp' },
  { id:'github',label:'GitHub',detail:'Not connected',connected:false,destination:'mcp:github' },
  { id:'mcp:offline',label:'Notes',detail:'Needs attention',connected:false,attention:true,destination:'mcp:connected' },
 ]
 await render('dev', async host => {
  for (const prefix of ['Browser ·','Google ·','WhatsApp ·','GitHub ·','MCPs ·']) expect(host.querySelector(`[aria-label^="${prefix}"]`)).toBeNull()
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Expand connections"]')!.click())
  expect(host.textContent).toContain('person@example.com · Needs attention')
  const github = [...host.querySelectorAll('button')].find(node => node.textContent === 'GitHubNot connected')!
  await act(async () => github.click())
  expect(mocks.target).toHaveBeenLastCalledWith('mcp', 'github')
  const whatsapp = [...host.querySelectorAll('button')].find(node => node.textContent === 'WhatsAppNot connected')!
  await act(async () => whatsapp.click())
  expect(mocks.target).toHaveBeenLastCalledWith('mcp', 'whatsapp')
  await act(async () => [...host.querySelectorAll('button')].find(node => node.textContent === 'Manage integrations')!.click())
  expect(host.textContent).toContain('Google appsNeeds attention')
  expect(host.textContent).toContain('GitHubNot connected')
  expect(host.textContent).toContain('BrowserChrome · Browser offline')
 })
})


it('keeps footer sharing server-admitted and scoped to the current account', async () => {
 for (const [mode, detail] of [['private','Private'], ['everyone','Everyone'], ['people','2 people']]) {
  mocks.share = { can_share:true, mode, people:[{id:'a'},{id:'b'},{id:'a'}], unknown_emails:['missing@example.test'] }
  await render('cowork', async host => {
   const footer = host.querySelector('footer')!
   const share = [...footer.querySelectorAll('button')].find(node => node.textContent?.startsWith('Access & sharing'))!
   expect(share.textContent).toBe(`Access & sharing${detail}`)
   expect(footer.querySelector('button')).toBe(share)
   mocks.open.mockClear(); mocks.target.mockClear()
   await act(async () => share.click())
   expect(mocks.open).not.toHaveBeenCalled(); expect(mocks.target).not.toHaveBeenCalled()
   const sharingRow = [...host.querySelectorAll('button')].find(node => node.querySelector('span')?.textContent === 'Sharing')!
   if (mode === 'private') for (const [label,target] of accessDestinations) {
    await act(async () => [...host.querySelectorAll('button')].find(node => node.querySelector('span')?.textContent === label)!.click())
    expect(mocks.target).toHaveBeenLastCalledWith('mcp',target)
    expect(mocks.open).toHaveBeenLastCalledWith('mcp')
   }
   await act(async () => sharingRow.click())
   expect(mocks.target).toHaveBeenLastCalledWith('mcp','share')
   expect(mocks.open).toHaveBeenLastCalledWith('mcp')
   mocks.open.mockClear()
   const owner = mocks.user
   mocks.user = { ...owner, id:'another-user' }
   await act(async () => sharingRow.click())
   expect(mocks.open).not.toHaveBeenCalled()
   mocks.user = owner
  })
 }
 mocks.share = { can_share:false, mode:'private', people:[], unknown_emails:[] }
 await render('cowork', async host => {
  expect(host.querySelector('footer')?.textContent).not.toContain('Access & sharing')
  await act(async () => [...host.querySelector('footer')!.querySelectorAll('button')].find(node => node.querySelector('span')?.textContent === 'Access')!.click())
  expect([...host.querySelectorAll('button')].some(node => node.querySelector('span')?.textContent === 'Sharing')).toBe(false)
  expect([...host.querySelectorAll('button')].some(node => node.querySelector('span')?.textContent === 'Project sync')).toBe(false)
  await act(async () => [...host.querySelectorAll('button')].find(node => node.querySelector('span')?.textContent === 'Local laptop')!.click())
  expect(mocks.target).toHaveBeenLastCalledWith('mcp','laptop')
 })
})


it('opens automation setup before any jobs exist and keeps native destination guards', async () => {
 await render('cowork', async host => {
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Automations"]')!.click())
  const section = host.querySelector('[aria-label="Automations"]')!.closest('section')!
  await act(async () => [...section.querySelectorAll('button')].find(node => node.textContent === 'Manage automations')!.click())
  expect(mocks.open).not.toHaveBeenCalled(); expect(mocks.target).not.toHaveBeenCalled()
  for (const [label,target] of [['Schedules','schedules'],['Watchers','conditions'],['Webhooks','triggers'],['Functions','functions'],['Email inbox','email']]) {
   const row = [...host.querySelectorAll('button')].find(node => node.querySelector('span')?.textContent === label)!
   await act(async () => row.click())
   expect(mocks.target).toHaveBeenLastCalledWith('workshop', target)
   expect(mocks.open).toHaveBeenLastCalledWith('schedules')
  }
  const staleRow = [...host.querySelectorAll('button')].find(node => node.querySelector('span')?.textContent === 'Schedules')!
  mocks.open.mockClear(); mocks.server = 'another-server'
  await act(async () => staleRow.click())
  expect(mocks.open).not.toHaveBeenCalled()
 })
})

// PLAT-746-289: a partial snapshot is not evidence of zero MCP connections.
it.each([false, true])('keeps unresolved MCP status distinct from a failed check (failed=%s)', async failed => {
 mocks.status.loaded = ['mcp:github']
 mocks.status.errors = failed ? ['Connections could not be loaded.'] : []
 mocks.status.apps = [{id:'github',label:'GitHub',detail:'chosen-account · Connected',connected:true,destination:'mcp:github'}]
 await render('dev', async host => {
  expect(host.querySelector('[aria-label="GitHub · chosen-account · Connected"]')).not.toBeNull()
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Expand connections"]')!.click())
  expect(host.textContent).toContain(failed ? 'MCPsUnavailable' : 'MCPsChecking…')
  expect(host.textContent).not.toContain('0 connected')
  const manage = [...host.querySelectorAll('button')].find(node => node.textContent === 'Manage integrations')!
  await act(async () => manage.click())
  expect(host.textContent).not.toContain('0 connected')
 })
})


it('shows one status notice and one retry for a batch of failed services', async () => {
 mocks.status.errors = ['Some automations could not be loaded.','Some automations could not be loaded.','Some automations could not be loaded.','GitHub status could not be checked.','Connections could not be loaded.']
 await render('dev', async host => {
  const notice = host.querySelector('[role="status"]')!
  expect(notice.querySelectorAll('p')).toHaveLength(1)
  expect([...notice.querySelectorAll('button')].filter(button=>button.textContent==='Retry')).toHaveLength(1)
  for (const error of mocks.status.errors) expect(notice.textContent).not.toContain(error)
 })
})
