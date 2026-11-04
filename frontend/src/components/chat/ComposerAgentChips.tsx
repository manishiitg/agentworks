import { useEffect } from 'react'
import { Loader2 } from 'lucide-react'
import { create } from 'zustand'
import { useShallow } from 'zustand/react/shallow'
import { useChatStore } from '../../stores/useChatStore'
import { useMobileViewport } from '../../hooks/useMobileViewport'
import { activateTab } from '../../utils/activateTab'
import { useCrewAgentsStore, type CrewAgentRow } from './AgentCard'
import { useAgentMenuState } from './agentMenuState'

// Quick switch in the chat box (owner, 4 Nov): the open Crew agent plus up to three others, most recently used first.
const MAX_OTHERS = 3

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

  return <div role="list" aria-label="Switch agent" className="flex min-w-0 items-center gap-1" data-testid="composer-agent-chips">
    <span role="listitem" aria-current="page" title={`${current.name} (open now)`}
      className="max-w-32 truncate rounded-full bg-primary/10 px-2 py-0.5 text-[11px] font-medium text-foreground ring-1 ring-primary/30">
      {current.name}
    </span>
    {shown.map(agent => <AgentChip key={agent.tabId} agent={agent} />)}
  </div>
}

function AgentChip({ agent }: { agent: CrewAgentRow }) {
  const state = useAgentMenuState(agent.tabId, agent.activity)
  const mark = state === 'running' ? <Loader2 role="img" aria-label="Working" className="h-2.5 w-2.5 shrink-0 animate-spin text-[hsl(var(--info))]" />
    : state === 'waiting' ? <span role="img" aria-label="Needs you" className="h-1.5 w-1.5 shrink-0 rounded-full bg-[hsl(var(--warning))] motion-safe:animate-pulse" />
      : state === 'failed' ? <span role="img" aria-label="Failed" className="h-1.5 w-1.5 shrink-0 rounded-full bg-destructive" />
        : agent.asking ? <span role="img" aria-label="Someone is asking it now" className="h-1.5 w-1.5 shrink-0 rounded-full bg-emerald-500 motion-safe:animate-pulse" />
          : state === 'done' ? <span role="img" aria-label="New reply" className="h-1.5 w-1.5 shrink-0 rounded-full bg-[hsl(var(--info))]" /> : null
  return <button type="button" role="listitem" onClick={() => activateTab(agent.tabId)} title={`Open ${agent.name}`}
    className="flex max-w-32 min-w-0 items-center gap-1 rounded-full border border-border px-2 py-0.5 text-[11px] text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
    <span className="min-w-0 truncate">{agent.name}</span>{mark}
  </button>
}
