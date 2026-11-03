import { useEffect, useState } from 'react'
import { MessageSquare, X } from 'lucide-react'
import api from '../../services/api'
import { SharedAgentConversation } from '../../products/work/SharedCrewWorkspace'

/** An agent someone shared with you from a Crew on their Mac (PLAT-856-509); the server decides the list. */
export type SharedWithMeAgent = { workspace_id: string; chat_id: string; name: string; owner_name: string; purpose?: string; available: boolean }

export function useSharedWithMe(): SharedWithMeAgent[] {
  const [agents, setAgents] = useState<SharedWithMeAgent[]>([])
  useEffect(() => {
    let live = true
    const load = () => void api.get<{ agents?: SharedWithMeAgent[] }>('/api/shared-with-me', { skipSessionContext: true })
      .then(({ data }) => { if (live) setAgents(data.agents || []) }).catch(() => {})
    load()
    const timer = window.setInterval(() => { if (!document.hidden) load() }, 60_000)
    window.addEventListener('focus', load)
    return () => { live = false; window.clearInterval(timer); window.removeEventListener('focus', load) }
  }, [])
  return agents
}

export function SharedAgentRow({ agent, selected, onOpen }: { agent: SharedWithMeAgent; selected: boolean; onOpen: () => void }) {
  return <button type="button" onClick={onOpen} aria-current={selected ? 'page' : undefined} title={agent.purpose || agent.name}
    className={`flex min-h-12 w-full items-center gap-2 rounded-lg px-3 py-1 text-left ${selected ? 'bg-foreground/[0.06]' : 'text-foreground/80 hover:bg-muted hover:text-foreground'}`}>
    <MessageSquare aria-hidden="true" className="h-4 w-4 shrink-0" />
    <span className="min-w-0 flex-1"><span className="block truncate text-[13px] leading-5">{agent.name} · {agent.owner_name}</span>
      <span className="flex items-center gap-1 text-[11px] leading-4 text-muted-foreground"><span aria-hidden="true" className={`h-1.5 w-1.5 rounded-full ${agent.available ? 'bg-emerald-500' : 'border border-muted-foreground'}`} />{agent.available ? 'Available' : 'Mac off'}</span></span>
  </button>
}

/** Your own chat with a shared agent: only your messages and its answers. */
export function SharedAgentSheet({ agent, onClose }: { agent: SharedWithMeAgent; onClose: () => void }) {
  return <div role="dialog" aria-label={`${agent.name} · ${agent.owner_name}`} className="fixed inset-0 z-50 flex justify-end bg-background/60 md:p-3">
    <div className="relative flex h-full w-full flex-col overflow-hidden border-border bg-background shadow-xl md:max-w-xl md:rounded-2xl md:border">
      <button type="button" aria-label="Close" onClick={onClose} className="absolute right-2 top-2 z-10 rounded p-1.5 hover:bg-muted"><X className="h-4 w-4" /></button>
      <SharedAgentConversation scope={{ workspace_id: agent.workspace_id }} agent={{ chat_id: agent.chat_id, name: `${agent.name} · ${agent.owner_name}`, purpose: agent.purpose, shared: true }} ownerName={agent.owner_name} onBack={onClose} />
    </div>
  </div>
}
