import { useEffect, useState } from 'react'
import api, { getAuthToken, getDesktopApiBaseUrl } from '../../services/api'
import { desktopRequest } from '../../services/desktopConnections'
import { useChatStore } from '../../stores/useChatStore'
import { hasDesktopConnections } from '../../stores/useConnectedWorkStore'
import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'
import { hydrateTabEvents } from '../../utils/sessionRestore'
import { activateTab } from '../../utils/activateTab'
import { registerRemoteSession } from '../../utils/remoteSessions'
import type { CrewAsk } from './YourComputerPanel'

type SharedAgent = { tab: string; name?: string; purpose?: string; audience: 'team' | ''; address?: string; can_change?: boolean; agentbridge_public?: boolean }

// Reads one JSON reply from the Crew's network through the Mac's saved sign-in.
async function viaConnection<T>(connection: string, path: string): Promise<T> {
  const response = await fetch(`${getDesktopApiBaseUrl()}/api/connections/${encodeURIComponent(connection)}${path}`, { headers: { Authorization: `Bearer ${getAuthToken() || ''}` } })
  if (!response.ok) throw new Error(String(response.status))
  return response.json() as Promise<T>
}
/** For a Mac Crew's tab in the Mac app: "Shared with my team" while it is In team, else ''. Read from this Mac's own
 * shared list. */
export function useMacTabShareLine(projectId?: string, conversationKey?: string): string {
  const [line, setLine] = useState('')
  useEffect(() => {
    setLine('')
    // Main's key is the project itself; the shared list calls it "main".
    const tab = conversationKey === projectId ? 'main' : conversationKey?.split(':chat:')[1]
    if (!projectId || !tab || !hasDesktopConnections() || getRawActiveWorkspaceId() !== 'local') return
    let live = true
    void (async () => {
      const { data } = await api.get<{ binding?: { connection_id?: string } }>(`/api/crew-sync/${encodeURIComponent(projectId)}`, { skipSessionContext: true })
      const connection = data.binding?.connection_id
      if (!connection || !live) return
      const { agents } = await desktopRequest<{ agents: SharedAgent[] }>(`/${encodeURIComponent(connection)}/crew-sync/${encodeURIComponent(projectId)}`, 'POST', { action: 'agents' })
      const agent = agents?.find(item => item.tab === tab)
      if (live && agent?.audience === 'team') setLine('Shared with my team')
    })().catch(() => {})
    return () => { live = false }
  }, [projectId, conversationKey])
  return line
}

/** How one of a Mac Crew's agents is shared, for its tab and the agents menu. */
export type MacAgentShare = { line: string; canChange: boolean }

/** Which of a Mac Crew's agents are In team ("main" or a side tab's id), from this Mac's own shared list. Empty while
 * Reach is off. */
export function useMacSharedAgents(projectId?: string, enabled = true): Record<string, MacAgentShare> {
  const [shares, setShares] = useState<Record<string, MacAgentShare>>({})
  useEffect(() => {
    setShares({})
    if (!enabled || !projectId || !hasDesktopConnections() || getRawActiveWorkspaceId() !== 'local') return
    let live = true
    const load = async () => {
      const { data } = await api.get<{ binding?: { connection_id?: string } }>(`/api/crew-sync/${encodeURIComponent(projectId)}`, { skipSessionContext: true })
      const connection = data.binding?.connection_id
      if (!connection) { if (live) setShares({}); return }
      const { agents } = await desktopRequest<{ agents: SharedAgent[] }>(`/${encodeURIComponent(connection)}/crew-sync/${encodeURIComponent(projectId)}`, 'POST', { action: 'agents' })
      const next: Record<string, MacAgentShare> = {}
      for (const agent of agents || []) if (agent.audience === 'team') next[agent.tab] = { canChange: Boolean(agent.can_change), line: 'my team' }
      if (live) setShares(current => JSON.stringify(current) === JSON.stringify(next) ? current : next)
    }
    const reload = () => { void load().catch(() => {}) }
    reload()
    const timer = window.setInterval(reload, 15000)
    window.addEventListener('focus', reload)
    return () => { live = false; window.clearInterval(timer); window.removeEventListener('focus', reload) }
  }, [projectId, enabled])
  return shares
}

// Opening Access at one agent's card, from that agent's tab: the Crew menu opens Access, then the card scrolls into view.
export type CrewAccessSection = 'mac' | 'publish' | 'team'
let pendingAccess: { projectId: string; tab: string; section: CrewAccessSection; opened?: boolean } | null = null
export function hasPendingCrewAccess(projectId: string) { return pendingAccess?.projectId === projectId }
/** Which Access tab a pending open asks for (Team unless said otherwise). */
export function pendingCrewAccessSection(projectId: string): CrewAccessSection { return pendingAccess?.projectId === projectId ? pendingAccess.section : 'team' }
export function openCrewAccess(projectId: string, tab: string, section: CrewAccessSection = 'team') {
  pendingAccess = { projectId, tab, section }
  window.dispatchEvent(new CustomEvent('agentworks:open-crew-access', { detail: { projectId, section } }))
}
/** The side panel's Shared folder row: open Access on its Team tab, scrolled to the shared folder's Sync section. */
let pendingSync = ''
export function openCrewSync(projectId: string) {
  pendingSync = projectId
  openCrewAccess(projectId, '')
}
export function takeCrewSync(projectId: string) {
  if (pendingSync !== projectId) return false
  pendingSync = ''
  return true
}
/** True once for a pending request: the Crew menu opens Access for it, and Back is not overridden afterwards. */
export function takeCrewAccess(projectId?: string) {
  const pending = pendingAccess
  if (!pending || pending.projectId !== projectId || pending.opened) return false
  pending.opened = true
  return true
}

/** Opens another person's own thread with one of this Mac's shared agents, for the owner to read. */
export async function openCrewAskThread(projectId: string, ask: CrewAsk) {
  const store = useChatStore.getState()
  const tabs = Object.values(store.chatTabs)
  const main = tabs.find(row => row.metadata?.agentProfileProjectId === projectId && row.metadata?.agentProfileIsMainChat && !row.metadata?.isViewOnly)
  if (!main || !ask.session_id) return
  const existing = tabs.find(row => row.sessionId === ask.session_id && row.metadata?.isViewOnly)
  const tabId = existing?.tabId || await store.createChatTab(`${ask.sender}'s chat with ${ask.agent}`, {
    ...main.metadata,
    agentProfileIsMainChat: false,
    agentProfileBuilder: false,
    agentProfileConversationKey: `${projectId}:history:${ask.session_id}`,
    agentProfileConversationId: undefined,
    isViewOnly: true,
    viewOnlyWriter: ask.sender,
    readOnlyRestoredAt: Date.now(),
    userInteractiveContinuation: false,
  }, ask.session_id)
  store.setTabCanSteer(tabId, false)
  await hydrateTabEvents(ask.session_id, { workspacePath: main.metadata?.agentProfileWorkspace, fallbackToChatHistory: true, preferChatHistory: true })
  activateTab(tabId)
}

/** Where the people it is shared with reach a shared agent (owner, 3 Nov): one audience for all three ways. */

/** A Team member's agent in the owner's Mac app (owner, 5 Nov: "I need to see the other user's agent … in local"):
 * read from the Crew's online copy through this Mac's sign-in; it opens as a watch-only tab inside this Crew, its
 * chat read from the server (utils/remoteSessions.ts), where only its owner types. */
export type TeamRemoteAgent = { key: string; name: string; purpose?: string; owner: string; open: () => void }

export function useTeamRemoteAgents(projectId?: string, enabled = true): TeamRemoteAgent[] {
  const [agents, setAgents] = useState<TeamRemoteAgent[]>([])
  useEffect(() => {
    setAgents([])
    if (!enabled || !projectId || !hasDesktopConnections() || getRawActiveWorkspaceId() !== 'local') return
    let live = true
    const load = async () => {
      const { data } = await api.get<{ binding?: { connection_id?: string; peer_project_id?: string; side?: string } }>(`/api/crew-sync/${encodeURIComponent(projectId)}`, { skipSessionContext: true })
      const connection = data.binding?.connection_id, peer = data.binding?.peer_project_id
      if (!connection || !peer || data.binding?.side !== 'local') { if (live) setAgents([]); return }
      const rows = await viaConnection<{ conversation_key: string; session_id?: string; name: string; purpose?: string; tab_owner_id?: string; tab_owner_name?: string; can_write?: boolean }[]>(connection, `/api/agent-profiles/work/side-chats?project_id=${encodeURIComponent(peer)}`)
      const next = (rows || []).filter(row => row.can_write === false && row.tab_owner_id && row.session_id).map(row => {
        const owner = row.tab_owner_name || 'Teammate'
        return { key: row.conversation_key, name: row.name, purpose: row.purpose, owner,
          open: () => { void openTeamRemoteAgent(projectId, connection, { key: row.conversation_key, session: row.session_id!, name: row.name, owner, ownerId: row.tab_owner_id! }) } }
      })
      if (live) setAgents(current => JSON.stringify(current.map(a => [a.key, a.name, a.owner])) === JSON.stringify(next.map(a => [a.key, a.name, a.owner])) ? current : next)
    }
    const reload = () => { void load().catch(() => {}) }
    reload()
    const timer = window.setInterval(reload, 30000)
    window.addEventListener('focus', reload)
    return () => { live = false; window.clearInterval(timer); window.removeEventListener('focus', reload) }
  }, [projectId, enabled])
  return agents
}

/** Opens a Team member's agent as a watch-only tab in this Mac Crew; its chat streams from the server. */
async function openTeamRemoteAgent(projectId: string, connection: string, agent: { key: string; session: string; name: string; owner: string; ownerId: string }) {
  registerRemoteSession(agent.session, connection)
  const store = useChatStore.getState()
  const tabs = Object.values(store.chatTabs)
  const main = tabs.find(row => row.metadata?.agentProfileProjectId === projectId && row.metadata?.agentProfileIsMainChat && !row.metadata?.isViewOnly)
  if (!main) return
  // The tab is the member's, named by the server's row (owner, 5 Nov: it read "Watching demo's Main"): its owner's id
  // and name replace the Main tab's, also on a tab kept from an earlier open.
  const watch = {
    agentProfileIsMainChat: false,
    agentProfileBuilder: false,
    agentProfileConversationKey: `${projectId}:team:${agent.key}`,
    agentProfileConversationId: undefined,
    agentProfileChatLocation: undefined,
    agentProfileTabCanWrite: false,
    agentProfileTabOwnerID: agent.ownerId,
    agentProfileTabOwnerName: agent.owner,
    isViewOnly: true,
    viewOnlyWriter: agent.owner,
    userInteractiveContinuation: false,
  }
  const existing = tabs.find(row => row.sessionId === agent.session)
  let tabId = existing?.tabId
  if (tabId) store.setTabMetadata(tabId, watch)
  else tabId = await store.createChatTab(agent.name, { ...main.metadata, ...watch, readOnlyRestoredAt: Date.now() }, agent.session)
  store.setTabCanSteer(tabId, false)
  await hydrateTabEvents(agent.session, {})
  activateTab(tabId)
}
