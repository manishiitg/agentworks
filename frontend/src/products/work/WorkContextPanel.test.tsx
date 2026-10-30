// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
vi.hoisted(() => vi.stubGlobal('localStorage', { getItem: () => null, setItem: () => {}, removeItem: () => {} }))
const mocks = vi.hoisted(() => ({ target: vi.fn(), open: vi.fn(), share: true, user: { id: 'alice', allowed_products: null as string[] | null }, server: 'local', documents: [] as { id: string; path: string; title: string; order: number }[], status: {} as Record<string, number | string> }))
vi.mock('../../hooks/useWorkspaceViewTarget', () => ({ setWorkspaceViewTarget: mocks.target }))
vi.mock('../../services/api', () => ({ default: { post: async () => ({ data: { can_share: mocks.share } }) }, getApiBaseUrl: () => '', getAuthToken: () => null, agentApi: { listReportHumanInputs: async () => ({ success: true, inputs: [] }) } }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: Object.assign((select: any) => select({ user: mocks.user }), { getState: () => ({ user: mocks.user }) }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ getWorkspaceScopedStorageKey: (key: string) => key, useWorkspaceConnectionStore: Object.assign((select: any) => select({ activeWorkspaceId: mocks.server }), { getState: () => ({ activeWorkspaceId: mocks.server }) }) }))
vi.mock('../../utils/productWorkspaceNavigation', () => ({ openProductWorkspace: vi.fn() }))
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: () => {} }))
vi.mock('../../components/integrations/AgentInboxPanel', () => ({ useAgentInboxEnabled: () => true }))
vi.mock('../../components/workspace/WorkspaceBrowserRow', () => ({ WorkspaceBrowserRow: ({ onClick }: { onClick: () => void }) => <button onClick={onClick}>Browser</button> }))
vi.mock('../../components/workspace/useProjectStatus', () => ({ useProjectStatus: () => mocks.status }))
vi.mock('../../components/workflow/reportDocuments', () => ({ useReportDocuments: () => ({ documents: mocks.documents, defaultPath: 'db/reports/index.html' }), selectReportDocument: vi.fn() }))
import { selectReportDocument } from '../../components/workflow/reportDocuments'
import { WorkContextPanel } from './WorkContextPanel'
import { workWorkspacePanels } from './workWorkspacePanels'
import { useProjectMenuHistoryStore, projectMenuHistoryScope } from '../../stores/useProjectMenuHistoryStore'
import { CREW_STARTER_TYPES, crewStarterShortcuts } from './crewStarterTypes'
import { crewProjectMenuItems } from './crewProjectMenu'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
beforeEach(() => {
  mocks.share = true; mocks.user = { id: 'alice', allowed_products: null }; mocks.server = 'local'; mocks.documents = []; mocks.status = {}
  useProjectMenuHistoryStore.setState({ recent: {}, startHere: {} })
  window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['work', 'knowledgebase', 'mcp-gateway', 'bullpen'], gatewayUrl: 'http://vault' }
})
afterEach(() => { vi.clearAllMocks(); delete window.__APP_RUNTIME_CONFIG__ })
const inventory = (enabledPanels?: Set<string>) => { const value = workWorkspacePanels({ enabledPanels, showShell: true }); return [...value.views, ...value.ops, ...value.setup] }
const scope = (project = 'example') => projectMenuHistoryScope(mocks.server, mocks.user.id, 'work', project)
async function render(check: (host: HTMLDivElement, update: (project?: string, features?: Set<string>) => Promise<void>) => Promise<void>) {
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  const update = async (project = 'example', features?: Set<string>) => { await act(async () => root.render(<WorkContextPanel workspacePath={'Crew/' + project} projectId={project} panels={inventory(features)} onOpen={mocks.open} enabledPanels={features} />)) }
  try { await update(); await check(host, update) } finally { await act(async () => root.unmount()); host.remove() }
}
function button(host: HTMLElement, label: string) { return [...host.querySelectorAll('button')].find(node => node.textContent === label)! }
it('uses the five category groups, opens Build for a new Crew and preserves every destination', async () => {
  await render(async host => {
    expect([...host.querySelectorAll('summary')].map(node => node.textContent)).toEqual(['Build', 'Use', 'Share', 'Integrations', 'Setup'])
    expect([...host.querySelectorAll('details')].map(node => node.open)).toEqual([true, false, false, false, false])
    for (const label of ['Dashboards', 'Functions', 'Skills', 'Files', 'Database', 'Plan', 'Schedules', 'Watchers', 'Webhooks', 'Browser', 'Chat history', 'Slack bot', 'MCPs', 'Access & sharing', 'Memory', 'Models', 'Instructions & identity', 'Terminal']) {
      expect([...host.querySelectorAll('button')].filter(node => node.textContent === label), label).toHaveLength(1)
    }
    await act(async () => button(host, 'Webhooks').click())
    expect(mocks.target).toHaveBeenLastCalledWith('workshop', 'triggers')
    expect(mocks.open).toHaveBeenLastCalledWith('schedules')
  })
})
it('moves recent destinations out of their categories, promotes them uniquely and keeps only ten', async () => {
  await render(async host => {
    const labels = ['Dashboards', 'Files', 'Skills', 'Plan', 'Database', 'Functions', 'Schedules', 'Watchers', 'Webhooks', 'Chat history', 'Memory']
    for (const label of labels) await act(async () => button(host, label).click())
    const recent = host.querySelector('section')!
    expect(recent.querySelector('h2')?.textContent).toBe('Recent')
    expect(recent.querySelectorAll('button')).toHaveLength(10)
    expect(recent.querySelector('button')?.textContent).toBe('Memory')
    expect([...recent.querySelectorAll('button')].some(node => node.textContent === 'Dashboards')).toBe(false)
    expect(button(host, 'Dashboards').closest('details')?.querySelector('summary')?.textContent).toBe('Build')
    for (const label of labels) expect([...host.querySelectorAll('button')].filter(node => node.textContent === label)).toHaveLength(1)
    await act(async () => button(host, 'Functions').click())
    expect(recent.querySelector('button')?.textContent).toBe('Functions')
    expect(useProjectMenuHistoryStore.getState().recent[scope()]).toHaveLength(10)
  })
})
it('filters inaccessible recent destinations and keeps histories separate per user, server and Crew', async () => {
  await render(async (host, update) => {
    await act(async () => button(host, 'Dashboards').click())
    await update('other')
    expect(host.querySelector('h2')?.textContent).not.toBe('Recent')
    await update()
    expect(host.querySelector('h2')?.textContent).toBe('Recent')
    mocks.user = { ...mocks.user, id: 'bob' }; await update()
    expect(host.querySelector('h2')?.textContent).not.toBe('Recent')
    mocks.user = { ...mocks.user, id: 'alice' }; mocks.server = 'remote'; await update()
    expect(host.querySelector('h2')?.textContent).not.toBe('Recent')
    mocks.server = 'local'; await update('example', new Set(['skills', 'schedules']))
    expect(button(host, 'Dashboards')).toBeUndefined()
    expect(host.querySelector('h2')?.textContent).not.toBe('Recent')
    const skills = button(host, 'Skills')
    mocks.user = { ...mocks.user, id: 'bob' }
    await act(async () => skills.click())
    expect(mocks.open).toHaveBeenCalledTimes(1)
  })
})
it('applies server sharing admission to both normal and recent entries', async () => {
  mocks.share = false
  useProjectMenuHistoryStore.getState().visit(scope(), 'mcp:share')
  await render(async host => {
    expect(button(host, 'Access & sharing')).toBeUndefined()
    expect(button(host, 'Owners & collaborators')).toBeUndefined()
    expect(host.querySelector('h2')?.textContent).not.toBe('Recent')
  })
})
it('honors server feature inventory and keeps Run accounts limited to dashboard', () => {
  const ids = inventory(new Set(['skills', 'schedules'])).map(item => item.id)
  expect(ids).toContain('memory'); expect(ids).toContain('history'); expect(ids).not.toContain('dashboard')
  expect(workWorkspacePanels({ readOnly: true }).views.map(item => item.id)).toEqual(['dashboard'])
})

it.each(CREW_STARTER_TYPES)('shows admitted $label starting shortcuts once and promotes visits to Recent', async starter => {
  useProjectMenuHistoryStore.getState().seedStartHere(scope(), crewStarterShortcuts(starter.id))
  await render(async host => {
    const start = host.querySelector('section')!
    expect(start.querySelector('h2')?.textContent).toBe('Start here')
    const first = start.querySelector<HTMLButtonElement>('button')!
    const label = first.textContent
    expect([...host.querySelectorAll('button')].filter(node => node.textContent === label)).toHaveLength(1)
    await act(async () => first.click())
    const recent = host.querySelector('section')!
    expect(recent.querySelector('h2')?.textContent).toBe('Recent')
    expect(recent.querySelector('button')?.textContent).toBe(label)
    expect([...host.querySelectorAll('button')].filter(node => node.textContent === label)).toHaveLength(1)
  })
})

it('maps every starter shortcut to a real destination in the fully admitted Crew menu', () => {
  const ids = crewProjectMenuItems({ panels: inventory(), projectId: 'example', allowedProducts: null }).map(item => item.id)
  for (const starter of CREW_STARTER_TYPES) for (const shortcut of crewStarterShortcuts(starter.id)) {
    expect(ids, `${starter.label}: ${shortcut}`).toContain(shortcut)
  }
})

it('applies feature and sharing admission to starting shortcuts and drops stale server clicks', async () => {
  mocks.share = false
  useProjectMenuHistoryStore.getState().seedStartHere(scope(), ['mcp:share', 'files', 'shell', 'memory:skills'])
  await render(async (host, update) => {
    expect(button(host, 'Access & sharing')).toBeUndefined()
    await update('example', new Set(['skills']))
    expect(button(host, 'Files')).toBeUndefined()
    const skills = button(host, 'Skills')
    mocks.server = 'other'
    await act(async () => skills.click())
    expect(mocks.open).not.toHaveBeenCalled()
  })
})

it('shows configured counts and laptop connection state beside their existing destinations', async () => {
  mocks.status = { skills: 32, googleApps: 1, secrets: 0, owners: 2, laptop: 'Not connected' }
  await render(async host => {
    for (const [label, detail] of [['Skills', '32']]) {
      const row = [...host.querySelectorAll('button')].find(node => node.textContent?.startsWith(label))!
      expect(row.textContent).toBe(label + detail)
    }
    await act(async () => [...host.querySelectorAll('button')].find(node => node.textContent === 'Skills32')!.click())
    expect(mocks.target).toHaveBeenLastCalledWith('memory', 'skills')
    expect(mocks.open).toHaveBeenLastCalledWith('memory')
  })
})

it('keeps all named dashboards inside one menu destination and promotes the group to Recent', async () => {
  mocks.documents = [
    { id: 'overview', path: 'db/reports/index.html', title: 'Overview', order: 0 },
    { id: 'budget', path: 'db/reports/budget.html', title: 'Budget', order: 1 },
  ]
  await render(async host => {
    const group = host.querySelector('[aria-label="Project dashboards"]')!
    expect(group.closest('details')?.querySelector('summary')?.textContent).toBe('Dashboards2')
    expect([...group.querySelectorAll('button')].map(row => row.textContent)).toEqual(['Overview', 'Budget'])
    await act(async () => button(host, 'Budget').click())
    expect(selectReportDocument).toHaveBeenLastCalledWith('Crew/example', 'db/reports/budget.html')
    expect(mocks.open).toHaveBeenLastCalledWith('dashboard')
    expect(useProjectMenuHistoryStore.getState().recent[scope()]).toEqual(['dashboard'])
    expect(host.querySelectorAll('[aria-label="Project dashboards"]')).toHaveLength(1)
    expect(host.querySelector('[aria-label="Project dashboards"]')?.closest('section')?.querySelector('h2')?.textContent).toBe('Recent')
  })
})
