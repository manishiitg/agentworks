import { useEffect, useState } from 'react'
import { Bot, Cable, ExternalLink, MessageSquare } from 'lucide-react'
import api from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { useChatStore } from '../../stores/useChatStore'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'

type Agent = {
  id: string; target?: string; name?: string; agent_name?: string; label?: string; kind: string; provider?: string
  purpose?: string; description?: string; online?: boolean; presence?: string
  chats?: { chat_id: string; name: string; purpose?: string; owner_name?: string }[]
}
const name = (agent: Agent) => agent.label || agent.name || agent.agent_name || 'Agent'

/** The same authorized directory as chat/MCP, opened beside this project's chat. */
export function ProjectAgentBridgePanel({ target, tabId, onAsk }: {
  target: string; tabId?: string; onAsk: (message: string) => void | Promise<unknown>
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
    void api.get<{ agents: Agent[] }>('/api/agent-directory', { signal: controller.signal, skipSessionContext: true })
      .then(({ data }) => { if (!controller.signal.aborted && isChatIdentityCurrent(identity)) setSnapshot({ scope, agents: data.agents || [] }) })
      .catch(() => { if (!controller.signal.aborted && isChatIdentityCurrent(identity)) setError('Could not load agents. Try again.') })
    return () => controller.abort()
  }, [target, user, server, scope, streaming, attempt])
  const ask = (message: string) => { void Promise.resolve().then(() => onAsk(message)).catch(cause => setError(cause instanceof Error ? cause.message : 'Could not open this chat.')) }
  const project = agents?.find(agent => agent.id === target)
  const connected = agents?.filter(agent => ['connected_agent', 'agent'].includes(agent.kind)) || []
  const others = agents?.filter(agent => agent.id !== target && !['connected_agent', 'agent', 'crew_agent'].includes(agent.kind)) || []
  return <div className="max-w-2xl space-y-6 text-sm">
    <p className="text-xs text-muted-foreground">Ask this chat to contact an agent. Messages keep your identity; other owners keep control of their tabs.</p>
    {error && <p role="alert" className="text-xs text-destructive">{error} <button type="button" className="underline" onClick={() => setAttempt(n => n + 1)}>Try again</button></p>}
    {!agents && !error && <p role="status" className="text-xs text-muted-foreground">Loading agents…</p>}
    {agents && <>
      <section aria-label="This project's agents" className="space-y-3"><h3 className="font-semibold">In this project</h3>
        {project?.chats?.map(chat => <button key={chat.chat_id} type="button" onClick={() => ask(`Help me contact the agent ${JSON.stringify(chat.name)} in this project (target ${JSON.stringify(target)}, chat_id ${JSON.stringify(chat.chat_id)}). Ask what I want to send.`)} className="flex w-full items-start gap-3 rounded-lg border border-border p-3 text-left hover:bg-muted"><Bot aria-hidden="true" className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" /><span className="min-w-0 flex-1"><span className="block break-words font-medium">{chat.name}</span>{chat.purpose && <span className="block text-xs text-muted-foreground">{chat.purpose}</span>}{chat.owner_name && <span className="block truncate text-xs text-muted-foreground">{chat.owner_name}</span>}</span><MessageSquare aria-hidden="true" className="h-4 w-4 shrink-0 text-muted-foreground" /></button>)}
        {!project?.chats?.length && <p className="text-xs text-muted-foreground">No named agents available here yet.</p>}
      </section>
      <section aria-label="Connected agents" className="space-y-3 border-t border-border pt-4"><h3 className="font-semibold">From your computers and network</h3>
        {connected.map(agent => <button type="button" key={agent.id} onClick={() => ask(`Help me contact ${JSON.stringify(name(agent))} through AgentBridge (target ${JSON.stringify(agent.target || agent.id)}). Show recent messages and ask what I want to send.`)} className="flex w-full items-start gap-3 rounded-lg border border-border p-3 text-left hover:bg-muted"><Cable aria-hidden="true" className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" /><span className="min-w-0 flex-1"><span className="block truncate font-medium">{name(agent)}</span><span className="block text-xs text-muted-foreground">{[agent.provider, agent.presence || (agent.online === true ? 'Online' : agent.online === false ? 'Offline' : 'Connection unknown')].filter(Boolean).join(' · ')}</span>{agent.purpose && <span className="block text-xs text-muted-foreground">{agent.purpose}</span>}</span><MessageSquare aria-hidden="true" className="h-4 w-4 shrink-0 text-muted-foreground" /></button>)}
        {!connected.length && <p className="text-xs text-muted-foreground">No connected agents available. Ask in chat for help connecting your computer.</p>}
      </section>
      {!!others.length && <details className="border-t border-border pt-4"><summary className="cursor-pointer font-medium">Other projects you can reach · {others.length}</summary><div className="mt-3 space-y-2">{others.map(agent => <button type="button" key={agent.id} onClick={() => ask(`Help me contact ${JSON.stringify(name(agent))} (target ${JSON.stringify(agent.id)}). List its available agents or functions and ask what I need.`)} className="block w-full rounded-md border border-border p-3 text-left hover:bg-muted"><span className="block font-medium">{name(agent)}</span>{(agent.purpose || agent.description) && <span className="block text-xs text-muted-foreground">{agent.purpose || agent.description}</span>}</button>)}</div></details>}
    </>}
    <button type="button" onClick={() => openProductWorkspace('bullpen')} className="inline-flex items-center gap-1.5 text-xs text-muted-foreground underline hover:text-foreground">Open full AgentBridge directory<ExternalLink aria-hidden="true" className="h-3 w-3" /></button>
  </div>
}
