import { useState } from 'react'
import { ChevronRight } from 'lucide-react'

export type TeamLiveAgent = { id: string; owner_id: string; tab: string; chat_key: string; name: string; person: string; label: string; activity: string }
export type TeamLiveView = { agents: TeamLiveAgent[]; idle: TeamLiveAgent[]; summary: string }

/** All labels and live/idle classification come from the admitted server view.
 * Opening reuses the corresponding chip/chat row, never a guessed identity. */
export function TeamLiveAgents({ live, openFor }: { live?: TeamLiveView; openFor: (agent: TeamLiveAgent) => (() => void) | undefined }) {
  const [idleOpen, setIdleOpen] = useState(false)
  const row = (agent: TeamLiveAgent, idle = false) => {
    const open = openFor(agent)
    return <button key={agent.id} type="button" role="listitem" disabled={!open} onClick={open} className="flex w-full min-w-0 items-center gap-1.5 rounded-md px-2 py-1 text-left text-[13px] text-muted-foreground enabled:hover:bg-muted enabled:hover:text-foreground max-md:min-h-10">
      {!idle && <span aria-hidden="true" className={`h-1.5 w-1.5 shrink-0 rounded-full ${agent.activity === 'waiting' ? 'bg-amber-500' : 'bg-emerald-500'}`} />}
      <span className="truncate">{agent.label}</span>
    </button>
  }
  return <>{live?.agents.map(agent => row(agent))}{!!live?.idle.length && <>
    <button type="button" aria-expanded={idleOpen} onClick={() => setIdleOpen(value => !value)} className="flex w-full items-center gap-1.5 rounded-md px-2 py-1 text-left text-xs text-muted-foreground hover:bg-muted max-md:min-h-10"><ChevronRight aria-hidden="true" className={`h-3 w-3 transition-transform ${idleOpen ? 'rotate-90' : ''}`} />{live.idle.length} others idle</button>
    {idleOpen && live.idle.map(agent => row(agent, true))}
  </>}</>
}
