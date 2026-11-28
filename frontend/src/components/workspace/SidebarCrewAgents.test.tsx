// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { SidebarAgentLayout, SidebarCrewAgents } from './SidebarCrewAgents'
const f = vi.hoisted(() => ({ remove: vi.fn(), select: vi.fn(), open: vi.fn(), markUnread: vi.fn(), copy: vi.fn(), fork: vi.fn(), setSidebarHidden: vi.fn(), hidden: new Set<string>(), active: 'a', canHide: false, extra: [] as Record<string, unknown>[], closedExtra: [] as Record<string, unknown>[], groups: {} as Record<string, string>, edit: vi.fn(async (..._args: unknown[]) => {}) }))
vi.mock('../chat/AgentCard', async importOriginal => ({
 ...await importOriginal<typeof import('../chat/AgentCard')>(),
 useCrewAgentsStore: () => ({ projectId: 'crew', agents: [
  { tabId: 'b', name: 'Second', sidebarOrder: 0, sidebarHidden: f.hidden.has('b'), actions: { forkTo: f.copy, fork: f.fork, ...(f.canHide ? { setSidebarHidden: f.setSidebarHidden } : {}) }, info: { name: 'Second', onRemove: f.remove } },
  { tabId: 'a', name: 'First', sidebarOrder: 1 }, { tabId: 'h', name: 'Third', sidebarOrder: 3, sidebarHidden: f.hidden.has('h') }, ...f.extra,
 ], closed: [{ key: 'closed', name: 'Closed fixture', sidebarOrder: 2, sidebarHidden: f.hidden.has('closed'), open: f.open }, ...f.closedExtra] }),
 AgentHoverCard: ({ children }: { children: React.ReactNode }) => <span data-details-card>{children}</span>,
}))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: (select: (s: unknown) => unknown) => select({ activeTabId: f.active, chatTabs: {b:{sessionId:'second-session'}}, activeSessionsCache: f.hidden.size ? [{session_id:'second-session',last_activity:new Date(Date.now()-41*60_000).toISOString()}] : [], markTabUnread: f.markUnread }) }))
vi.mock('../../stores/useLLMStore', () => ({ useLLMStore: (select: (s: unknown) => unknown) => select({ providerManifest: [] }) }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: (s: unknown) => unknown) => select({ user: { id: 'sidebar-owner' } }) }))
vi.mock('../../products/work/teamApi', () => ({ useTeamTab: () => ({}), teamAgentFor: () => undefined }))
vi.mock('../../products/work/OwnTerminalAgents', () => ({ OwnTerminalAgents: () => null }))
vi.mock('../chat/agentMenuState', () => ({ useAgentMenuState: () => null }))
vi.mock('./agentGroups', async importOriginal => ({ ...await importOriginal<typeof import('./agentGroups')>(), useAgentGroups: () => f.groups, useAgentGroupsStore: { getState: () => ({ edit: (_project: string, change: unknown) => f.edit(change) }) } }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('lets a person put an agent above a group and move the whole group back', async () => {
 const host = document.createElement('div'), root = createRoot(host)
 f.groups = { member: 'CLI agents' }
 f.extra = [
  { tabId: 'member', key: 'member', name: 'Tools', actions: { orderKey: 'member' } },
  { tabId: 'lead', key: 'lead', name: 'Lead', actions: { orderKey: 'lead' } },
 ]
 const drag = (type: string, target: Element) => {
  const event = new Event(type, { bubbles: true, cancelable: true })
  Object.defineProperty(event, 'dataTransfer', { value: { setData: () => {}, effectAllowed: '', dropEffect: '' } })
  target.dispatchEvent(event)
 }
 const live = () => root.render(<SidebarCrewAgents projectId="crew" />)
 const group = () => host.querySelector('[aria-label="Group CLI agents"]')!
 try {
  f.edit.mockClear()
  localStorage.clear()
  await act(async () => live())
  const lead = host.querySelector('[aria-label="More for Lead"]')!.closest('[draggable="true"]')!
  await act(async () => drag('dragstart', lead))
  const gap = host.querySelector('[data-sidebar-drop-before="group:CLI agents"]')!
  expect(gap).toBeTruthy()
  // Drop strip must not shift rows below it during dragstart (zero-height overlay).
  expect(gap.parentElement?.className).toContain('h-0')
  expect(gap.className).toContain('absolute')
  await act(async () => { drag('dragover', gap); drag('drop', gap) })
  expect(host.textContent!.indexOf('Lead')).toBeLessThan(host.textContent!.indexOf('CLI agents'))
  expect(group().textContent).toContain('Tools')
  expect(group().textContent).not.toContain('Lead')
  expect(f.edit).not.toHaveBeenCalled()

  await act(async () => root.render(<SidebarAgentLayout projectId="crew" rows={['member', 'lead']} keyOf={value => value} forkedFromOf={() => undefined} teammate={() => false} render={value => <span>{value}</span>} />))
  expect(host.textContent!.indexOf('lead')).toBeLessThan(host.textContent!.indexOf('CLI agents'))
  await act(async () => live())
  await act(async () => drag('dragstart', group().querySelector('[draggable="true"]')!))
  const aboveLead = host.querySelector('[data-sidebar-drop-before="agent:lead"]')!
  await act(async () => drag('drop', aboveLead))
  expect(host.textContent!.indexOf('CLI agents')).toBeLessThan(host.textContent!.indexOf('Lead'))
  expect(group().textContent).toContain('Tools')

  const leadAgain = host.querySelector('[aria-label="More for Lead"]')!.closest('[draggable="true"]')!
  await act(async () => drag('dragstart', leadAgain))
  await act(async () => drag('drop', group().querySelector('[draggable="true"]')!))
  expect(f.edit).toHaveBeenCalledWith({ op: 'move', key: 'lead', group: 'CLI agents' })
 } finally { f.groups = {}; f.extra = []; f.edit.mockClear(); localStorage.clear(); await act(async () => root.unmount()) }
})

// #2952: flattening preserves the existing order, selected agent and closed access.
it('lists own agents directly in their normal order without group controls', async () => {
 const host = document.createElement('div'), root = createRoot(host)
 try {
  await act(async () => root.render(<SidebarCrewAgents projectId="crew" onSelect={f.select}/>))
  expect([...host.querySelectorAll('[role="listitem"]')].map(row => row.textContent)).toEqual(['Second', 'First', 'Third'])
  expect(host.querySelector('[aria-current="page"]')?.textContent).toBe('First')
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-current="page"]')!.click())
  expect(f.select).toHaveBeenCalledWith('a')
  expect(host.querySelector('select, form, [data-details-card]')).toBeNull()
  await act(async () => [...host.querySelectorAll('button')].find(row => row.textContent?.includes('Closed agents'))!.click())
  await act(async () => [...host.querySelectorAll('button')].find(row => row.textContent==='Closed fixture')!.click())
  expect(f.open).toHaveBeenCalledOnce()
 } finally { await act(async () => root.unmount()) }
})

it('shows sharing marks only on shared agents and leaves private agents clean', async () => {
 const host = document.createElement('div'), root = createRoot(host)
 f.extra = Array.from({ length: 5 }, (_, index) => ({
  tabId: `shared-${index}`, name: `Shared agent ${index}`, inTeam: true,
  teamShare: index === 0
   ? { inTeam: true, audience: 'whole_team', chatShare: 'agent' }
   : index === 1
   ? { inTeam: true, audience: 'chosen', chosenPeople: ['Ana', 'Sam'], chatShare: 'live' }
   : { inTeam: true, audience: 'whole_team', chatShare: 'live' },
 }))
 try {
  await act(async () => root.render(<SidebarCrewAgents projectId="crew" />))
  // Private and chat-hidden agents stay clean in the left menu (owner, 26 Nov);
  // shared agents whose chat is visible always show the sharing icon (owner, 28 Nov).
  expect(host.querySelector('[aria-current="page"] [aria-label="Only you"]')).toBeNull()
  expect(host.querySelector('[aria-label*="Agent only"]')).toBeNull()
  const chosenMark = host.querySelector('[aria-label="Shared with Ana, Sam · Full chat"]')
  expect(chosenMark).not.toBeNull()
  expect(chosenMark?.className).toContain('inline-flex')
  expect(chosenMark?.className).not.toContain('hidden')
  expect(host.querySelector('[aria-label="Full chat: everyone on the Team can ask it and read its chat"]')).not.toBeNull()
  expect(host.textContent).not.toMatch(/In team unless marked with a lock|All in team/)
 } finally { f.extra = []; await act(async () => root.unmount()) }
})

it('marks an agent unread from its actions menu without opening the chat', async () => {
 const host = document.createElement('div'), root = createRoot(host)
 f.select.mockClear()
 try {
  await act(async () => root.render(<SidebarCrewAgents projectId="crew" onSelect={f.select}/>))
  expect(host.querySelector('[title="Mark as unread"]')).toBeNull()
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="More for Second"]')!.click())
  // "Remove agent…" lives here, last, not in the agent's top menu (owner, 20 Nov).
  expect([...host.querySelectorAll('[role="menuitem"]')].map(item => item.textContent)).toEqual(['Mark as unread', 'Fork chat', 'Remove agent…'])
  const unread = host.querySelector<HTMLButtonElement>('[role="menuitem"]')!
  expect(unread.textContent).toBe('Mark as unread')
  await act(async () => unread.click())
  expect(f.markUnread).toHaveBeenCalledWith('b')
  expect(f.select).not.toHaveBeenCalled()
  expect(host.querySelector('[role="menu"]')).toBeNull()
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="More for Second"]')!.click())
  await act(async () => [...host.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find(item => item.textContent === 'Remove agent…')!.click())
  expect(f.remove).toHaveBeenCalledOnce()
  expect(f.select).not.toHaveBeenCalled()
 } finally { await act(async () => root.unmount()) }
})


// #3092: a shown-again agent returns to the list, and selection doesn't promote hidden rows.
it('hides and shows own agents without moving or clearing the active chat or its time', async () => {
 const host = document.createElement('div'), root = createRoot(host)
 f.canHide = true
 f.hidden = new Set(['closed','h'])
 f.active = 'b'
 const select = vi.fn((tabId: string) => { f.active = tabId })
 const render = () => root.render(<SidebarCrewAgents projectId="crew" onSelect={select}/>)
 const click = async (label: string) => await act(async () => {
  const target = [...host.querySelectorAll('button')].find(row => row.getAttribute('aria-label') === label || row.textContent === label)
  expect(target, label).toBeTruthy()
  target!.click()
  render()
 })
 const hiddenRows = () => [...host.querySelectorAll('[aria-label="Archived agents"] [role="listitem"]')].map(row => row.textContent)
 f.setSidebarHidden.mockImplementation((hidden: boolean) => { if (hidden) f.hidden.add('b'); else f.hidden.delete('b') })
 try {
  await act(async () => render())
  await click('More for Second')
  await click('Archive')
  expect(select).not.toHaveBeenCalled()
  expect([...host.querySelectorAll('[role="listitem"]')].map(row => row.textContent)).toEqual(['First'])
  expect(hiddenRows()).toEqual([])
  await click('Archived · 3')
  expect(hiddenRows()).toEqual(['Second41m','Closed fixture','Third'])
  expect(host.querySelector('[aria-current="page"]')?.textContent).toBe('Second41m')
  await click('Third')
  expect(select).toHaveBeenCalledWith('h')
  expect(hiddenRows()).toEqual(['Second41m','Closed fixture','Third'])
  await click('More for Second')
  await click('Unarchive')
  expect(f.setSidebarHidden.mock.calls).toEqual([[true],[false]])
  expect([...host.querySelectorAll('[role="listitem"]')].map(row => row.textContent)).toContain('Second41m')
  expect(hiddenRows()).toEqual(['Closed fixture','Third'])
  expect(host.querySelector('[aria-current="page"]')?.textContent).toBe('Third')
 } finally {
  f.hidden.clear()
  f.active = 'a'
  f.canHide = false
  f.setSidebarHidden.mockReset()
  await act(async () => root.unmount())
 }
})


// #3288: your own groups come first, a fork sits under its parent, and a teammate's agent can sit in a group.
it('shows groups with their agents, forks under their parent without claiming missing agents were unshared', async () => {
 const host = document.createElement('div'), root = createRoot(host)
 f.hidden = new Set(); f.active = 'a'; f.canHide = false
 f.extra = [
  { tabId: 'p', key: 'crew:chat:parent', name: 'Parent', sidebarOrder: 4 },
  { tabId: 'c', key: 'crew:chat:fork', forkedFrom: 'crew:chat:parent', name: 'Parent (fork)', sidebarOrder: 5 },
  { tabId: 't', key: 'crew:chat:theirs', name: 'Their agent', ownerId: 'teammate', owner: 'Ana' },
  { tabId: 'u', key: 'crew:chat:other', name: 'Other agent', ownerId: 'user', owner: 'Bo' },
 ]
 f.groups = { 'crew:chat:parent': 'Design', 'crew:chat:theirs': 'Design', 'crew:chat:gone': 'Design' }
 const names = (selector: string) => [...host.querySelectorAll(`${selector} [role="listitem"]`)].map(row => row.textContent)
 const click = async (label: string) => await act(async () => {
  const target = [...host.querySelectorAll('button')].find(button => button.getAttribute('aria-label') === label || button.textContent === label)
  expect(target, label).toBeTruthy()
  target!.click()
 })
 try {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'Date'] })
  await act(async () => root.render(<SidebarCrewAgents projectId="crew" onSelect={f.select}/>))
  expect(names('[aria-label="Group Design"]')).toEqual(['Parent', 'Parent (fork)', 'ATheir agent'])
  // On online projects, other people's ungrouped chats are collapsed behind a toggle (owner, 26 Nov).
  expect(names('[aria-label="Agents"] > div:not([aria-label^="Group"])')).toEqual(['Second', 'First', 'Third'])
  await click('Teammates’ chats (1)')
  expect(names('[aria-label="Agents"] > div:not([aria-label^="Group"])')).toEqual(['Second', 'First', 'Third', 'BOther agent'])
  // A local Mac project hides them completely: Team members' agents stay in groups and the Team view.
  const runtimeWindow = window as Window & { __APP_RUNTIME_CONFIG__?: { deploymentMode?: string } }
  runtimeWindow.__APP_RUNTIME_CONFIG__ = { deploymentMode: 'local' }
  await act(async () => root.render(<SidebarCrewAgents projectId="crew" onSelect={f.select} key="local"/>))
  expect(names('[aria-label="Agents"] > div:not([aria-label^="Group"])')).toEqual(['Second', 'First', 'Third'])
  expect(host.textContent).not.toContain('Teammates’ chats')
  delete runtimeWindow.__APP_RUNTIME_CONFIG__
  await act(async () => root.render(<SidebarCrewAgents projectId="crew" onSelect={f.select}/>))
  // #3389: even a prolonged roster gap does not prove sharing was revoked.
  expect(host.textContent).not.toContain('no longer shared with you')
  await act(async () => { vi.advanceTimersByTime(3000) })
  vi.useRealTimers()
  expect(host.textContent).not.toContain('no longer shared with you')
  expect(host.textContent).not.toContain('Remove from group')
  f.extra.push({ tabId: 'late', key: 'crew:chat:gone', name: 'Late agent', ownerId: 'teammate', owner: 'Ana' })
  await act(async () => root.render(<SidebarCrewAgents projectId="crew" onSelect={f.select}/>))
  expect(names('[aria-label="Group Design"]')).toContain('ALate agent')
  await click('Hide forks of Parent')
  expect(names('[aria-label="Group Design"]')).toEqual(['Parent', 'ATheir agent', 'ALate agent'])
  expect(host.textContent).toContain('1 fork')
  await click('More for Parent')
  expect([...host.querySelectorAll('[role="menuitem"]')].map(item => item.textContent)).toEqual(expect.arrayContaining(['New group…', 'Take out of Design']))
  await click('Take out of Design')
  expect(f.edit).toHaveBeenLastCalledWith({ op: 'move', key: 'crew:chat:parent', group: '' })
  // The teammate picker had no way out but a Close button below a long list: a click outside closes it.
  // An Agent only teammate agent is listed but can't be added (owner, 28 Nov).
  f.extra.push({ tabId: 'ask-only', key: 'crew:chat:ask-only', name: 'Ask-only agent', ownerId: 'teammate', owner: 'Ana', messageOnly: true })
  await act(async () => root.render(<SidebarCrewAgents projectId="crew" onSelect={f.select}/>))
  await click('More for group Design')
  await click('Add a teammate’s agent…')
  expect(host.querySelector('[role="dialog"]')).toBeTruthy()
  const askOnly = [...host.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')].find(button => button.textContent?.startsWith('Ask-only agent'))!
  expect(askOnly.disabled).toBe(true)
  const edits = f.edit.mock.calls.length
  await act(async () => askOnly.click())
  expect(f.edit.mock.calls.length).toBe(edits)
  await act(async () => document.body.dispatchEvent(new MouseEvent('mousedown', { bubbles: true })))
  expect(host.querySelector('[role="dialog"]')).toBeNull()
 } finally { vi.useRealTimers(); f.extra = []; f.groups = {}; window.localStorage.clear(); await act(async () => root.unmount()) }
})

// Switching to another project showed this one's agents flat: a project that is not open keeps its groups and forks.
it('keeps groups and forks for a project that is not the open one', async () => {
 const host = document.createElement('div'), root = createRoot(host)
 f.groups = { 'k:parent': 'Design', 'k:theirs': 'Design', 'k:gone': 'Empty', 'k:team:k:chat:mate': 'Mates' }
 type R = { key: string; name: string; from?: string; theirs?: boolean }
 const rows: R[] = [{ key: 'k:parent', name: 'Parent' }, { key: 'k:fork', name: 'Parent (fork)', from: 'k:parent' }, { key: 'k:theirs', name: 'Their agent', theirs: true }, { key: 'k:other', name: 'Ungrouped teammate', theirs: true }, { key: 'k:solo', name: 'Solo' }]
 try {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'Date'] })
  await act(async () => root.render(<SidebarAgentLayout<R> projectId="crew" rows={rows} keyOf={r => r.key} forkedFromOf={r => r.from} teammate={r => !!r.theirs} render={r => <span role="listitem">{r.name}</span>} />))
  await act(async () => { vi.advanceTimersByTime(3000) })
  vi.useRealTimers()
  expect([...host.querySelectorAll('[aria-label="Group Design"] [role="listitem"]')].map(row => row.textContent)).toEqual(['Parent', 'Parent (fork)', 'Their agent'])
  expect(host.textContent).toContain('Solo')
  // Preserve the group without inventing a reason why its saved entry is missing.
  expect(host.querySelector('[aria-label="Group Empty"]')?.textContent).toContain('Open the project')
  expect(host.textContent).not.toContain('Remove from group')
  expect(host.textContent).not.toContain('Ungrouped teammate')
  // On the website teammates' agents are not listed at all: their grouped agent is not "no longer shared".
  expect(host.querySelector('[aria-label="Group Mates"]')?.textContent).not.toContain('no longer shared')
 } finally { vi.useRealTimers(); f.groups = {}; window.localStorage.clear(); await act(async () => root.unmount()) }
})


// A teammate's grouped agent vanished after switching projects: with no tab open here it was a Closed agent.
it("keeps a teammate's grouped agent in its group when its tab is not open", async () => {
 const host = document.createElement('div'), root = createRoot(host)
 f.closedExtra = [{ key: 'crew:team:m', owner: 'Maya', name: 'native agents', open: f.open }]
 f.groups = { 'crew:team:m': 'cliagents' }
 try {
  await act(async () => root.render(<SidebarCrewAgents projectId="crew" onSelect={f.select}/>))
  const group = host.querySelector('[role="list"][aria-label="cliagents"]')!
  expect(group.textContent).toContain('native agents')
  expect([...host.querySelectorAll('button')].find(row => row.textContent?.includes('Closed agents'))?.textContent).toBe('Closed agents (1)')
 } finally { f.closedExtra = []; f.groups = {}; await act(async () => root.unmount()) }
})

// #3504: the person moves a fork out from under its agent from its menu or by dragging it out; a note offers putting it
// back, and a refused move says so with Try again.
it('moves a fork out of its agent from the menu or by dragging, and puts it back', async () => {
 const host = document.createElement('div'), root = createRoot(host)
 const forkParent = vi.fn(async (_parent: string) => {})
 f.hidden = new Set(); f.active = 'a'; f.canHide = false; f.groups = {}
 f.extra = [
  { tabId: 'p', key: 'crew:chat:parent', name: 'Parent', sidebarOrder: 4, actions: { orderKey: 'crew:chat:parent' } },
  { tabId: 'c', key: 'crew:chat:fork', forkedFrom: 'crew:chat:parent', name: 'Parent (fork)', sidebarOrder: 5, actions: { orderKey: 'crew:chat:fork', forkParent } },
 ]
 const click = async (label: string) => await act(async () => {
  const target = [...host.querySelectorAll('button')].find(button => button.getAttribute('aria-label') === label || button.textContent === label)
  expect(target, label).toBeTruthy()
  target!.click()
 })
 const drag = (type: string, target: Element) => {
  const event = new Event(type, { bubbles: true, cancelable: true })
  Object.defineProperty(event, 'dataTransfer', { value: { setData: () => {}, effectAllowed: '', dropEffect: '' } })
  target.dispatchEvent(event)
 }
 try {
  await act(async () => root.render(<SidebarCrewAgents projectId="crew" onSelect={f.select}/>))
  await click('More for Parent (fork)')
  await click('Move out of Parent')
  expect(forkParent).toHaveBeenLastCalledWith('')
  expect(host.querySelector('[role="status"]')).toBeNull()

  forkParent.mockRejectedValueOnce(new Error('offline'))
  await click('More for Parent (fork)')
  await click('Move out of Parent')
  expect(host.querySelector('[role="alert"]')?.textContent).toContain('Couldn’t move Parent (fork) out. It’s still under Parent.')
  await click('Try again')
  expect(forkParent).toHaveBeenLastCalledWith('')
  expect(host.querySelector('[role="status"]')).toBeNull()

  forkParent.mockClear()
  const forkRow = [...host.querySelectorAll('[draggable="true"]')].find(row => row.textContent?.includes('Parent (fork)'))!
  await act(async () => drag('dragstart', forkRow))
  const zone = [...host.querySelectorAll('div')].find(div => div.textContent === 'Drop here to move it out of Parent')
  expect(zone).toBeTruthy()
  await act(async () => { drag('dragover', zone!); drag('drop', zone!) })
  expect(forkParent).toHaveBeenCalledWith('')
 } finally { f.extra = []; window.localStorage.clear(); await act(async () => root.unmount()) }
})

// #4152: drag onto the middle of another chat (or Main) to nest it as a sub-chat, drag onto the top edge to reorder,
// refuse nesting under its own sub-chat, and also allow nesting via the ⋯ menu (Move under…).
it('nests a chat under another chat by dragging or via Move under…, while top-edge drop still reorders', async () => {
 const host = document.createElement('div'), root = createRoot(host)
 const childForkParent = vi.fn(async (_parent: string) => {})
 const parentForkParent = vi.fn(async (_parent: string) => {})
 const targetDropHere = vi.fn()
 f.hidden = new Set(); f.active = 'a'; f.canHide = false; f.groups = {}
 f.extra = [
  { tabId: 'm', key: 'crew', isDefault: true, name: 'Agent', sidebarOrder: 0, actions: {} },
  { tabId: 'p', key: 'crew:chat:parent', name: 'Parent', sidebarOrder: 4, actions: { orderKey: 'crew:chat:parent', forkParent: parentForkParent, dropHere: targetDropHere } },
  { tabId: 'sub', key: 'crew:chat:sub', forkedFrom: 'crew:chat:parent', name: 'Child', sidebarOrder: 5, actions: { orderKey: 'crew:chat:sub', forkParent: vi.fn(async () => {}) } },
  { tabId: 't', key: 'crew:chat:topic', name: 'Topic', sidebarOrder: 6, actions: { orderKey: 'crew:chat:topic', forkParent: childForkParent } },
 ]
 const rect = (el: Element) => {
  Object.defineProperty(el, 'getBoundingClientRect', { configurable: true, value: () => ({ top: 100, bottom: 130, height: 30, left: 0, right: 200, width: 200 }) })
 }
 const drag = (type: string, target: Element, clientY = 118) => {
  const event = new MouseEvent(type, { bubbles: true, cancelable: true, clientY })
  Object.defineProperty(event, 'dataTransfer', { value: { setData: () => {}, effectAllowed: '', dropEffect: '' } })
  target.dispatchEvent(event)
 }
 const rowByKey = (key: string) => host.querySelector(`[data-sidebar-agent-row="${key}"]`)!
 const click = async (label: string, selector = 'button') => await act(async () => {
  const target = [...host.querySelectorAll(selector)].find(button => button.getAttribute('aria-label') === label || button.textContent?.trim() === label)
  expect(target, label).toBeTruthy()
  ;(target as HTMLElement).click()
 })
 try {
  await act(async () => root.render(<SidebarCrewAgents projectId="crew" onSelect={f.select}/>))
  const topicRow = rowByKey('crew:chat:topic')
  const parentRow = rowByKey('crew:chat:parent')
  const mainRow = rowByKey('crew')
  const subRow = rowByKey('crew:chat:sub')
  rect(parentRow); rect(mainRow); rect(subRow)

  // Drag Topic onto middle of Parent -> nest under Parent without an inline undo banner.
  await act(async () => drag('dragstart', topicRow))
  await act(async () => drag('dragover', parentRow, 118))
  expect(parentRow.textContent).toContain('Make sub-chat')
  await act(async () => drag('drop', parentRow, 118))
  expect(childForkParent).toHaveBeenLastCalledWith('crew:chat:parent')
  expect(targetDropHere).not.toHaveBeenCalled()
  expect(host.querySelector('[role="status"]')).toBeNull()

  // Drag Topic onto top edge of Parent -> reorder instead of nesting.
  childForkParent.mockClear()
  await act(async () => drag('dragstart', topicRow))
  await act(async () => drag('dragover', parentRow, 102))
  await act(async () => drag('drop', parentRow, 102))
  expect(targetDropHere).toHaveBeenCalledWith('crew:chat:topic')
  expect(childForkParent).not.toHaveBeenCalled()

  // Drag Topic onto the default agent (no orderKey/dropHere) -> nest under it.
  await act(async () => drag('dragstart', topicRow))
  await act(async () => drag('dragover', mainRow, 115))
  await act(async () => drag('drop', mainRow, 115))
  expect(childForkParent).toHaveBeenLastCalledWith('crew')

  // Drag Parent onto its own sub-chat Child -> refused cleanly.
  await act(async () => drag('dragstart', parentRow))
  await act(async () => drag('dragover', subRow, 118))
  expect(subRow.textContent).toContain('Own sub-chat')
  await act(async () => drag('drop', subRow, 118))
  expect(parentForkParent).not.toHaveBeenCalled()
  expect(host.querySelector('[role="alert"]')?.textContent).toContain('Parent can’t go under Child')

  // Menu fallback: Move under… lists eligible chats and nests on click.
  childForkParent.mockClear()
  await click('More for Topic')
  await click('Move under…', '[role="menuitem"]')
  const menuText = host.querySelector('[role="group"][aria-label="Make Topic a sub-chat under"]')?.textContent || ''
  expect(menuText).toContain('Parent')
  expect(menuText).toContain('Agent')
  expect(menuText).not.toContain('Topic')
  await click('Parent', '[role="menuitem"]')
  expect(childForkParent).toHaveBeenLastCalledWith('crew:chat:parent')
  expect(host.querySelector('[role="status"]')).toBeNull()
 } finally { f.extra = []; window.localStorage.clear(); await act(async () => root.unmount()) }
})
