import { useEffect, useRef, useState } from 'react'
import { Globe, Hash, MessageSquare, MoreHorizontal, Pencil, Smartphone, Terminal, type LucideIcon } from 'lucide-react'
import api, { getAuthToken, getDesktopApiBaseUrl } from '../../services/api'
import { desktopRequest } from '../../services/desktopConnections'
import { useChatStore } from '../../stores/useChatStore'
import { hasDesktopConnections } from '../../stores/useConnectedWorkStore'
import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'
import { fillAgentPurposeDraft } from '../../utils/agentPurposeDraft'
import { hydrateTabEvents } from '../../utils/sessionRestore'
import { activateTab } from '../../utils/activateTab'
import { Switch } from '../../components/ui/Switch'
import { useProjectTeam } from './teamApi'
import type { CrewAsk } from './YourComputerPanel'

type SharedAgent = { tab: string; name?: string; purpose?: string; audience: 'team' | 'everyone' | 'chosen' | ''; people?: string[]; address?: string; can_change?: boolean; agentbridge_public?: boolean }
type AgentRow = { tab: string; name: string; purpose: string; icon?: string }
type Person = { id: string; username: string; email?: string }

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

/** Who can message your agents (owner, 3 Nov mockup mac-share-card): one card per agent with three plain questions:
 * is it shared (switch), who (chosen people from a list, or everyone), how they reach it (themselves always; their AI
 * agents by a switch that shows the address). The purpose is the tab's own, set from its chat. Saved as you go. */
export function MacSharedAgents({ projectId, connection, network }: { projectId: string; connection: string; network: string }) {
  const [rows, setRows] = useState<AgentRow[] | null>(null)
  // Where each agent answers: a published agent is on the server (the server decides; absent means this Mac).
  const [placements, setPlacements] = useState<Record<string, { location: 'publishing' | 'server' }>>({})
  const [shared, setShared] = useState<SharedAgent[]>([])
  const [people, setPeople] = useState<Person[]>([])
  const { team } = useProjectTeam(projectId)
  const [supported, setSupported] = useState(false)
  const [adding, setAdding] = useState('')
  const [search, setSearch] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState('')
  const [openPurpose, setOpenPurpose] = useState('')
  const endpoint = `/${encodeURIComponent(connection)}/crew-sync/${encodeURIComponent(projectId)}`
  useEffect(() => {
    let live = true
    void viaConnection<{ shared_agents?: boolean }>(connection, '/api/crew-sync-capabilities').then(caps => { if (live) setSupported(Boolean(caps.shared_agents)) }).catch(() => {})
    void viaConnection<{ users: Person[] }>(connection, '/api/users/directory').then(data => { if (live) setPeople(data.users || []) }).catch(() => {})
    // A purpose set from a tab's chat shows here on the next read.
    const load = () => desktopRequest<{ tabs: AgentRow[]; agents: SharedAgent[]; placements?: Record<string, { location: 'publishing' | 'server' }> }>(endpoint, 'POST', { action: 'agents' })
      .then(data => { if (!live) return; setRows(data.tabs || []); setPlacements(data.placements || {}); setShared(current => busyRef.current ? current : data.agents || []) })
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
  const purposeOf = (tab: string) => rows.find(row => row.tab === tab)?.purpose?.trim() || ''
  // Saves the whole list; an agent waits to be saved until its tab has a purpose and, for chosen people, someone.
  const save = async (next: SharedAgent[]) => {
    setShared(next)
    const ready = next.filter(agent => purposeOf(agent.tab) && (agent.audience !== 'chosen' || agent.people?.length))
      .map(({ tab, audience, people, can_change, agentbridge_public }) => ({ tab, audience, people, can_change: Boolean(can_change), agentbridge_public: Boolean(agentbridge_public) }))
    setBusy(true); setError('')
    try {
      const data = await desktopRequest<{ agents: SharedAgent[] }>(endpoint, 'POST', { action: 'share_agents', agents: ready })
      // Saved agents come back with their address; ones still waiting for a purpose or a person stay on screen.
      setShared([...(data?.agents || []), ...next.filter(agent => !ready.some(item => item.tab === agent.tab))])
    }
    catch (cause) { setError(cause instanceof Error ? cause.message : 'It was not saved. Try again.') }
    finally { setBusy(false) }
  }
  const update = (tab: string, change: Partial<SharedAgent> | null, persist = true) => {
    const current = shared.find(agent => agent.tab === tab)
    const base: SharedAgent = current || { tab, audience: 'team', people: [] }
    // Only me keeps an agent that is public on AgentBridge (Mac online's choice), with no people audience.
    const off: SharedAgent[] = current?.agentbridge_public ? [{ ...current, audience: '', people: [] }] : []
    const next = change === null ? [...shared.filter(agent => agent.tab !== tab), ...off] : [...shared.filter(agent => agent.tab !== tab), { ...base, ...change }]
    if (persist) void save(next); else setShared(next)
  }
  const setPurpose = (tab: string) => { const id = agentChatTabId(projectId, tab); if (id) fillAgentPurposeDraft(id) }
  const personName = (id: string) => people.find(person => person.id === id)?.username || id
  const choices = (agent: SharedAgent) => people.filter(person => team?.people.some(member => member.user_id === person.id) && !agent.people?.includes(person.id) && `${person.username} ${person.email || ''}`.toLowerCase().includes(search.trim().toLowerCase()))
  // One choice per agent, as the approved mockup has it: Only me turns sharing off; Team, Chosen and Everyone share it.
  const AUDIENCES = [['none', 'Only me'], ['team', 'Team'], ['chosen', 'Chosen'], ['everyone', 'Everyone']] as const
  return <section ref={cards} aria-label="Who can message your agents">
    {rows.map(row => {
      const agent = shared.find(item => item.tab === row.tab)
      const purpose = purposeOf(row.tab)
      const place = placements[row.tab]?.location
      const choice = agent?.audience || 'none'
      return <div key={row.tab} data-shared-agent-card={row.tab} className="border-t border-border px-0.5 py-2.5 text-sm">
        <div className="flex flex-wrap items-center gap-2.5">
          <span aria-hidden="true" className="grid h-[22px] w-[22px] shrink-0 place-items-center rounded-md bg-muted text-xs">{row.icon?.trim() || row.name.slice(0, 1).toUpperCase()}</span>
          <span title={purpose || undefined} className="min-w-0 truncate">{row.name}</span>
          <span className={`shrink-0 rounded-full px-2 py-0.5 text-xs ${place === 'server' || place === 'publishing' ? 'bg-sky-500/15 text-sky-700 dark:text-sky-300' : 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-300'}`}>{place === 'server' ? 'Server' : place === 'publishing' ? 'Moving to the server' : 'Mac'}</span>
          <div role="radiogroup" aria-label={`Who can message ${row.name}`} className="ml-auto flex overflow-hidden rounded-full border border-border text-xs">
            {AUDIENCES.map(([value, label]) => <button key={value} type="button" role="radio" aria-checked={choice === value} disabled={busy} onClick={() => { if (choice !== value) update(row.tab, value === 'none' ? null : { audience: value }) }}
              className={`px-2.5 py-0.5 ${choice === value ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:bg-muted'}`}>{label}</button>)}
          </div>
          <button type="button" aria-label={`More for ${row.name}`} aria-expanded={openPurpose === row.tab} onClick={() => setOpenPurpose(openPurpose === row.tab ? '' : row.tab)} className="grid h-6 w-6 shrink-0 place-items-center rounded-full text-muted-foreground hover:bg-muted hover:text-foreground"><MoreHorizontal aria-hidden="true" className="h-4 w-4" /></button>
        </div>
        {agent && !purpose && <p className="mt-1.5 flex items-center gap-2 pl-8 text-[12.5px] text-amber-600">Set this agent's purpose to share it.<button type="button" onClick={() => setPurpose(row.tab)} className="text-primary hover:underline">Set purpose</button></p>}
        {agent?.audience === 'chosen' && <div className="mt-1.5 flex flex-wrap items-center gap-1.5 pl-8">
          {(agent.people || []).map(id => <span key={id} className="inline-flex items-center gap-1 rounded-full border border-border px-2.5 py-0.5 text-xs">{personName(id)}<button type="button" aria-label={`Remove ${personName(id)}`} onClick={() => update(row.tab, { people: (agent.people || []).filter(person => person !== id) })} className="text-muted-foreground hover:text-foreground">×</button></span>)}
          <button type="button" aria-expanded={adding === row.tab} onClick={() => { setAdding(adding === row.tab ? '' : row.tab); setSearch('') }} className="rounded-full border border-dashed border-border px-2.5 py-0.5 text-xs text-primary hover:bg-muted">+ Person</button>
        </div>}
        {agent?.audience === 'chosen' && adding === row.tab && <div className="ml-8 mt-1.5 w-64 max-w-[calc(100%-2rem)] overflow-hidden rounded-2xl border border-border bg-popover shadow">
          <input autoFocus aria-label="Search people" value={search} onChange={event => setSearch(event.target.value)} onKeyDown={event => { if (event.key === 'Escape') { setAdding(''); setSearch('') } }} placeholder="Search people" className="w-full border-b border-border bg-transparent px-3 py-1.5 text-xs outline-none" />
          <div className="max-h-56 overflow-y-auto">
            {choices(agent).map(person => <button key={person.id} type="button" onClick={() => { update(row.tab, { people: [...(agent.people || []), person.id] }); setSearch('') }} className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs hover:bg-muted">
              <span className="truncate">{person.username}</span>{person.email && person.email !== person.username && <span className="ml-auto truncate text-muted-foreground">{person.email}</span>}
            </button>)}
            {!choices(agent).length && <p className="px-3 py-1.5 text-xs text-muted-foreground">{team?.people.length ? 'Nobody found' : 'Add people to the Team above'}</p>}
          </div>
        </div>}
        {openPurpose === row.tab && <div className="ml-8 mt-2 space-y-2 rounded-xl border border-border bg-popover p-3 text-xs">
          <p className="flex items-start gap-1.5 text-muted-foreground"><span className="min-w-0 whitespace-pre-wrap break-words">{purpose || 'No purpose yet.'}</span>
            <button type="button" aria-label={purpose ? `Change ${row.name}'s purpose` : `Set ${row.name}'s purpose`} title={purpose ? 'Change purpose' : 'Set purpose'} onClick={() => setPurpose(row.tab)} className="grid h-5 w-5 shrink-0 place-items-center rounded hover:bg-muted hover:text-foreground"><Pencil aria-hidden="true" className="h-3 w-3" /></button></p>
          {agent ? <>
            <label className="flex items-center gap-2"><Switch aria-label={`${row.name} can make changes for them`} checked={Boolean(agent.can_change)} disabled={busy} onCheckedChange={on => update(row.tab, { can_change: on })} />Can make changes for them</label>
            <p className="text-[11px] text-muted-foreground">{agent.can_change ? 'It can change files, run commands and use your apps and logins for them.' : 'It only answers their questions.'}</p>
            <p className="flex flex-wrap items-center gap-1.5 text-[11px] text-muted-foreground">Use it from
              {([[Globe, 'Website'], [Smartphone, 'Phone'], [MessageSquare, 'ChatGPT or Claude'], [Hash, 'Slack']] as [LucideIcon, string][]).map(([Icon, label]) => <Icon key={label} role="img" aria-label={label} className="h-3.5 w-3.5"><title>{label}</title></Icon>)}
              their apps ·<Terminal role="img" aria-label="Claude Code, Codex and other agents" className="h-3.5 w-3.5"><title>Claude Code, Codex and other agents</title></Terminal>their AI agents{agent.audience === 'everyone' && network ? ` · everyone on ${network}` : ''}</p>
            {agent.address && <p className="flex items-center gap-2"><span className="min-w-0 truncate rounded-full bg-muted px-2.5 py-0.5 font-mono">agent:{agent.address}</span>
              <button type="button" className="shrink-0 rounded-full border border-border px-2 py-0.5 text-[11px] hover:bg-muted" onClick={() => void navigator.clipboard?.writeText(`agent:${agent.address}`).then(() => { setCopied(row.tab); window.setTimeout(() => setCopied(''), 1500) })}>{copied === row.tab ? 'Copied' : 'Copy'}</button></p>}
          </> : <p className="text-muted-foreground">Only you can message it. Choose Team, Chosen or Everyone to share it.</p>}
        </div>}
      </div>
    })}
    {rows.length <= 1 && <p className="border-t border-border px-0.5 py-2.5 text-xs text-muted-foreground">Add an agent with + in the chat to share it.</p>}
    {error && <p className="text-xs text-amber-600">{error}</p>}
  </section>
}

/** For a Mac Crew's tab in the Mac app: who it is shared with, in a few words ("Shared with user2"), or '' when it is
 * not shared. Read from this Mac's own shared list and its network's people. */
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
      if (!agent || !live) return
      if (agent.audience === 'everyone') { setLine('Shared with everyone'); return }
      if (agent.audience === 'team') { setLine('Shared with my team'); return }
      const people = await viaConnection<{ users: Person[] }>(connection, '/api/users/directory').then(result => result.users || []).catch(() => [] as Person[])
      const names = (agent.people || []).map(id => people.find(person => person.id === id)?.username || 'someone')
      if (live) setLine(`Shared with ${names.length > 2 ? `${names.slice(0, 2).join(', ')} and ${names.length - 2} more` : names.join(' and ')}`)
    })().catch(() => {})
    return () => { live = false }
  }, [projectId, conversationKey])
  return line
}

/** How one of a Mac Crew's agents is shared, for its tab and the agents menu. */
export type MacAgentShare = { line: string; canChange: boolean }

/** Which of a Mac Crew's agents others can message ("main" or a side tab's id) and with whom ("tester-b", "everyone in
 * the company"), from this Mac's own shared list and its network's people. Empty while Reach is off. */
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
      const chosen = (agents || []).some(agent => agent.audience !== 'everyone')
      const people = chosen ? await viaConnection<{ users: Person[] }>(connection, '/api/users/directory').then(result => result.users || []).catch(() => [] as Person[]) : []
      const next: Record<string, MacAgentShare> = {}
      for (const agent of agents || []) {
        const names = (agent.people || []).map(id => people.find(person => person.id === id)?.username || 'someone')
        next[agent.tab] = { canChange: Boolean(agent.can_change), line: agent.audience === 'everyone' ? 'everyone in the company' : agent.audience === 'team' ? 'my team' : names.length > 2 ? `${names.slice(0, 2).join(', ')} and ${names.length - 2} more` : names.join(' and ') }
      }
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
let pendingAccess: { projectId: string; tab: string; opened?: boolean } | null = null
export function hasPendingCrewAccess(projectId: string) { return pendingAccess?.projectId === projectId }
export function openCrewAccess(projectId: string, tab: string) {
  pendingAccess = { projectId, tab }
  window.dispatchEvent(new CustomEvent('agentworks:open-crew-access', { detail: { projectId } }))
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
