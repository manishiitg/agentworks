import { useEffect, useRef, useState } from 'react'
import { Globe, Hash, Info, MessageSquare, Pencil, Smartphone, Terminal, type LucideIcon } from 'lucide-react'
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

type SharedAgent = { tab: string; name?: string; purpose?: string; audience: 'team' | 'everyone' | 'chosen'; people?: string[]; address?: string; can_change?: boolean }
type AgentRow = { tab: string; name: string; purpose: string }
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
    const load = () => desktopRequest<{ tabs: AgentRow[]; agents: SharedAgent[] }>(endpoint, 'POST', { action: 'agents' })
      .then(data => { if (!live) return; setRows(data.tabs || []); setShared(current => busyRef.current ? current : data.agents || []) })
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
      .map(({ tab, audience, people, can_change }) => ({ tab, audience, people, can_change: Boolean(can_change) }))
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
    const next = change === null ? shared.filter(agent => agent.tab !== tab) : [...shared.filter(agent => agent.tab !== tab), { ...base, ...change }]
    if (persist) void save(next); else setShared(next)
  }
  const setPurpose = (tab: string) => { const id = agentChatTabId(projectId, tab); if (id) fillAgentPurposeDraft(id) }
  const personName = (id: string) => people.find(person => person.id === id)?.username || id
  const choices = (agent: SharedAgent) => people.filter(person => team?.people.some(member => member.user_id === person.id) && !agent.people?.includes(person.id) && `${person.username} ${person.email || ''}`.toLowerCase().includes(search.trim().toLowerCase()))
  return <section ref={cards} aria-label="Who can message your agents" className="mt-4 space-y-3">
    <p className="text-sm font-semibold">Who can message your agents</p>
    {rows.map(row => {
      const agent = shared.find(item => item.tab === row.tab)
      const purpose = purposeOf(row.tab)
      const saved = Boolean(agent && purpose && (agent.audience !== 'chosen' || agent.people?.length))
      return <div key={row.tab} data-shared-agent-card={row.tab} className={`rounded-xl border border-border px-4 py-3 ${agent ? 'space-y-2.5' : 'opacity-70'}`}>
        <div className="flex items-center gap-2">
          <MessageSquare aria-hidden="true" className="h-4 w-4 shrink-0" />
          <span className="min-w-0 truncate text-sm font-semibold">{row.name}</span>
          <span className="ml-auto shrink-0 text-xs text-muted-foreground">{!agent ? 'Only you' : saved ? 'Shared' : !purpose ? 'Set its purpose to share it' : 'Add people to share it'}</span>
          <Switch aria-label={`Others can message ${row.name}`} checked={Boolean(agent)} disabled={busy} onCheckedChange={on => update(row.tab, on ? {} : null)} />
        </div>
        {agent && <div className="ml-6 space-y-2.5">
          <div className="flex items-start gap-1.5 text-[13px] text-muted-foreground">
            {purpose ? <span title={purpose} className={`min-w-0 ${openPurpose === row.tab ? 'whitespace-pre-wrap break-words' : 'truncate'}`}>{purpose}</span> : <span className="min-w-0 truncate text-amber-600">Set this agent's purpose to share it.</span>}
            {purpose && <button type="button" aria-expanded={openPurpose === row.tab} aria-label={openPurpose === row.tab ? 'Show less of the purpose' : 'Show the whole purpose'} title={openPurpose === row.tab ? 'Show less' : purpose} onClick={() => setOpenPurpose(openPurpose === row.tab ? '' : row.tab)} className="grid h-5 w-5 shrink-0 place-items-center self-start rounded text-muted-foreground hover:bg-muted hover:text-foreground"><Info aria-hidden="true" className="h-3 w-3" /></button>}
            <button type="button" aria-label={purpose ? `Change ${row.name}'s purpose` : `Set ${row.name}'s purpose`} title={purpose ? 'Change purpose' : 'Set purpose'} onClick={() => setPurpose(row.tab)} className="grid h-5 w-5 shrink-0 place-items-center self-start rounded text-muted-foreground hover:bg-muted hover:text-foreground"><Pencil aria-hidden="true" className="h-3 w-3" /></button>
          </div>
          <div className="flex flex-wrap items-center gap-2"><div role="radiogroup" aria-label={`Who can message ${row.name}`} className="inline-flex overflow-hidden rounded-full border border-border text-xs">
            {(['team', 'chosen', 'everyone'] as const).map(choice => <button key={choice} type="button" role="radio" aria-checked={agent.audience === choice} disabled={busy} onClick={() => update(row.tab, { audience: choice })} className={`rounded-full px-3 py-0.5 ${agent.audience === choice ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:bg-muted'}`}>{choice === 'team' ? 'My team' : choice === 'chosen' ? 'Chosen people' : 'Everyone in the company'}</button>)}
          </div>{agent.audience === 'everyone' && network && <span className="text-[11px] text-muted-foreground">everyone on {network}</span>}</div>
          {agent.audience === 'chosen' && <div className="flex flex-wrap items-center gap-1.5">
            {(agent.people || []).map(id => <span key={id} className="inline-flex items-center gap-1 rounded-full border border-border bg-muted px-2.5 py-0.5 text-xs">{personName(id)}<button type="button" aria-label={`Remove ${personName(id)}`} onClick={() => update(row.tab, { people: (agent.people || []).filter(person => person !== id) })} className="text-muted-foreground hover:text-foreground">×</button></span>)}
            <button type="button" aria-expanded={adding === row.tab} onClick={() => { setAdding(adding === row.tab ? '' : row.tab); setSearch('') }} className="rounded-full border border-dashed border-border px-2.5 py-0.5 text-xs text-primary hover:bg-muted">+ Add</button>
          </div>}
          {agent.audience === 'chosen' && adding === row.tab && <div className="w-64 max-w-full overflow-hidden rounded-2xl border border-border bg-popover shadow">
            <input autoFocus aria-label="Search people" value={search} onChange={event => setSearch(event.target.value)} onKeyDown={event => { if (event.key === 'Escape') { setAdding(''); setSearch('') } }} placeholder="Search people" className="w-full border-b border-border bg-transparent px-3 py-1.5 text-xs outline-none" />
            <div className="max-h-56 overflow-y-auto">
              {choices(agent).map(person => <button key={person.id} type="button" onClick={() => { update(row.tab, { people: [...(agent.people || []), person.id] }); setSearch('') }} className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs hover:bg-muted">
                <span aria-hidden="true" className="grid h-5 w-5 shrink-0 place-items-center rounded-full bg-muted text-[10px]">{person.username.slice(0, 2).toUpperCase()}</span>
                <span className="truncate">{person.username}</span>{person.email && person.email !== person.username && <span className="ml-auto truncate text-muted-foreground">{person.email}</span>}
              </button>)}
              {!choices(agent).length && <p className="px-3 py-1.5 text-xs text-muted-foreground">{team?.people.length ? 'Nobody found' : 'Add people to the Team above'}</p>}
            </div>
          </div>}
          <label className="flex items-center gap-2 text-xs"><Switch aria-label={`${row.name} can make changes for them`} checked={Boolean(agent.can_change)} disabled={busy} onCheckedChange={on => update(row.tab, { can_change: on })} />Can make changes for them</label>
          <p className="text-[11px] text-muted-foreground">{agent.can_change ? 'It can change files, run commands and use your apps and logins for them.' : 'It only answers their questions.'}</p>
          <p className="flex flex-wrap items-center gap-1.5 text-[11px] text-muted-foreground">Use it from
            {([[Globe, 'Website'], [Smartphone, 'Phone'], [MessageSquare, 'ChatGPT or Claude'], [Hash, 'Slack']] as [LucideIcon, string][]).map(([Icon, label]) => <Icon key={label} role="img" aria-label={label} className="h-3.5 w-3.5"><title>{label}</title></Icon>)}
            their apps ·<Terminal role="img" aria-label="Claude Code, Codex and other agents" className="h-3.5 w-3.5"><title>Claude Code, Codex and other agents</title></Terminal>their AI agents</p>
          {agent.address && <p className="flex items-center gap-2 text-xs"><span className="min-w-0 truncate rounded-full bg-muted px-2.5 py-0.5 font-mono">agent:{agent.address}</span>
            <button type="button" className="shrink-0 rounded-full border border-border px-2 py-0.5 text-[11px] hover:bg-muted" onClick={() => void navigator.clipboard?.writeText(`agent:${agent.address}`).then(() => { setCopied(row.tab); window.setTimeout(() => setCopied(''), 1500) })}>{copied === row.tab ? 'Copied' : 'Copy'}</button></p>}
        </div>}
      </div>
    })}
    {rows.length <= 1 && <p className="text-xs text-muted-foreground">Add an agent with + in the chat to share it.</p>}
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
