// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { SidebarAgentLayout, SidebarCrewAgents } from './SidebarCrewAgents'
const f = vi.hoisted(() => ({ select: vi.fn(), open: vi.fn(), markUnread: vi.fn(), copy: vi.fn(), fork: vi.fn(), setSidebarHidden: vi.fn(), hidden: new Set<string>(), active: 'a', canHide: false, extra: [] as Record<string, unknown>[], groups: {} as Record<string, string>, edit: vi.fn(async (..._args: unknown[]) => {}) }))
vi.mock('../chat/AgentCard', async importOriginal => ({
 ...await importOriginal<typeof import('../chat/AgentCard')>(),
 useCrewAgentsStore: () => ({ projectId: 'crew', agents: [
  { tabId: 'b', name: 'Second', sidebarOrder: 0, sidebarHidden: f.hidden.has('b'), actions: { forkTo: f.copy, fork: f.fork, ...(f.canHide ? { setSidebarHidden: f.setSidebarHidden } : {}) }, info: { name: 'Second', onRemove: vi.fn() } },
  { tabId: 'a', name: 'First', sidebarOrder: 1 }, { tabId: 'h', name: 'Third', sidebarOrder: 3, sidebarHidden: f.hidden.has('h') }, ...f.extra,
 ], closed: [{ key: 'closed', name: 'Closed fixture', sidebarOrder: 2, sidebarHidden: f.hidden.has('closed'), open: f.open }] }),
 AgentHoverCard: ({ children }: { children: React.ReactNode }) => <span data-details-card>{children}</span>,
}))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: (select: (s: unknown) => unknown) => select({ activeTabId: f.active, chatTabs: {b:{sessionId:'second-session'}}, activeSessionsCache: f.hidden.size ? [{session_id:'second-session',last_activity:new Date(Date.now()-41*60_000).toISOString()}] : [], markTabUnread: f.markUnread }) }))
vi.mock('../../stores/useLLMStore', () => ({ useLLMStore: (select: (s: unknown) => unknown) => select({ providerManifest: [] }) }))
vi.mock('../../products/work/teamApi', () => ({ useTeamTab: () => ({}), teamAgentFor: () => undefined, teammateAgentsListed: () => false }))
vi.mock('../../products/work/OwnTerminalAgents', () => ({ OwnTerminalAgents: () => null }))
vi.mock('../chat/agentMenuState', () => ({ useAgentMenuState: () => null }))
vi.mock('./agentGroups', async importOriginal => ({ ...await importOriginal<typeof import('./agentGroups')>(), useAgentGroups: () => f.groups, useAgentGroupsStore: { getState: () => ({ edit: (_project: string, change: unknown) => f.edit(change) }) } }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

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

it('marks an agent unread from its actions menu without opening the chat', async () => {
 const host = document.createElement('div'), root = createRoot(host)
 f.select.mockClear()
 try {
  await act(async () => root.render(<SidebarCrewAgents projectId="crew" onSelect={f.select}/>))
  expect(host.querySelector('[title="Mark as unread"]')).toBeNull()
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="More for Second"]')!.click())
  expect([...host.querySelectorAll('[role="menuitem"]')].map(item => item.textContent)).toEqual(['Mark as unread', 'Fork chat'])
  const unread = host.querySelector<HTMLButtonElement>('[role="menuitem"]')!
  expect(unread.textContent).toBe('Mark as unread')
  await act(async () => unread.click())
  expect(f.markUnread).toHaveBeenCalledWith('b')
  expect(f.select).not.toHaveBeenCalled()
  expect(host.querySelector('[role="menu"]')).toBeNull()
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
 const hiddenRows = () => [...host.querySelectorAll('[aria-label="Hidden agents"] [role="listitem"]')].map(row => row.textContent)
 f.setSidebarHidden.mockImplementation((hidden: boolean) => { if (hidden) f.hidden.add('b'); else f.hidden.delete('b') })
 try {
  await act(async () => render())
  await click('More for Second')
  await click('Hide from sidebar')
  expect(select).not.toHaveBeenCalled()
  expect([...host.querySelectorAll('[role="listitem"]')].map(row => row.textContent)).toEqual(['First'])
  expect(hiddenRows()).toEqual([])
  await click('Hidden agents · 3')
  expect(hiddenRows()).toEqual(['Second41m','Closed fixture','Third'])
  expect(host.querySelector('[aria-current="page"]')?.textContent).toBe('Second41m')
  await click('Third')
  expect(select).toHaveBeenCalledWith('h')
  expect(hiddenRows()).toEqual(['Second41m','Closed fixture','Third'])
  await click('More for Second')
  await click('Show in sidebar')
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
it('shows groups with their agents, forks under their parent and agents a teammate stopped sharing', async () => {
 const host = document.createElement('div'), root = createRoot(host)
 f.hidden = new Set(); f.active = 'a'; f.canHide = false
 f.extra = [
  { tabId: 'p', key: 'crew:chat:parent', name: 'Parent', sidebarOrder: 4 },
  { tabId: 'c', key: 'crew:chat:fork', forkedFrom: 'crew:chat:parent', name: 'Parent (fork)', sidebarOrder: 5 },
  { tabId: 't', key: 'crew:chat:theirs', name: 'Their agent', ownerId: 'teammate', owner: 'Ana' },
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
  expect(names('[aria-label="Agents"] > div:not([aria-label^="Group"])')).toEqual(['Second', 'First', 'Third'])
  // #3389: the note flashed while the lists loaded; it shows only once the agent stays missing.
  expect(host.textContent).not.toContain('no longer shared with you')
  await act(async () => { vi.advanceTimersByTime(3000) })
  vi.useRealTimers()
  expect(host.textContent).toContain('no longer shared with you')
  await click('Remove from group')
  expect(f.edit).toHaveBeenLastCalledWith({ op: 'move', key: 'crew:chat:gone', group: '' })
  await click('Hide forks of Parent')
  expect(names('[aria-label="Group Design"]')).toEqual(['Parent', 'ATheir agent'])
  expect(host.textContent).toContain('1 fork')
  await click('More for Parent')
  expect([...host.querySelectorAll('[role="menuitem"]')].map(item => item.textContent)).toEqual(expect.arrayContaining(['New group…', 'Take out of Design']))
  await click('Take out of Design')
  expect(f.edit).toHaveBeenLastCalledWith({ op: 'move', key: 'crew:chat:parent', group: '' })
  // The teammate picker had no way out but a Close button below a long list: a click outside closes it.
  await click('More for group Design')
  await click('Add a teammate’s agent…')
  expect(host.querySelector('[role="dialog"]')).toBeTruthy()
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
  await act(async () => root.render(<SidebarAgentLayout<R> projectId="crew" rows={rows} known={new Set(rows.map(r => r.key))} keyOf={r => r.key} forkedFromOf={r => r.from} teammate={r => !!r.theirs} render={r => <span role="listitem">{r.name}</span>} />))
  await act(async () => { vi.advanceTimersByTime(3000) })
  vi.useRealTimers()
  expect([...host.querySelectorAll('[aria-label="Group Design"] [role="listitem"]')].map(row => row.textContent)).toEqual(['Parent', 'Parent (fork)', 'Their agent'])
  expect(host.textContent).toContain('Solo')
  // A group whose only agent was unshared still shows, with the same note as the open project.
  expect(host.querySelector('[aria-label="Group Empty"]')?.textContent).toContain('no longer shared')
  expect(host.textContent).not.toContain('Ungrouped teammate')
  // On the website teammates' agents are not listed at all: their grouped agent is not "no longer shared".
  expect(host.querySelector('[aria-label="Group Mates"]')?.textContent).not.toContain('no longer shared')
 } finally { vi.useRealTimers(); f.groups = {}; window.localStorage.clear(); await act(async () => root.unmount()) }
})

