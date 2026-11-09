import { useEffect, useState } from 'react'
import { Cable, MessageSquare } from 'lucide-react'
import api from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { useChatStore } from '../../stores/useChatStore'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'

type Agent = {
  id: string; target?: string; name?: string; agent_name?: string; label?: string; kind: string; provider?: string; network?: string
  purpose?: string; description?: string; online?: boolean; presence?: string
  chats?: { chat_id: string; name: string; purpose?: string; owner_name?: string }[]
}
const name = (agent: Agent) => agent.label || agent.name || agent.agent_name || 'Agent'
const providerLabel = (provider?: string) => ({ claude: 'Claude Code', codex: 'Codex', cursor: 'Cursor', muse: 'Muse', pi: 'Pi' } as Record<string, string>)[(provider || '').toLowerCase()] || provider || 'On your computer'

/** The same authorized directory as chat/MCP, opened beside this project's chat. */
export function ProjectAgentBridgePanel({ target, tabId, onAsk, view }: {
  target: string; tabId?: string; onAsk: (message: string) => void | Promise<unknown>
  /** coding: coding apps on this Mac (Connections); others: published agents (Full menu, Message other agents). */
  view: 'coding' | 'others'
}) {
  const user = useAuthStore(state => state.user?.id)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const streaming = useChatStore(state => tabId ? state.chatTabs[tabId]?.isStreaming === true : false)
  const scope = `${server}:${user}:${target}`
  const [snapshot, setSnapshot] = useState<{ scope: string; agents: Agent[] } | null>(null)
  const agents = snapshot?.scope === scope ? snapshot.agents : null
  const [error, setError] = useState('')
  const [attempt, setAttempt] = useState(0)
  useEffect(() => {
    const identity = captureChatIdentity(), controller = new AbortController()
    setSnapshot(null); setError('')
    void api.get<{ agents: Agent[]; network_error?: string }>('/api/agent-directory', { signal: controller.signal, skipSessionContext: true, params: { ...(view === 'others' ? { view: 'message' } : {}), ...(target.startsWith('workspace:work:') ? { crew: target.slice('workspace:work:'.length) } : {}) } })
      .then(({ data }) => { if (!controller.signal.aborted && isChatIdentityCurrent(identity)) { setSnapshot({ scope, agents: data.agents || [] }); if (data.network_error) setError(data.network_error) } })
      .catch(() => { if (!controller.signal.aborted && isChatIdentityCurrent(identity)) setError('Could not load agents. Try again.') })
    return () => controller.abort()
  }, [target, user, server, scope, streaming, attempt, view])
  const ask = (message: string) => { void Promise.resolve().then(() => onAsk(message)).catch(cause => setError(cause instanceof Error ? cause.message : 'Could not open this chat.')) }
  const connected = agents?.filter(agent => ['connected_agent', 'agent'].includes(agent.kind)) || []
  // Provider identifies a native CLI, not which computer or person owns it.
  const local = connected.filter(agent => agent.provider && !agent.network).sort((a, b) => Number(b.online === true) - Number(a.online === true))
  const network = connected.filter(agent => !agent.provider || agent.network)
  // Crews you own or that are shared with you, and their shared agents; never this Crew itself.
  const crewsAndAgents = agents?.filter(agent => (agent.kind === 'crew' || agent.kind === 'crew_agent') && agent.id !== target && agent.target !== target) || []
  const row = (agent: Agent, onComputer: boolean) => <button type="button" key={agent.id} onClick={() => ask(`Help me contact ${JSON.stringify(name(agent))} through AgentBridge (target ${JSON.stringify(agent.target || agent.id)}). Show recent messages and ask what I want to send. Use this exact agent and report its actual delivery status. If it is unavailable, tell me; do not substitute execution in this server Crew.`)} className={`flex w-full items-start gap-3 rounded-lg border p-3 text-left hover:bg-muted ${onComputer ? 'border-primary/50 bg-background' : 'border-border'}`}><Cable aria-hidden="true" className={`mt-0.5 h-4 w-4 shrink-0 ${onComputer ? 'text-primary' : 'text-muted-foreground'}`} /><span className="min-w-0 flex-1"><span className="flex items-center gap-2"><span className="truncate font-medium">{name(agent)}</span>{onComputer && <span className="shrink-0 rounded bg-primary/15 px-1.5 py-0.5 text-[11px] font-medium text-primary">{providerLabel(agent.provider)}</span>}</span><span className="flex items-center gap-1.5 text-xs text-muted-foreground">{agent.online === true && <span aria-hidden="true" className="h-1.5 w-1.5 rounded-full bg-green-500" />}{agent.presence || (agent.online === true ? 'Online' : agent.online === false ? 'Offline' : 'Connection unknown')}</span>{agent.purpose && <span className="block text-xs text-muted-foreground">{agent.purpose}</span>}</span><MessageSquare aria-hidden="true" className="h-4 w-4 shrink-0 text-muted-foreground" /></button>
  return <div className="max-w-2xl space-y-4 text-sm">
    {error && <p role="alert" className="text-xs text-destructive">{error} <button type="button" className="underline" onClick={() => setAttempt(n => n + 1)}>Try again</button></p>}
    {!agents && !error && <p role="status" className="text-xs text-muted-foreground">Loading…</p>}
    {agents && view === 'others' && <section aria-label="Message other agents" className="space-y-3">
      <p className="text-xs text-muted-foreground">Your own agents, agents people have published, and Crews shared with you.</p>
      {/* Coding chats on this Mac (Claude Code, Codex, Cursor) come first, with their program label; they had their own
          page until the owner removed it (4 Nov). */}
      {local.map(agent => row(agent, true))}
      {network.map(agent => row(agent, false))}
      {crewsAndAgents.map(agent => row(agent, false))}
      {!local.length && !network.length && !crewsAndAgents.length && <p className="text-xs text-muted-foreground">No agents to message yet.</p>}
    </section>}
  </div>
}
