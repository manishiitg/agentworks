import { useEffect, useRef, useState } from 'react'
import { MoreHorizontal, Pencil } from 'lucide-react'
import api, { getAuthToken, getDesktopApiBaseUrl } from '../../services/api'
import { desktopRequest } from '../../services/desktopConnections'
import { useChatStore } from '../../stores/useChatStore'
import { hasDesktopConnections } from '../../stores/useConnectedWorkStore'
import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'
import { fillAgentPurposeDraft } from '../../utils/agentPurposeDraft'
import { hydrateTabEvents } from '../../utils/sessionRestore'
import { activateTab } from '../../utils/activateTab'
import { registerRemoteSession } from '../../utils/remoteSessions'
import { Switch } from '../../components/ui/Switch'
import type { CrewAsk } from './YourComputerPanel'

type SharedAgent = { tab: string; name?: string; purpose?: string; audience: 'team' | ''; address?: string; can_change?: boolean; agentbridge_public?: boolean }
type AgentRow = { tab: string; name: string; purpose: string }

// Reads one JSON reply from the Crew's network through the Mac's saved sign-in.
async function viaConnection<T>(connection: string, path: string): Promise<T> {
  const response = await fetch(`${getDesktopApiBaseUrl()}/api/connections/${encodeURIComponent(connection)}${path}`, { headers: { Authorization: `Bearer ${getAuthToken() || ''}` } })
  if (!response.ok) throw new Error(String(response.status))
  return response.json() as Promise<T>
}

// The chat tab of one of this Crew's agents, to put the purpose request in its chat box.
function agentChatTabId(projectId: string, tab: string) {
  const tabs = Object.values(useChatStore.getState().chatTabs).filter(item => item.metadata?.agentProfileProjectId === projectId && !item.metadata?.isViewOnly)
  const found = tab === 'main' ? tabs.find(item => item.metadata?.agentProfileIsMainChat) : tabs.find(item => item.metadata?.agentProfileConversationKey === `${projectId}:chat:${tab}`)
  return found?.tabId
}

/** Your agents in the Team (owner, 5 Nov, "A Team is a shared room"): each agent is In team or Only me. Joining needs
 * the agent's purpose, set from its own chat. Saved as you go. */
export function MacSharedAgents({ projectId, connection }: { projectId: string; connection: string }) {
  const [rows, setRows] = useState<AgentRow[] | null>(null)
  // Where each agent answers: a published agent is on the server (the server decides; absent means this Mac).
  const [placements, setPlacements] = useState<Record<string, { location: 'publishing' | 'server' }>>({})
  const [shared, setShared] = useState<SharedAgent[]>([])
  const [supported, setSupported] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [openPurpose, setOpenPurpose] = useState('')
  // "All agents in team" (owner, 5 Nov): kept on this Mac; while on, every agent with a purpose is in the team.
  const [allInTeam, setAllInTeam] = useState(false)
  const endpoint = `/${encodeURIComponent(connection)}/crew-sync/${encodeURIComponent(projectId)}`
  useEffect(() => {
    let live = true
    void viaConnection<{ shared_agents?: boolean }>(connection, '/api/crew-sync-capabilities').then(caps => { if (live) setSupported(Boolean(caps.shared_agents)) }).catch(() => {})
    // A purpose set from a tab's chat shows here on the next read.
    const load = () => desktopRequest<{ tabs: AgentRow[]; agents: SharedAgent[]; placements?: Record<string, { location: 'publishing' | 'server' }>; all_in_team?: boolean }>(endpoint, 'POST', { action: 'agents' })
      .then(data => { if (!live) return; setRows(data.tabs || []); setPlacements(data.placements || {}); setAllInTeam(Boolean(data.all_in_team)); setShared(current => busyRef.current ? current : data.agents || []) })
      .catch(cause => { if (live) { setRows(current => current || []); setError(cause instanceof Error ? cause.message : "This Crew's agents could not be read.") } })
    void load()
    const timer = window.setInterval(() => { void load() }, 15000)
    window.addEventListener('focus', load)
    return () => { live = false; window.clearInterval(timer); window.removeEventListener('focus', load) }
  }, [connection, projectId, endpoint])
  const busyRef = useRef(false)
  busyRef.current = busy
  const cards = useRef<HTMLElement>(null)
  // Opened from an agent's tab: bring that agent's card into view once the cards are on screen.
  useEffect(() => {
    const pending = pendingAccess
    if (!rows || !pending || pending.projectId !== projectId) return
    const card = cards.current?.querySelector(`[data-shared-agent-card="${CSS.escape(pending.tab)}"]`)
    pendingAccess = null
    card?.scrollIntoView?.({ block: 'center', behavior: 'smooth' })
  }, [rows, supported, projectId])
  if (!supported || !rows) return null
  // Sharing needs the agent's purpose (the server refuses it otherwise).
  const readyOf = (tab: string) => Boolean(rows.find(item => item.tab === tab)?.purpose?.trim())
  const purposeOf = (tab: string) => rows.find(row => row.tab === tab)?.purpose?.trim() || ''
  // Saves the whole list; only agents with a purpose are sent (the server refuses others).
  const save = async (next: SharedAgent[]) => {
    setShared(next)
    const ready = next.filter(agent => readyOf(agent.tab))
      .map(({ tab, audience, can_change, agentbridge_public }) => ({ tab, audience, can_change: Boolean(can_change), agentbridge_public: Boolean(agentbridge_public) }))
    setBusy(true); setError('')
    try {
      const data = await desktopRequest<{ agents: SharedAgent[] }>(endpoint, 'POST', { action: 'share_agents', agents: ready })
      setShared(data?.agents || [])
    }
    catch (cause) { setError(cause instanceof Error ? cause.message : 'It was not saved. Try again.') }
    finally { setBusy(false) }
  }
  const update = (tab: string, change: Partial<SharedAgent> | null, persist = true) => {
    const current = shared.find(agent => agent.tab === tab)
    const base: SharedAgent = current || { tab, audience: 'team' }
    // Only me keeps an agent that is public on AgentBridge (Mac online's choice), with no audience.
    const off: SharedAgent[] = current?.agentbridge_public ? [{ ...current, audience: '' }] : []
    const next = change === null ? [...shared.filter(agent => agent.tab !== tab), ...off] : [...shared.filter(agent => agent.tab !== tab), { ...base, ...change }]
    if (persist) void save(next); else setShared(next)
  }
  const setPurpose = (tab: string) => { const id = agentChatTabId(projectId, tab); if (id) fillAgentPurposeDraft(id) }
  // A Team is a shared room (owner, 5 Nov): each agent is In team (everyone in the team sees it and its chat live) or
  // Only me. Agents public on AgentBridge are Mac online's choice.
  const choose = (tab: string, value: 'team' | 'none') => {
    if (value === 'none') {
      if (allInTeam) void desktopRequest(endpoint, 'POST', { action: 'team_all', all_in_team: false }).then(() => setAllInTeam(false)).catch(() => {})
      update(tab, null)
      return
    }
    update(tab, { audience: 'team' })
  }
  const AUDIENCES = [['team', 'In team'], ['none', 'Only me']] as const
  const chooseAll = async (on: boolean) => {
    setBusy(true); setError('')
    try {
      const data = await desktopRequest<{ all_in_team: boolean; note?: string }>(endpoint, 'POST', { action: 'team_all', all_in_team: on })
      setAllInTeam(Boolean(data.all_in_team))
      if (data.note) setError(data.note)
      const fresh = await desktopRequest<{ agents: SharedAgent[] }>(endpoint, 'POST', { action: 'agents' })
      setShared(fresh.agents || [])
    } catch (cause) { setError(cause instanceof Error ? cause.message : 'It was not saved. Try again.') }
    finally { setBusy(false) }
  }
  return <section ref={cards} aria-label="Your agents in the team">
    <div className="border-t border-border px-0.5 py-2.5 text-sm">
      <label className="flex items-center gap-2"><span className="font-medium">All agents in team</span><span className="text-xs text-muted-foreground">new agents join too, once they have a purpose</span><Switch aria-label="All agents in team" className="ml-auto" checked={allInTeam} disabled={busy} onCheckedChange={on => void chooseAll(on)} /></label>
      <p className="mt-1 text-xs text-muted-foreground">In team: everyone in this Crew's Team sees this agent and its chat live, and their agents can message it. Only me: just you.</p>
    </div>
    {rows.map(row => {
      const agent = shared.find(item => item.tab === row.tab)
      const purpose = purposeOf(row.tab)
      const place = placements[row.tab]?.location
      const choice = agent?.audience === 'team' ? 'team' : 'none'
      // An agent without a purpose cannot be shared: its row always says so (owner, 5 Nov).
      const blocked = readyOf(row.tab) ? '' : 'Give this agent a purpose before sharing it'
      return <div key={row.tab} data-shared-agent-card={row.tab} className="border-t border-border px-0.5 py-2.5 text-sm">
        <div className="flex flex-wrap items-center gap-2.5">
          <span title={purpose || undefined} className="min-w-0 truncate">{row.name}</span>
          {/* The row reads as a state, not only a toggle (owner, 5 Nov: "ui is not clear"). */}
          {choice === 'team' && <span className="shrink-0 text-xs text-muted-foreground">Shared with the team</span>}
          <span className={`shrink-0 rounded-full px-2 py-0.5 text-xs ${place === 'server' || place === 'publishing' ? 'bg-sky-500/15 text-sky-700 dark:text-sky-300' : 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-300'}`}>{place === 'server' ? 'Server' : place === 'publishing' ? 'Moving to the server' : 'Mac'}</span>
          <div role="radiogroup" aria-label={`${row.name} in the team`} className="ml-auto flex overflow-hidden rounded-full border border-border text-xs">
            {AUDIENCES.map(([value, label]) => <button key={value} type="button" role="radio" aria-checked={choice === value} disabled={busy || (value === 'team' && Boolean(blocked) && choice !== 'team')} title={value === 'team' && blocked ? blocked : undefined} onClick={() => { if (choice !== value) choose(row.tab, value) }}
              className={`px-2.5 py-0.5 disabled:cursor-not-allowed disabled:opacity-50 ${choice === value ? 'bg-primary font-medium text-primary-foreground' : 'text-muted-foreground hover:bg-muted'}`}>{label}</button>)}
          </div>
          <button type="button" aria-label={`More for ${row.name}`} aria-expanded={openPurpose === row.tab} onClick={() => setOpenPurpose(openPurpose === row.tab ? '' : row.tab)} className="grid h-6 w-6 shrink-0 place-items-center rounded-full text-muted-foreground hover:bg-muted hover:text-foreground"><MoreHorizontal aria-hidden="true" className="h-4 w-4" /></button>
        </div>
        {blocked && <p className="mt-1.5 flex flex-wrap items-center gap-2 pl-8 text-[12.5px] text-amber-600">{blocked}.<button type="button" onClick={() => setPurpose(row.tab)} className="text-primary hover:underline">Set it in its chat</button></p>}
        {openPurpose === row.tab && <div className="ml-8 mt-2 space-y-2 rounded-xl border border-border bg-popover p-3 text-xs">
          <p className="flex items-start gap-1.5 text-muted-foreground"><span className="min-w-0 whitespace-pre-wrap break-words">{purpose || 'No purpose yet.'}</span>
            <button type="button" aria-label={purpose ? `Change ${row.name}'s purpose` : `Set ${row.name}'s purpose`} title={purpose ? 'Change purpose' : 'Set purpose'} onClick={() => setPurpose(row.tab)} className="grid h-5 w-5 shrink-0 place-items-center rounded hover:bg-muted hover:text-foreground"><Pencil aria-hidden="true" className="h-3 w-3" /></button></p>
          {agent ? <>
            <label className="flex items-center gap-2"><Switch aria-label={`${row.name} can make changes for them`} checked={Boolean(agent.can_change)} disabled={busy} onCheckedChange={on => update(row.tab, { can_change: on })} />Can make changes for them</label>
            <p className="text-[11px] text-muted-foreground">{agent.can_change ? 'It can change files, run commands and use your apps and logins for them.' : 'It only answers their questions.'}</p>
          </> : <p className="text-muted-foreground">Only you can message it. Choose In team to share it with your Team.</p>}
        </div>}
      </div>
    })}
    {rows.length <= 1 && <p className="border-t border-border px-0.5 py-2.5 text-xs text-muted-foreground">Add an agent with + in the chat to share it.</p>}
    {error && <p className="text-xs text-amber-600">{error}</p>}
  </section>
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
          open: () => { void openTeamRemoteAgent(projectId, connection, { key: row.conversation_key, session: row.session_id!, name: row.name, owner }) } }
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
async function openTeamRemoteAgent(projectId: string, connection: string, agent: { key: string; session: string; name: string; owner: string }) {
  registerRemoteSession(agent.session, connection)
  const store = useChatStore.getState()
  const tabs = Object.values(store.chatTabs)
  const main = tabs.find(row => row.metadata?.agentProfileProjectId === projectId && row.metadata?.agentProfileIsMainChat && !row.metadata?.isViewOnly)
  if (!main) return
  const existing = tabs.find(row => row.sessionId === agent.session)
  const tabId = existing?.tabId || await store.createChatTab(agent.name, {
    ...main.metadata,
    agentProfileIsMainChat: false,
    agentProfileBuilder: false,
    agentProfileConversationKey: `${projectId}:team:${agent.key}`,
    agentProfileConversationId: undefined,
    agentProfileChatLocation: undefined,
    agentProfileTabCanWrite: false,
    agentProfileTabOwnerName: agent.owner,
    isViewOnly: true,
    viewOnlyWriter: agent.owner,
    readOnlyRestoredAt: Date.now(),
    userInteractiveContinuation: false,
  }, agent.session)
  store.setTabCanSteer(tabId, false)
  await hydrateTabEvents(agent.session, {})
  activateTab(tabId)
}
