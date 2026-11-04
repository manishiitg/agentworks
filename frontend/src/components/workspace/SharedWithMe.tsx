import { useEffect, useState } from 'react'
import { Folder, MessageSquare } from 'lucide-react'
import api from '../../services/api'
import { SharedAgentConversation } from '../../products/work/SharedCrewWorkspace'
import { TeamFolderPanel } from '../../products/work/TeamFolderPanel'
import { useTeams } from '../../products/work/teamApi'
import { useSharedCrewPageStore } from '../../stores/useSharedCrewPageStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'

/** An agent someone shared with you from a Crew on their Mac (PLAT-856-509); the server decides the list. */
export type SharedWithMeAgent = { workspace_id: string; chat_id: string; name: string; owner_name: string; crew_name?: string; purpose?: string; available: boolean }

// One reading for the sidebar and the page.
let latest: SharedWithMeAgent[] = []
const listeners = new Set<(agents: SharedWithMeAgent[]) => void>()
let timer: number | undefined
const load = () => void api.get<{ agents?: SharedWithMeAgent[] }>('/api/shared-with-me', { skipSessionContext: true })
  .then(({ data }) => { latest = data.agents || []; listeners.forEach(notify => notify(latest)) }).catch(() => {})

export function useSharedWithMe(): SharedWithMeAgent[] {
  const [agents, setAgents] = useState<SharedWithMeAgent[]>(latest)
  useEffect(() => {
    listeners.add(setAgents)
    if (listeners.size === 1) {
      load()
      timer = window.setInterval(() => { if (!document.hidden) load() }, 60_000)
      window.addEventListener('focus', load)
    }
    return () => {
      listeners.delete(setAgents)
      if (listeners.size) return
      window.clearInterval(timer)
      window.removeEventListener('focus', load)
    }
  }, [])
  return agents
}

/** The Crews you were shared agents from: one row each ("test · demo"); the open one lists the agents shared with you
 * below it, like a Crew's agents in the left menu, and each opens the page on that agent. */
export function useSharedCrewRows() {
  const agents = useSharedWithMe()
  const open = useSharedCrewPageStore(state => state.open)
  const current = useSharedCrewPageStore(state => state.workspaceId)
  const chatId = useSharedCrewPageStore(state => state.chatId)
  const crews = [...new Set(agents.map(agent => agent.workspace_id))]
  return crews.map(id => {
    const mine = agents.filter(agent => agent.workspace_id === id)
    const shown = mine.find(agent => agent.chat_id === chatId) || mine[0]
    return <div key={id}>
      <SharedCrewRow label={crewLabel(mine[0])} available={mine.some(agent => agent.available)} selected={current === id} onOpen={() => { open(id); closeDrawer() }} />
      {current === id && <div role="list" aria-label="Agents" className="ml-6 space-y-0.5 border-l border-border pb-1 pl-1.5">
        {mine.map(agent => <button key={agent.chat_id} type="button" role="listitem" aria-current={agent === shown ? 'page' : undefined} onClick={() => { open(id, agent.chat_id); closeDrawer() }} title={agent.name}
          className={`flex w-full min-w-0 items-center gap-2 rounded-md px-2 py-1 text-left text-[13px] max-md:min-h-10 ${agent === shown ? 'bg-foreground/[0.06] font-medium text-foreground' : 'text-muted-foreground hover:bg-muted hover:text-foreground'}`}>
          <span className="min-w-0 flex-1 truncate">{agent.name}</span>
        </button>)}
      </div>}
    </div>
  })
}

// The phone drawer closes once something is opened from it.
const closeDrawer = () => window.dispatchEvent(new Event('close-app-sidebar'))
const crewLabel = (agent: SharedWithMeAgent) => `${agent.crew_name || agent.name} · ${agent.owner_name}`

function SharedCrewRow({ label, available, selected, onOpen }: { label: string; available: boolean; selected: boolean; onOpen: () => void }) {
  return <button type="button" onClick={onOpen} aria-current={selected ? 'page' : undefined} title={label}
    className={`flex min-h-12 w-full items-center gap-2 rounded-lg px-3 py-1 text-left ${selected ? 'bg-foreground/[0.06]' : 'text-foreground/80 hover:bg-muted hover:text-foreground'}`}>
    <MessageSquare aria-hidden="true" className="h-4 w-4 shrink-0" />
    <span className="min-w-0 flex-1"><span className="block truncate text-[13px] leading-5">{label}</span>
      <span className="flex items-center gap-1 text-[11px] leading-4 text-muted-foreground"><span aria-hidden="true" className={`h-1.5 w-1.5 rounded-full ${available ? 'bg-emerald-500' : 'border border-muted-foreground'}`} />{available ? 'Available' : 'Mac off'}</span></span>
  </button>
}

/** A Crew someone shared agents with you from, as a Crew page (owner, 4 Nov: "a normal Crew will open up"): its name,
 * a tab per agent shared with you, and your own chat with each. No files, settings or menu: you use it, you don't build it. */
export function SharedCrewPage() {
  const workspaceId = useSharedCrewPageStore(state => state.workspaceId)
  const chatId = useSharedCrewPageStore(state => state.chatId)
  const open = useSharedCrewPageStore(state => state.open)
  const agents = useSharedWithMe().filter(agent => agent.workspace_id === workspaceId)
  // The Crew's shared folder, for a member of its Team who may edit it (the server lists only those teams).
  const teams = useTeams()
  const team = teams.find(row => !row.target.connection && `work:${row.team.project_id}` === workspaceId)
  const [folderOpen, setFolderOpen] = useState(false)
  useEffect(() => { setFolderOpen(false) }, [workspaceId])
  // Opening anything else from the sidebar leaves this page.
  useEffect(() => useProductSurfaceStore.subscribe((state, previous) => {
    if (state.productSurface !== previous.productSurface || state.selectedWorkProjectId !== previous.selectedWorkProjectId) open(null)
  }), [open])
  if (!workspaceId) return null
  const agent = agents.find(item => item.chat_id === chatId) || agents[0]
  return <div className="flex h-full min-h-0 flex-col bg-background">
    {/* On a phone the sidebar button floats at the top left, so the header leaves room for it. */}
    <header className="flex shrink-0 items-center gap-2 border-b border-border px-3 py-2 max-md:min-h-[60px] max-md:pl-16">
      <span className="flex min-w-0 max-w-[60%] items-center px-1 text-sm font-semibold"><span className="truncate">{agents[0] ? crewLabel(agents[0]) : 'Shared Crew'}</span></span>
      <nav aria-label="Agents" className="flex min-w-0 items-center gap-1 overflow-x-auto [scrollbar-width:none]">
        {agents.map(item => <button key={item.chat_id} type="button" aria-current={item === agent && !folderOpen ? 'page' : undefined} onClick={() => { setFolderOpen(false); open(workspaceId, item.chat_id) }}
          className={`flex shrink-0 items-center rounded-lg px-2.5 py-1 text-[13px] font-medium max-md:min-h-10 ${item === agent && !folderOpen ? 'bg-muted text-foreground' : 'text-muted-foreground hover:bg-muted hover:text-foreground'}`}>
          {item.name}
        </button>)}
        {team && <button type="button" aria-current={folderOpen ? 'page' : undefined} onClick={() => setFolderOpen(true)}
          className={`flex shrink-0 items-center gap-1.5 rounded-lg px-2.5 py-1 text-[13px] font-medium max-md:min-h-10 ${folderOpen ? 'bg-muted text-foreground' : 'text-muted-foreground hover:bg-muted hover:text-foreground'}`}>
          <Folder aria-hidden="true" className="h-3.5 w-3.5" />Shared folder
        </button>}
      </nav>
    </header>
    {folderOpen && team ? <TeamFolderPanel key={team.team.project_id} {...team} onAsk={() => setFolderOpen(false)} />
      : !agents.length ? <p role="status" className="p-6 text-sm text-muted-foreground">Nothing from this Crew is shared with you right now.</p>
      : agent && <SharedAgentConversation key={agent.chat_id} scope={{ workspace_id: agent.workspace_id }} agent={{ chat_id: agent.chat_id, name: agent.name, purpose: agent.purpose, shared: true }} ownerName={agent.owner_name} />}
  </div>
}
