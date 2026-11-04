import { Loader2, Plus } from 'lucide-react'
import { AgentHoverCard, useAgentsViewStore, useCrewAgentsStore, type CrewAgentRow } from '../chat/AgentCard'
import { useAgentMenuState } from '../chat/agentMenuState'
import { useChatStore } from '../../stores/useChatStore'
import { activateTab } from '../../utils/activateTab'

/** The open Crew's agents in the app's left menu (owner, 4 Nov, "Left menu", the default): one row each with its name,
 * a mark at the end only when needed, the agent card on hover, and "+ New agent". */
export function SidebarCrewAgents({ projectId }: { projectId: string }) {
  const view = useAgentsViewStore(state => state.view)
  const crew = useCrewAgentsStore()
  const activeTabId = useChatStore(state => state.activeTabId)
  if (view !== 'menu' || crew.projectId !== projectId || !crew.agents.length) return null
  return <div role="list" aria-label="Agents" className="ml-6 space-y-0.5 border-l border-border pb-1 pl-1.5">
    {crew.agents.map(agent => <AgentRow key={agent.tabId} agent={agent} active={agent.tabId === activeTabId} />)}
    {crew.canAdd && <button type="button" onClick={() => window.dispatchEvent(new CustomEvent('agentworks:new-agent', { detail: { projectId } }))}
      className="flex w-full items-center gap-1.5 rounded-md px-2 py-1 text-left text-xs text-primary hover:bg-muted">
      <Plus aria-hidden="true" className="h-3 w-3" />New agent
    </button>}
  </div>
}

function AgentRow({ agent, active }: { agent: CrewAgentRow; active: boolean }) {
  const state = useAgentMenuState(agent.tabId, agent.activity)
  const mark = state === 'running' ? <Loader2 role="img" aria-label="Working" className="h-3 w-3 shrink-0 animate-spin text-[hsl(var(--info))]" />
    : state === 'waiting' ? <span role="img" aria-label="Needs you" title="Needs you" className="h-1.5 w-1.5 shrink-0 rounded-full bg-[hsl(var(--warning))] motion-safe:animate-pulse" />
      : state === 'failed' ? <span role="img" aria-label="Failed" title="Failed" className="h-1.5 w-1.5 shrink-0 rounded-full bg-destructive" />
        : agent.asking ? <span role="img" aria-label="Someone is asking it now" title="Someone is asking it now" className="h-1.5 w-1.5 shrink-0 rounded-full bg-emerald-500 motion-safe:animate-pulse" />
          : state === 'done' ? <span role="img" aria-label="New reply" title="New reply" className="h-1.5 w-1.5 shrink-0 rounded-full bg-[hsl(var(--info))]" /> : null
  return <AgentHoverCard info={agent.info}>
    <button type="button" role="listitem" aria-current={active ? 'page' : undefined} onClick={() => activateTab(agent.tabId)}
      className={`flex w-full min-w-0 items-center gap-2 rounded-md px-2 py-1 text-left text-[13px] ${active ? 'bg-foreground/[0.06] font-medium text-foreground' : 'text-muted-foreground hover:bg-muted hover:text-foreground'}`}>
      <span className="min-w-0 flex-1 truncate">{agent.name}</span>{mark}
    </button>
  </AgentHoverCard>
}
