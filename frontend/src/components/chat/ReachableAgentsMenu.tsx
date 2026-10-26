import { useEffect, useRef, useState, type FocusEvent } from 'react'
import * as Menu from '@radix-ui/react-dropdown-menu'
import { Bot, Cable, ChevronRight, Loader2, Network, Search } from 'lucide-react'
import api from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { AGENTWORKS_ROLE, AGENTWORKS_SUBTITLE, AgentWorksIcon } from '../ui/AgentWorksIdentity'

type ReachableAgent = {
  id: string
  name?: string
  agent_name?: string
  label?: string
  kind: string
  provider?: string
  description?: string
  purpose?: string
  online?: boolean
  presence?: string
  /** "agentworks" on the person's own AgentWorks; set by the server. */
  role?: string
}
const agentName = (agent: ReachableAgent) => agent.label || agent.name || agent.agent_name || 'Agent'
const AGENT_KINDS: Record<string, string> = { crew: 'Crew', crew_agent: 'Crew agent', goal: 'Goal', relay: 'Relay', connected_agent: 'AgentBridge', agent: 'AgentBridge' }
const agentKind = (agent: ReachableAgent) => AGENT_KINDS[agent.kind] || 'Agent'

/** The same authorized directory as list_agents, fetched only while opened. */
export function ReachableAgentsMenu({ submenu = false }: { submenu?: boolean }) {
  const userId = useAuthStore(state => state.user?.id)
  const workspace = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const scope = `${workspace}:${userId || ''}`
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [attempt, setAttempt] = useState(0)
  const [snapshot, setSnapshot] = useState<{ scope: string; agents: ReachableAgent[]; loading: boolean; error: boolean } | null>(null)
  const input = useRef<HTMLInputElement>(null)
  const data = snapshot?.scope === scope ? snapshot : null
  useEffect(() => {
    if (!open || !userId) return
    const identity = captureChatIdentity()
    const controller = new AbortController()
    setQuery('')
    setSnapshot({ scope, agents: [], loading: true, error: false })
    void api.get<{ agents: ReachableAgent[] }>('/api/agent-directory', { signal: controller.signal, skipSessionContext: true })
      .then(response => {
        if (!controller.signal.aborted && isChatIdentityCurrent(identity)) setSnapshot({ scope, agents: response.data.agents || [], loading: false, error: false })
      }).catch(() => {
        if (!controller.signal.aborted && isChatIdentityCurrent(identity)) setSnapshot({ scope, agents: [], loading: false, error: true })
      })
    return () => controller.abort()
  }, [open, scope, userId, attempt])
  if (!userId) return null
  const search = query.trim().toLocaleLowerCase()
  const agents = (data?.agents || []).filter(agent => [agentName(agent), agent.name, agent.agent_name, agentKind(agent), agent.provider, agent.purpose, agent.description].some(text => text?.toLocaleLowerCase().includes(search)))
    .sort((a, b) => Number(b.role === AGENTWORKS_ROLE) - Number(a.role === AGENTWORKS_ROLE) || Number(b.online === true) - Number(a.online === true) || agentName(a).localeCompare(agentName(b)))
  const directory = <>
      <Menu.Label className="px-3 pb-2 pt-3 text-sm font-medium">Agents you can reach</Menu.Label>
      <div className="mx-2 mb-2 flex items-center gap-2 rounded-lg border border-border bg-background px-2.5">
        <Search className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
        <input autoFocus ref={input} aria-label="Search accessible agents" placeholder="Search agents…" value={query} onChange={event => setQuery(event.target.value)} onKeyDown={event => { if (!['ArrowDown', 'ArrowUp', 'Escape', 'Tab'].includes(event.key)) event.stopPropagation() }} className="h-8 min-w-0 flex-1 bg-transparent text-xs outline-none" />
      </div>
      <div role="list" aria-label="Reachable agents" className="max-h-[min(22rem,50dvh)] overflow-y-auto p-1.5">
        {(!data || data.loading) ? <p role="status" className="flex items-center gap-2 px-2 py-3 text-xs text-muted-foreground"><Loader2 className="h-3.5 w-3.5 animate-spin" />Loading agents…</p>
          : data.error ? <div className="px-2 py-3 text-xs"><p>Could not load your agents.</p><button type="button" onClick={() => setAttempt(value => value + 1)} className="mt-2 text-primary underline">Try again</button></div>
          : !agents.length ? <p className="px-2 py-3 text-xs text-muted-foreground">{search ? 'No agents match your search.' : 'No agents are available to this account yet.'}</p>
          : agents.map(agent => {
            const connected = agentKind(agent) === 'AgentBridge'
            const agentWorks = agent.role === AGENTWORKS_ROLE
            const Icon = connected ? Cable : Bot
            const name = agentName(agent)
            return <div role="listitem" key={agent.id} title={agent.purpose || agent.description || name} className="flex items-start gap-2.5 rounded-lg px-2 py-2">
              {agentWorks ? <AgentWorksIcon className="h-4 w-4" /> : <Icon className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />}
              <span className="min-w-0 flex-1"><span className="block truncate text-xs font-medium">{name}</span><span className="block truncate text-[10px] text-muted-foreground">{agentWorks ? AGENTWORKS_SUBTITLE : agentKind(agent)}{agent.provider ? ` · ${agent.provider}` : ''} · {agent.presence || (connected ? agent.online === false ? 'Offline' : 'Status unavailable' : 'Runs when called')}</span>{(agent.purpose || agent.description) && <span className="mt-0.5 line-clamp-2 text-[11px] text-muted-foreground">{agent.purpose || agent.description}</span>}</span>
            </div>
          })}
      </div>
      <p className="border-t border-border px-3 py-2 text-[10px] text-muted-foreground">Ask this chat to contact an agent by name.</p>
  </>
  const contentClass = 'z-[101] w-[min(360px,calc(100vw-24px))] overflow-hidden rounded-xl border border-border bg-popover text-popover-foreground shadow-lg'
  const focusSearch = (event: FocusEvent<HTMLDivElement>) => {
    if (event.target === event.currentTarget) input.current?.focus()
  }
  if (submenu) return <Menu.Sub open={open} onOpenChange={setOpen}>
    <Menu.SubTrigger aria-label="Agents you can reach" className="flex cursor-pointer items-center gap-2.5 rounded-md px-2.5 py-2 text-sm outline-none data-[highlighted]:bg-muted data-[state=open]:bg-muted">
      <Network className="h-4 w-4" aria-hidden="true" /><span>Agents</span><ChevronRight className="ml-auto h-3.5 w-3.5" aria-hidden="true" />
    </Menu.SubTrigger>
    <Menu.Portal><Menu.SubContent role="dialog" aria-label="Accessible agents" sideOffset={6} collisionPadding={12} onFocusCapture={focusSearch} className={contentClass}>
      {directory}
    </Menu.SubContent></Menu.Portal>
  </Menu.Sub>
  return <Menu.Root open={open} onOpenChange={setOpen} modal={false}>
    <Menu.Trigger aria-haspopup="dialog" title="Agents you can reach" aria-label="Agents you can reach" className="inline-flex h-7 shrink-0 items-center gap-1.5 rounded px-1 text-[11px] text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
      <Network className="h-3.5 w-3.5" aria-hidden="true" /><span>Agents{data && !data.loading && !data.error ? ` · ${data.agents.length}` : ''}</span>
    </Menu.Trigger>
    <Menu.Portal><Menu.Content role="dialog" aria-label="Accessible agents" side="top" align="start" sideOffset={8} collisionPadding={12} onFocusCapture={focusSearch} className={contentClass}>
      {directory}
    </Menu.Content></Menu.Portal>
  </Menu.Root>
}
