import { useEffect } from 'react'
import { ArrowLeft } from 'lucide-react'
import { useCrewAgentsStore, type CrewAgentRow } from '../../components/chat/AgentCard'
import { useAgentMenuState, type AgentMenuState } from '../../components/chat/agentMenuState'
import { useChatStore } from '../../stores/useChatStore'
import type { PollingEvent } from '../../services/api-types'
import { activateTab } from '../../utils/activateTab'

// Team board (owner, 4 Nov, PLAT-856-509): clicking the Crew name shows every agent as a card with what it is doing
// now; clicking a card opens that agent. Everything here reads what the tab strip and chat store already hold.

const LIVE_CHARS = 80

function payload(event: PollingEvent): Record<string, unknown> {
  const envelope = (event.data && typeof event.data === 'object' ? event.data : {}) as Record<string, unknown>
  const inner = envelope.data
  return (inner && typeof inner === 'object' ? inner : envelope) as Record<string, unknown>
}

/** The tool the agent started last and has not finished yet, if any. */
function runningToolName(events: PollingEvent[] | undefined): string {
  if (!events?.length) return ''
  const finished = new Set<string>()
  for (let index = events.length - 1; index >= 0; index -= 1) {
    const event = events[index]
    const data = payload(event)
    const id = typeof data.tool_call_id === 'string' ? data.tool_call_id : ''
    if (event.type === 'tool_call_end' || event.type === 'tool_call_error') { if (id) finished.add(id); else return ''; continue }
    if (event.type === 'tool_call_start') {
      if (id && finished.has(id)) return ''
      const name = [data.tool_name, data.name].find(value => typeof value === 'string' && value.trim())
      return typeof name === 'string' ? name.trim() : ''
    }
  }
  return ''
}

const STATE_WORDS: Record<NonNullable<AgentMenuState>, string> = { running: 'Working…', waiting: 'Needs you', failed: 'Failed', done: 'New reply' }

function AgentBoardCard({ agent, onOpen }: { agent: CrewAgentRow; onOpen: () => void }) {
  const state = useAgentMenuState(agent.tabId, agent.activity)
  const running = state === 'running'
  const tool = useChatStore(store => {
    const sessionId = store.chatTabs[agent.tabId]?.sessionId
    return running && sessionId ? runningToolName(store.tabEvents[sessionId]) : ''
  })
  const text = useChatStore(store => {
    const sessionId = store.chatTabs[agent.tabId]?.sessionId
    const value = running && sessionId ? (store.streamingText[sessionId] || '').replace(/\s+/g, ' ').trim() : ''
    return value.length > LIVE_CHARS ? `…${value.slice(-LIVE_CHARS)}` : value
  })
  const live = running ? (tool ? `Using ${tool}` : text || STATE_WORDS.running) : state ? STATE_WORDS[state] : agent.asking ? 'Needs you' : 'Idle'
  const purpose = agent.info?.purpose?.trim()
  return <button type="button" onClick={onOpen} data-team-board-agent={agent.tabId}
    className="flex min-w-0 flex-col gap-1.5 rounded-2xl border border-border bg-card p-4 text-left transition-colors hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
    <span className="flex min-w-0 items-center gap-2">
      <span className={`h-2 w-2 shrink-0 rounded-full ${running ? 'bg-primary animate-pulse' : state === 'waiting' || agent.asking ? 'bg-amber-500' : state === 'failed' ? 'bg-destructive' : state === 'done' ? 'bg-primary' : 'bg-muted-foreground/30'}`} aria-hidden="true" />
      <span className="truncate text-sm font-medium">{agent.name}</span>
    </span>
    <span className="truncate text-xs text-muted-foreground">{purpose || 'No purpose set yet'}</span>
    <span className="truncate rounded-md bg-muted/60 px-2 py-1 font-mono text-xs text-foreground/80" aria-live="polite">{live}</span>
  </button>
}

export function TeamBoard({ projectId, onClose }: { projectId: string; onClose: () => void }) {
  const agents = useCrewAgentsStore(state => state.projectId === projectId ? state.agents : null)
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => { if (event.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])
  return <div data-team-board className="absolute inset-0 z-20 flex flex-col overflow-y-auto bg-background">
    <div className="flex items-center gap-2 px-4 pt-4">
      <button type="button" onClick={onClose}
        className="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <ArrowLeft className="h-3.5 w-3.5" aria-hidden="true" />Back to chat
      </button>
      <span className="text-sm font-semibold">Your team</span>
    </div>
    {agents?.length ? <div className="grid grid-cols-1 gap-3 p-4 sm:grid-cols-2 xl:grid-cols-3">
      {agents.map(agent => <AgentBoardCard key={agent.tabId} agent={agent} onOpen={() => { activateTab(agent.tabId); onClose() }} />)}
    </div> : <div className="grid flex-1 place-items-center p-6 text-sm text-muted-foreground">No agents here yet.</div>}
  </div>
}
