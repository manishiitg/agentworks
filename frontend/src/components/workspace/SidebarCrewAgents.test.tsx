// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { SidebarCrewAgents } from './SidebarCrewAgents'
const f = vi.hoisted(() => ({ select: vi.fn(), open: vi.fn(), markUnread: vi.fn(), copy: vi.fn(), fork: vi.fn(), inFeature: '', billing: { id: 'feat-1', name: 'Billing', state: 'active', agents: [], open_tasks: 2, done_tasks: 0 } }))
vi.mock('../chat/AgentCard', async importOriginal => ({
 ...await importOriginal<typeof import('../chat/AgentCard')>(),
 useCrewAgentsStore: () => ({ projectId: 'crew', agents: [
  { tabId: 'b', name: 'Second', actions: { forkTo: f.copy, fork: f.fork }, info: { name: 'Second', onRemove: vi.fn() } }, { tabId: 'a', name: 'First' }, { tabId: 'h', name: 'Third' },
 ], closed: [{ key: 'closed', name: 'Closed fixture', open: f.open }] }),
 AgentHoverCard: ({ children }: { children: React.ReactNode }) => <span data-details-card>{children}</span>,
}))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: (select: (s: unknown) => unknown) => select({ activeTabId: 'a', chatTabs: {}, activeSessionsCache: [], markTabUnread: f.markUnread }) }))
vi.mock('../../stores/useLLMStore', () => ({ useLLMStore: (select: (s: unknown) => unknown) => select({ providerManifest: [] }) }))
vi.mock('../../products/work/teamApi', () => ({ useTeamTab: () => ({}), teamAgentFor: () => undefined }))
vi.mock('../../products/work/featuresApi', () => ({ useCrewFeatures: () => ({ features: [f.billing], forTab: (tab: string) => tab === f.inFeature ? [f.billing] : [] }) }))
vi.mock('../../products/work/OwnTerminalAgents', () => ({ OwnTerminalAgents: () => null }))
vi.mock('../chat/agentMenuState', () => ({ useAgentMenuState: () => null }))
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

// #3210: an agent in a feature moves out of the normal list, under its feature.
it('lists agents in no feature first, then each feature with its agents', async () => {
 const host = document.createElement('div'), root = createRoot(host)
 f.inFeature = 'b'
 try {
  await act(async () => root.render(<SidebarCrewAgents projectId="crew" onSelect={f.select}/>))
  expect([...host.querySelectorAll('[role="listitem"]')].map(row => row.textContent)).toEqual(['First', 'Third', 'Second'])
  expect(host.querySelector('[aria-label="Feature Billing"] [role="listitem"]')?.textContent).toBe('Second')
 } finally { f.inFeature = ''; await act(async () => root.unmount()) }
})
