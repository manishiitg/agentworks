// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { SidebarCrewAgents } from './SidebarCrewAgents'
const f = vi.hoisted(() => ({ select: vi.fn(), open: vi.fn() }))
vi.mock('../chat/AgentCard', () => ({
 useCrewAgentsStore: () => ({ projectId: 'crew', agents: [
  { tabId: 'b', name: 'Second', pinned: true }, { tabId: 'a', name: 'First', pinned: true }, { tabId: 'h', name: 'Hidden', pinned: false },
 ], closed: [{ key: 'closed', name: 'Closed fixture', open: f.open }] }),
 AgentHoverCard: ({ children }: { children: React.ReactNode }) => <>{children}</>, AgentActionsMenu: () => null,
}))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: (select: (s: unknown) => unknown) => select({ activeTabId: 'a', chatTabs: {}, activeSessionsCache: [] }) }))
vi.mock('../../stores/useLLMStore', () => ({ useLLMStore: (select: (s: unknown) => unknown) => select({ providerManifest: [] }) }))
vi.mock('../../products/work/teamApi', () => ({ useTeamTab: () => ({}), teamAgentFor: () => undefined }))
vi.mock('../../products/work/OwnTerminalAgents', () => ({ OwnTerminalAgents: () => null }))
vi.mock('../chat/agentMenuState', () => ({ useAgentMenuState: () => null }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// #2952: flattening preserves the existing order, selected agent and hidden/closed access.
it('lists own agents directly in their normal order without group controls', async () => {
 const host = document.createElement('div'), root = createRoot(host)
 try {
  await act(async () => root.render(<SidebarCrewAgents projectId="crew" onSelect={f.select}/>))
  expect([...host.querySelectorAll('[role="listitem"]')].map(row => row.textContent)).toEqual(['Second', 'First'])
  expect(host.querySelector('[aria-current="page"]')?.textContent).toBe('First')
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-current="page"]')!.click())
  expect(f.select).toHaveBeenCalledWith('a')
  expect(host.querySelector('select, form')).toBeNull()
  // Agents hidden on purpose ("Hide from list") stay reachable from the end of the list.
  expect(host.textContent).not.toContain('Hidden')
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="1 hidden agent"]')!.click())
  expect(host.textContent).toContain('Hidden')
  await act(async () => [...host.querySelectorAll('button')].find(row => row.textContent?.includes('Closed agents'))!.click())
  await act(async () => [...host.querySelectorAll('button')].find(row => row.textContent==='Closed fixture')!.click())
  expect(f.open).toHaveBeenCalledOnce()
 } finally { await act(async () => root.unmount()) }
})
