import { Plus } from 'lucide-react'
import { AgentHoverCard, useAgentsViewStore, useCrewAgentsStore, type CrewAgentRow } from '../chat/AgentCard'
import { AgentIcon, AgentMark } from '../chat/AgentIcon'
import { useAgentMenuState } from '../chat/agentMenuState'
import { useChatStore } from '../../stores/useChatStore'
import { activateTab } from '../../utils/activateTab'

/** The open Crew's agents in the app's left menu (owner, 4 Nov, "Left menu", the default): one row each with its icon,
 * name and a mark at the end only when needed, the agent card on hover, and "+ New agent". */
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
  return <AgentHoverCard info={agent.info}>
    <button type="button" role="listitem" aria-current={active ? 'page' : undefined} onClick={() => activateTab(agent.tabId)}
      className={`flex w-full min-w-0 items-center gap-2 rounded-md px-2 py-1 text-left text-[13px] ${active ? 'bg-foreground/[0.06] font-medium text-foreground' : 'text-muted-foreground hover:bg-muted hover:text-foreground'}`}>
      <AgentIcon name={agent.name} icon={agent.info?.icon} active={active} />
      <span className="min-w-0 flex-1 truncate">{agent.name}</span><AgentMark state={state} asking={agent.asking} />
    </button>
  </AgentHoverCard>
}

/** The collapsed sidebar (icon rail) keeps the open Crew's agents (owner, 4 Nov): the Crew's letter (opens its team
 * board), then one icon per agent with its mark. Shown only while one of that Crew's agents is open. */
export function RailCrewAgents() {
  const crew = useCrewAgentsStore()
  const activeTabId = useChatStore(state => state.activeTabId)
  if (!crew.projectId || !crew.agents.some(agent => agent.tabId === activeTabId)) return null
  const projectId = crew.projectId
  return <div role="list" aria-label="Agents" className="mt-2 flex min-h-0 flex-col items-center gap-1 overflow-y-auto border-t border-border/70 pt-2 [scrollbar-width:none]">
    <button type="button" title={`${crew.name || 'Crew'}: see all agents`} aria-label={`${crew.name || 'Crew'}: see all agents`}
      onClick={() => window.dispatchEvent(new CustomEvent('agentworks:open-team-board', { detail: { projectId } }))}
      className="grid h-8 w-8 place-items-center rounded-full hover:bg-muted"><AgentIcon name={crew.name || 'Crew'} size="md" round /></button>
    {crew.agents.map(agent => <RailAgent key={agent.tabId} agent={agent} active={agent.tabId === activeTabId} />)}
  </div>
}

function RailAgent({ agent, active }: { agent: CrewAgentRow; active: boolean }) {
  const state = useAgentMenuState(agent.tabId, agent.activity)
  // Every agent the same box; the open one highlighted. Hover shows its card to the right of the rail.
  return <AgentHoverCard info={agent.info} side="right">
    <button type="button" role="listitem" aria-current={active ? 'page' : undefined} title={agent.info ? undefined : agent.name} aria-label={agent.name} onClick={() => activateTab(agent.tabId)}
      className="relative grid h-8 w-8 place-items-center rounded-md hover:opacity-90">
      <AgentIcon name={agent.name} icon={agent.info?.icon} size="md" active={active} />
      <span className="absolute -right-0.5 -top-0.5"><AgentMark state={state} asking={agent.asking} /></span>
    </button>
  </AgentHoverCard>
}
