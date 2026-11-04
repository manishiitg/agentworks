import { useEffect } from 'react'
import { create } from 'zustand'
import { useShallow } from 'zustand/react/shallow'
import { useChatStore } from '../../stores/useChatStore'
import { useMobileViewport } from '../../hooks/useMobileViewport'
import { activateTab } from '../../utils/activateTab'
import { useCrewAgentsStore, type CrewAgentRow } from './AgentCard'
import { useAgentMenuState } from './agentMenuState'
import { AgentIcon, AgentMark } from './AgentIcon'

// Quick switch in the chat box (owner, 4 Nov): the open Crew agent (icon and name) plus up to four others as icons with
// their marks, most recently used first, then "+N" (opens the team board). Its own group, apart from the model picker.
const MAX_OTHERS = 4

/** Each Crew's agent tabs, most recently opened first. Kept for this page visit only. */
const useRecentAgentsStore = create<{ byProject: Record<string, string[]>; touch: (projectId: string, tabId: string) => void }>(set => ({
  byProject: {},
  touch: (projectId, tabId) => set(state => {
    const list = state.byProject[projectId] || []
    if (list[0] === tabId) return state
    return { byProject: { ...state.byProject, [projectId]: [tabId, ...list.filter(id => id !== tabId)].slice(0, 20) } }
  }),
}))

export default function ComposerAgentChips() {
  const activeTabId = useChatStore(state => state.activeTabId)
  const { projectId, agents } = useCrewAgentsStore(useShallow(state => ({ projectId: state.projectId, agents: state.agents })))
  const recent = useRecentAgentsStore(state => (projectId ? state.byProject[projectId] : undefined))
  const mobile = useMobileViewport()
  const current = activeTabId ? agents.find(agent => agent.tabId === activeTabId) : undefined

  useEffect(() => {
    if (projectId && current) useRecentAgentsStore.getState().touch(projectId, current.tabId)
  }, [projectId, current])

  if (mobile || !current || agents.length < 2) return null
  const others = agents.filter(agent => agent.tabId !== current.tabId)
  const order = (recent || []).filter(id => id !== current.tabId)
  // Recently used first; the rest keep their tab order, so a Crew opened just now still offers a switch.
  const rank = (agent: CrewAgentRow) => { const i = order.indexOf(agent.tabId); return i < 0 ? order.length + others.indexOf(agent) : i }
  const shown = [...others].sort((a, b) => rank(a) - rank(b)).slice(0, MAX_OTHERS)

  const more = others.length - shown.length
  return <div role="list" aria-label="Switch agent" className="ml-1 flex min-w-0 items-center gap-0.5 border-l border-border pl-2" data-testid="composer-agent-chips">
    <span role="listitem" aria-current="page" title={`${current.name} (open now)`}
      className="flex max-w-40 min-w-0 items-center gap-1.5 rounded-full bg-foreground/[0.06] py-0.5 pl-1 pr-2 text-[11px] font-medium text-foreground">
      <AgentIcon name={current.name} icon={current.info?.icon} active /><span className="min-w-0 truncate">{current.name}</span>
    </span>
    {shown.map(agent => <AgentChip key={agent.tabId} agent={agent} />)}
    {more > 0 && <button type="button" title="See all agents" onClick={() => window.dispatchEvent(new CustomEvent('agentworks:open-team-board', { detail: { projectId } }))}
      className="rounded-full px-1.5 py-0.5 text-[11px] text-muted-foreground hover:bg-muted hover:text-foreground">+{more}</button>}
  </div>
}

function AgentChip({ agent }: { agent: CrewAgentRow }) {
  const state = useAgentMenuState(agent.tabId, agent.activity)
  return <button type="button" role="listitem" onClick={() => activateTab(agent.tabId)} title={`Open ${agent.name}`} aria-label={`Open ${agent.name}`}
    className="flex items-center gap-1 rounded-full px-1 py-0.5 hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
    <AgentIcon name={agent.name} icon={agent.info?.icon} /><AgentMark state={state} asking={agent.asking} />
  </button>
}
