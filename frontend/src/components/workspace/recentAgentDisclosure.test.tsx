// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { RecentAgentChats } from './RecentAgentChats'
import { SidebarAgentPresence, SidebarAgentVisibilityProvider, sidebarAgentIdentity } from './SidebarAgentVisibility'
import { useProjectMenuHistoryStore, connectedWorkHistoryScope } from '../../stores/useProjectMenuHistoryStore'
import { useSidebarExpansionStore } from '../../stores/useSidebarExpansionStore'

vi.mock('../../stores/useChatStore', () => ({ useChatStore: (select: (state: unknown) => unknown) => select({ activeTabId: null, chatTabs: { analyst: { tabId: 'analyst', name: 'Analyst', metadata: { agentProfileId: 'work', agentProfileProjectId: 'example', agentProfileProjectTitle: 'Example', agentProfileConversationKey: 'example:chat:analyst', agentProfileTabOwnerID: 'owner' } } } }) }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: (state: unknown) => unknown) => select({ user: { id: 'owner' } }) }))
vi.mock('../../components/chat/agentMenuState', () => ({ useAgentMenuState: () => 'idle' }))
vi.mock('../../utils/openAgentTab', () => ({ openAgentTab: vi.fn() }))
vi.mock('../../utils/desktopConnection', () => ({ getRawActiveWorkspaceId: () => 'local' }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// One visible personal agent, not a second Recents shortcut; collapsed Recents is a saved choice.
it('deduplicates mounted Crew rows and remembers a collapsed recent chats section', async () => {
  const previous = localStorage.getItem('auth-storage')
  localStorage.setItem('auth-storage', JSON.stringify({ state: { user: { id: 'owner' } } }))
  useProjectMenuHistoryStore.getState().visitAgent(connectedWorkHistoryScope(), 'analyst')
  useSidebarExpansionStore.setState({ expanded: {} })
  const host = document.createElement('div'), root = createRoot(host)
  const tree = (expanded: boolean) => <SidebarAgentVisibilityProvider>{expanded && <SidebarAgentPresence identity={sidebarAgentIdentity('local', 'example:chat:analyst')}><span>Expanded agent row</span></SidebarAgentPresence>}<RecentAgentChats /></SidebarAgentVisibilityProvider>
  const recent = () => host.querySelector('button[title="Analyst · Example"]')
  const disclosure = () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Recent chats')!
  try {
    await act(async () => root.render(tree(true)))
    expect(recent()).toBeNull()
    await act(async () => root.render(tree(false)))
    expect(recent()).not.toBeNull()
    await act(async () => disclosure().click())
    expect(disclosure().getAttribute('aria-expanded')).toBe('false')
    expect(recent()).toBeNull()
    await act(async () => root.render(tree(true)))
    await act(async () => root.render(tree(false)))
    expect(disclosure().getAttribute('aria-expanded')).toBe('false')
  } finally { await act(async () => root.unmount()); if (previous === null) localStorage.removeItem('auth-storage'); else localStorage.setItem('auth-storage', previous) }
})
