// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
vi.hoisted(() => vi.stubGlobal('localStorage', { getItem: () => null, setItem: () => {}, removeItem: () => {} }))
const state = vi.hoisted(() => ({ enabled: true, canWrite: true, canShare: true, multiUser: false, user: { id: 'alice', allowed_products: ['agentworks', 'relays'] as string[] | null }, server: 'local', documents: [] as { id: string; path: string; title: string; order: number }[], open: vi.fn() }))
vi.mock('../../hooks/useCanWriteWorkflow', () => ({ useCanWriteWorkflow: () => state.canWrite }))
vi.mock('../../stores/useWorkflowStore', () => ({ useWorkflowStore: (select: any) => select({ openWorkspaceView: state.open }) }))
vi.mock('../../stores/useWorkflowManifestStore', () => ({ useWorkflowManifestStore: (select: any) => select({ workflows: [{ workspace_path: 'Workflow/example', manifest: { id: 'example', pulse: { enabled: state.enabled } } }] }) }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: Object.assign((select: any) => select({ isMultiUserMode: state.multiUser, user: state.user }), { getState: () => ({ user: state.user }) }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ getWorkspaceScopedStorageKey: (key: string) => key, useWorkspaceConnectionStore: Object.assign((select: any) => select({ activeWorkspaceId: state.server }), { getState: () => ({ activeWorkspaceId: state.server }) }) }))
vi.mock('../../services/api', () => ({ default: { post: async () => ({ data: { can_share: state.canShare } }) }, getApiBaseUrl: () => '', getAuthToken: () => null }))
vi.mock('../integrations/AgentInboxPanel', () => ({ useAgentInboxEnabled: () => true }))
vi.mock('./hooks/usePendingDecisionCount', () => ({ usePendingDecisionState: () => ({ loaded: true, count: 2 }) }))
vi.mock('./ShareDashboardButton', () => ({ ShareDashboardButton: ({ label, onOpen }: any) => <button onClick={onOpen}>{label}</button> }))
vi.mock('../workspace/WorkspaceBrowserRow', () => ({ WorkspaceBrowserRow: ({ onClick }: any) => <button onClick={onClick}>Browser</button> }))
vi.mock('../workspace/useProjectStatus', () => ({ useProjectStatus: () => ({}) }))
vi.mock('../workspace/WorkspaceAgentBridgeRows', () => ({ WorkspaceAgentBridgeRows: () => null }))
vi.mock('./reportDocuments', () => ({ useReportDocuments: () => ({ documents: state.documents, defaultPath: 'db/reports/index.html' }), selectReportDocument: vi.fn() }))
import { selectReportDocument } from './reportDocuments'
import { WorkflowContextPanel } from './WorkflowContextPanel'
import { RelayContextPanel } from './RelayContextPanel'
import { useProjectMenuHistoryStore, projectMenuHistoryScope } from '../../stores/useProjectMenuHistoryStore'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let host: HTMLDivElement, root: Root
beforeEach(() => {
  Object.assign(state, { enabled: true, canWrite: true, canShare: true, multiUser: false, server: 'local', documents: [], user: { id: 'alice', allowed_products: ['agentworks', 'relays'] } })
  useProjectMenuHistoryStore.setState({ recent: {} }); host = document.createElement('div'); document.body.append(host); root = createRoot(host)
})
afterEach(async () => { await act(async () => root.unmount()); host.remove(); vi.clearAllMocks() })
const render = (relay = false, path = 'Workflow/example') => act(async () => root.render(relay ? <RelayContextPanel workspacePath={path} /> : <WorkflowContextPanel workspacePath={path} />))
const button = (label: string) => [...host.querySelectorAll('button')].find(node => (node.getAttribute('aria-label') || node.textContent) === label)!
const scope = (relay = false, project = 'example') => projectMenuHistoryScope(state.server, state.user.id, relay ? 'relays' : 'agentworks', project)

it.each([false, true])('uses Crew category order and keeps every product destination (%s)', async relay => {
  await render(relay)
  expect([...host.querySelectorAll('summary')].map(node => node.textContent)).toEqual(['Build', 'Use', 'Share', 'Integrations', 'Setup'])
  expect([...host.querySelectorAll('details')].map(node => node.open)).toEqual([true, false, false, false, false])
  for (const label of ['Dashboards', 'Files', 'Skills', 'Chat history', 'People & access', 'Webhooks', 'Watchers', 'MCP access', 'Models', 'Settings', 'Run logs', 'Costs and usage']) expect(button(label), label).toBeDefined()
  if (relay) {
    for (const label of ['Flow', 'Runs', 'Code']) expect(button(label)).toBeDefined()
    for (const label of ['Pulse', 'Publish', 'Notify', 'Schedules', 'Functions', 'Upgrades', 'Database']) expect(button(label)).toBeUndefined()
  } else {
    for (const label of ['Pulse', 'Publish', 'Notify', 'Schedules', 'Functions', 'Upgrades', 'Database', 'Knowledge base']) expect(button(label)).toBeDefined()
    expect(button('Pulse').closest('details')).toBeNull()
  }
  await act(async () => button('Webhooks').click())
  expect(state.open).toHaveBeenLastCalledWith('workshop', 'triggers')
})

it.each([false, true])('shows up to ten recent items once and keeps their correct destination (%s)', async relay => {
  await render(relay)
  await act(async () => button('Skills').click())
  expect(state.open).toHaveBeenLastCalledWith('mcp', 'skills')
  expect(useProjectMenuHistoryStore.getState().recent[scope(relay)]).toEqual(['mcp:skills'])
  expect([...host.querySelectorAll('h2')].some(node => node.textContent === 'Recent')).toBe(true)
  expect([...host.querySelectorAll('button')].filter(node => node.textContent === 'Skills')).toHaveLength(1)
  expect(button('Skills').closest('details')).toBeNull()
  await act(async () => useProjectMenuHistoryStore.getState().visit(scope(relay), 'mcp:skills'))
  expect(useProjectMenuHistoryStore.getState().recent[scope(relay)]).toEqual(['mcp:skills'])
  const destinations = ['report', 'files', 'mcp:skills', 'workshop:chats', 'costs', 'execution-logs', 'identity:general', 'identity:llm', 'mcp:cli', 'workshop:triggers', 'workshop:conditions']
  await act(async () => destinations.forEach(id => useProjectMenuHistoryStore.getState().visit(scope(relay), id)))
  expect(useProjectMenuHistoryStore.getState().recent[scope(relay)]).toHaveLength(10)
  const recent = [...host.querySelectorAll('section')].find(node => node.querySelector('h2')?.textContent === 'Recent')!
  expect(recent.querySelectorAll('button')).toHaveLength(10)
  for (const row of recent.querySelectorAll('button')) expect([...host.querySelectorAll('button')].filter(node => node.textContent === row.textContent)).toHaveLength(1)
})

it('isolates history by workspace, product, account and server and ignores stale destinations', async () => {
  await render(); await act(async () => button('Skills').click())
  for (const update of [() => render(true), () => render(false, 'Workflow/another'), async () => { state.user = { ...state.user, id: 'bob' }; await render() }, async () => { state.server = 'other'; await render() }]) {
    await update(); expect([...host.querySelectorAll('h2')].some(node => node.textContent === 'Recent')).toBe(false)
  }
  await act(async () => useProjectMenuHistoryStore.getState().visit(scope(), 'removed-panel'))
  expect(host.textContent).not.toContain('removed-panel')
})

it('keeps role and sharing gates for current and recent items', async () => {
  await render(); await act(async () => button('People & access').click())
  state.canShare = false; state.user = { ...state.user, id: 'no-sharing' }; await render()
  await act(async () => useProjectMenuHistoryStore.getState().visit(scope(), 'mcp:share'))
  expect(button('People & access')).toBeUndefined()
  expect(button('Team')).toBeUndefined()
  expect(button('WhatsApp')).toBeDefined()
  state.canWrite = false; await render(); expect(host.textContent).toBe('')
})

it('retains separate Relay graph, runs and code targets', async () => {
  state.multiUser = true
  await render(true)
  expect(button('Team')).toBeUndefined()
  expect(button('WhatsApp')).toBeDefined()
  for (const [label, target] of [['Flow', 'graph'], ['Runs', 'calls'], ['Code', 'source']]) {
    await act(async () => button(label).click()); expect(state.open).toHaveBeenLastCalledWith('flow', target)
  }
})

it.each([false, true])('groups named dashboards once and preserves selection in Goals/Relay (%s)', async relay => {
  state.documents = [
    { id: 'overview', path: 'db/reports/index.html', title: 'Overview', order: 0 },
    { id: 'budget', path: 'db/reports/budget.html', title: 'Budget', order: 1 },
  ]
  await render(relay)
  expect([...host.querySelector('[aria-label="Project dashboards"]')!.querySelectorAll('button')].map(row => row.textContent)).toEqual(['Overview', 'Budget'])
  await act(async () => button('Budget').click())
  expect(selectReportDocument).toHaveBeenLastCalledWith('Workflow/example', 'db/reports/budget.html')
  expect(state.open).toHaveBeenLastCalledWith('report', undefined)
  expect(useProjectMenuHistoryStore.getState().recent[scope(relay)]).toEqual(['report'])
  expect(host.querySelectorAll('[aria-label="Project dashboards"]')).toHaveLength(1)
  expect(host.querySelector('[aria-label="Project dashboards"]')?.closest('section')?.querySelector('h2')?.textContent).toBe('Recent')
})

it('keeps Pulse as a permanent Goal agent entry, reflects off state and never duplicates it in Recent', async () => {
  useProjectMenuHistoryStore.getState().visit(scope(), 'pulse')
  await render()
  const pulse = button('Pulse')
  expect(pulse.title).toContain('Your goal agent')
  expect(pulse.textContent).toBe('PulseOn')
  expect(pulse.title).toContain('while you’re away')
  expect(pulse.textContent).toContain('On')
  expect(pulse.closest('section')).toBeNull()
  expect(host.querySelector('[aria-label="Workspace context"] button')).toBe(pulse)
  await act(async () => pulse.click())
  expect(state.open).toHaveBeenLastCalledWith('pulse', undefined)
  expect(host.querySelectorAll('[aria-label="Pulse"]')).toHaveLength(1)
  expect([...host.querySelectorAll('h2')].some(node => node.textContent === 'Recent')).toBe(false)
  state.enabled = false; await render()
  const off = button('Turn Pulse on')
  expect(off.textContent).toContain('Off')
  expect(off.title).not.toContain('while you’re away')
  expect(off.textContent).toBe('PulseOff')
  await act(async () => off.click())
  expect(state.open).toHaveBeenLastCalledWith('pulse', undefined)
  expect(state.enabled).toBe(false)
  await render(true)
  expect(button('Turn Pulse on')).toBeUndefined()
})
