// @vitest-environment happy-dom
import { act } from 'react'
import type { ReactNode } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import type { CrewDashboardsTab, CrewPeopleView } from './teamApi'

const mocks = vi.hoisted(() => ({ people: vi.fn(), change: vi.fn(), tab: vi.fn(), select: vi.fn() }))
vi.mock('./teamApi', async importOriginal => ({
  ...(await importOriginal<typeof import('./teamApi')>()),
  useTeamTab: () => ({
    view: { team: { project_id: 'team', name: 'Team', owner_id: 'o', owner_name: 'demo', on: false, people: [], folder: {}, can_manage: true, folder_access: true }, intro: '', people: [], agents: [], can_switch: false, can_leave: false, can_choose: false, can_add_folder: false, can_remove_folder: false, asks: {} },
    target: { project: 'team' }, error: '',
  }),
  crewPeople: mocks.people, changeCrewPerson: mocks.change, crewDashboardsTab: mocks.tab,
}))
vi.mock('../../components/workflow/reportDocuments', () => ({ selectReportDocument: mocks.select }))
import { TeamTab } from './TeamTab'
import { CrewDashboardsTab as DashboardsTabCard } from './CrewDashboardsTab'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const levels = [
  { key: 'works', label: 'Works with me', line: 'Their own copy of the shared files.', available: true },
  { key: 'dashboards', label: 'Dashboards only', line: 'Opens all this Crew\'s dashboards and can ask this Crew.', available: true },
] as const
const view = (extra: Partial<CrewPeopleView> = {}): CrewPeopleView => ({
  kind: 'server', team_on: false, intro: 'Add someone once. Choose how they use this Crew.', can_add: true, can_share: true, gets: [], levels: [...levels],
  owner: { user_id: 'o', name: 'demo', level: 'owner', level_label: 'Owner', line: 'Owner', owner: true, you: true },
  people: [{ user_id: 'a', name: 'anita', level: 'dashboards', level_label: 'Dashboards only', line: 'All this Crew\'s dashboards. Can ask this Crew. No files.', moves: ['works', 'none'] }],
  ...extra,
})
const mount = async (node: ReactNode) => {
  const host = document.createElement('div'), root = createRoot(host)
  await act(async () => root.render(node))
  await act(async () => {}) // the server's reply
  return { host, unmount: () => act(async () => root.unmount()) }
}
const choose = async (select: HTMLSelectElement, value: string) => {
  Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!.call(select, value)
  await act(async () => { select.dispatchEvent(new Event('change', { bubbles: true })) })
}
afterEach(() => { vi.clearAllMocks(); document.body.innerHTML = '' })

// People card (owner, 6 Nov): the server's lines and levels; a change shows what the person gains or loses first and is
// saved only after the confirm button (confirm=true); the Team switch is not derived from people.
it('moving a person asks first with the server\'s lines, and saves only on confirm', async () => {
  mocks.people.mockResolvedValue(view())
  mocks.change.mockResolvedValueOnce({ confirm_needed: true, preview: { person: 'anita', from: 'dashboards', to: 'works', summary: 'anita will move from dashboards only to works with me.', gains: ['The shared files, with their own copy'], loses: [], notes: ['The Team is off, so they get this when you turn it on.'] } })
    .mockResolvedValueOnce({ changed: true })
  const { host, unmount } = await mount(<TeamTab projectId="team" />)
  try {
    expect(host.textContent).toContain('All this Crew\'s dashboards. Can ask this Crew. No files.')
    await choose(host.querySelector<HTMLSelectElement>('select[aria-label="anita: how they use this Crew"]')!, 'works')
    expect(mocks.change).toHaveBeenCalledTimes(1)
    expect(mocks.change).toHaveBeenLastCalledWith({ project: 'team' }, { person: 'a', level: 'works' }) // no confirm yet
    const dialog = document.body.querySelector('[role="dialog"]')!
    expect(dialog.textContent).toContain('anita will move from dashboards only to works with me.')
    expect(dialog.textContent).toContain('The shared files, with their own copy')
    expect(dialog.textContent).toContain('The Team is off, so they get this when you turn it on.')
    await act(async () => [...dialog.querySelectorAll('button')].find(button => button.textContent === 'Move')!.click())
    expect(mocks.change).toHaveBeenLastCalledWith({ project: 'team' }, { person: 'a', level: 'works', confirm: true, create_dashboard: false })
    expect(document.body.querySelector('[role="dialog"]')).toBeNull()
  } finally { await unmount() }
})

it('a Crew on a Mac shows the server\'s put-it-online line and takes the person to Mac online', async () => {
  mocks.people.mockResolvedValue(view({ kind: 'mac_online', people: [{ user_id: 'a', name: 'anita', level: 'works', level_label: 'Works with me', line: 'Files, agents and dashboards. Has their own copy.', moves: ['dashboards', 'none'] }], put_online: 'To give someone dashboards only, with Ask and buttons, put this Crew online first.', levels: [levels[0], { ...levels[1], available: false, why: 'To give someone dashboards only, with Ask and buttons, put this Crew online first.' }] }))
  const opened: unknown[] = []
  const listen = (event: Event) => opened.push((event as CustomEvent).detail)
  window.addEventListener('agentworks:open-crew-access', listen)
  const { host, unmount } = await mount(<TeamTab projectId="team" />)
  try {
    expect(host.textContent).toContain('put this Crew online first.')
    const option = [...host.querySelectorAll('option')].find(item => item.textContent === 'Dashboards only' && item.getAttribute('value') === 'dashboards' && item.disabled)
    expect(option).toBeDefined() // the level cannot be chosen, and the server's reason is its tooltip
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Put it online')!.click())
    expect(opened).toEqual([{ projectId: 'team', section: 'mac' }])
  } finally { window.removeEventListener('agentworks:open-crew-access', listen); await unmount() }
})

// Dashboards tab (owner, 6 Nov): the server's rows, filters and lines; Change goes to People; a teammate has no controls.
const tab = (extra: Partial<CrewDashboardsTab> = {}): CrewDashboardsTab => ({
  workspace: 'Crew/team', intro: 'Everything published from this Crew.', can_change: true, filter: 'all',
  filters: [{ key: 'all', label: 'All', count: 2 }, { key: 'dashboard', label: 'Dashboards', count: 1 }, { key: 'artifact', label: 'Artifacts', count: 1 }],
  items: [
    { id: 'sales', title: 'Sales report', path: 'db/reports/sales.html', kind: 'dashboard', line: 'Dashboard · updated 5 min ago', who: 'people who work with you, anita' },
    { id: 'brief', title: 'Launch brief', path: 'db/reports/brief.html', kind: 'artifact', line: 'Artifact · updated yesterday', who: 'people who work with you' },
  ], ...extra,
})

it('the Dashboards tab shows the server\'s rows, opens one, sends Change to People and filters through the server', async () => {
  mocks.tab.mockResolvedValue(tab())
  const opened: unknown[] = []
  const listen = (event: Event) => opened.push((event as CustomEvent).detail)
  window.addEventListener('agentworks:open-crew-access', listen)
  const { host, unmount } = await mount(<DashboardsTabCard projectId="team" />)
  try {
    expect(host.textContent).toContain('Sales report')
    expect(host.textContent).toContain('Dashboard · updated 5 min ago')
    expect(host.textContent).toContain('people who work with you, anita')
    const open = [...host.querySelectorAll('button')].filter(button => button.textContent === 'Open')[0]
    await act(async () => open.click())
    expect(mocks.select).toHaveBeenLastCalledWith('Crew/team', 'db/reports/sales.html', true)
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Change')!.click())
    expect(opened).toEqual([{ projectId: 'team', section: 'team' }])
    mocks.tab.mockResolvedValue(tab({ filter: 'artifact', items: [tab().items[1]] }))
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Artifacts 1')!.click())
    expect(mocks.tab).toHaveBeenLastCalledWith({ project: 'team' }, 'artifact')
    expect(host.textContent).not.toContain('Sales report')
  } finally { window.removeEventListener('agentworks:open-crew-access', listen); await unmount() }
})

it('a teammate has no Change and sees the server\'s note; an empty Crew shows the server\'s empty state', async () => {
  mocks.tab.mockResolvedValue(tab({ can_change: false, note: 'Only the owner changes who can open these.' }))
  const first = await mount(<DashboardsTabCard projectId="team" />)
  try {
    expect(first.host.textContent).toContain('Only the owner changes who can open these.')
    expect([...first.host.querySelectorAll('button')].some(button => button.textContent === 'Change')).toBe(false)
  } finally { await first.unmount() }
  mocks.tab.mockResolvedValue(tab({ items: [], filters: [{ key: 'all', label: 'All', count: 0 }], empty: { title: 'No dashboards yet', line: 'Your agents can turn this Crew\'s work into a dashboard, a document or a short video, ready to share.', ask: 'Help me make a dashboard for this Crew', example: 'e.g. "make a dashboard of this week\'s sales"' } }))
  const second = await mount(<DashboardsTabCard projectId="team" />)
  try {
    expect(second.host.textContent).toContain('No dashboards yet')
    expect(second.host.textContent).toContain('make a dashboard of this week')
  } finally { await second.unmount() }
})
