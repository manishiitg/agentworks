import { useEffect, useRef, useState } from 'react'
import api from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { localFileError } from './codeLocalFiles'
import { openPublishedAgentChat } from './openPublishedAgentChat'

export type PublishedAgentChat = { conversation_key: string; conversation_id: string; session_id: string; name: string; provider?: string; model_id?: string; connection_id?: string }
type Agent = { agent_id: string; name: string }

/** Connections owns the reusable agent list; opened conversations remain ordinary private tabs. */
export function CrewPublishedAgents({ profileId, projectId }: { profileId: string; projectId: string }) {
  const [snapshot, setSnapshot] = useState<{ scope: string; identity: number; agents: Agent[] }>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [attempt, setAttempt] = useState(0)
  const owner = useAuthStore(state => state.user?.id)
  const connection = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const workspaceId = `${profileId}:${projectId}`
  const mounted = useRef(false)
  useEffect(() => {
    let disposed = false
    mounted.current = true
    const identity = captureChatIdentity()
    setBusy(true); setError('')
    void api.post<{ agents: Agent[] }>('/api/workspaces', { action: 'agents', workspace_id: workspaceId })
      .then(({ data }) => { if (!disposed && isChatIdentityCurrent(identity)) setSnapshot({ scope: workspaceId, identity, agents: data.agents }) })
      .catch(cause => { if (!disposed && isChatIdentityCurrent(identity)) setError(localFileError(cause)) })
      .finally(() => { if (!disposed && isChatIdentityCurrent(identity)) setBusy(false) })
    return () => { disposed = true; mounted.current = false }
  }, [workspaceId, owner, connection, attempt])
  const agents = snapshot?.scope === workspaceId && isChatIdentityCurrent(snapshot.identity) ? snapshot.agents : []
  const choose = async (agent: Agent) => {
    const identity = captureChatIdentity()
    setBusy(true); setError('')
    try {
      const { data } = await api.post<PublishedAgentChat>('/api/workspaces', { action: 'open_agent', workspace_id: workspaceId, agent_id: agent.agent_id })
      if (mounted.current && isChatIdentityCurrent(identity)) await openPublishedAgentChat(projectId, data)
    } catch (cause) { if (isChatIdentityCurrent(identity)) setError(localFileError(cause)) }
    finally { if (isChatIdentityCurrent(identity)) setBusy(false) }
  }
  const history = async (agent: Agent) => {
    const identity = captureChatIdentity()
    setBusy(true); setError('')
    try {
      const { data } = await api.get<Blob>('/api/external/v1/publication-history', { params: { workspace_id: workspaceId, agent_id: agent.agent_id }, responseType: 'blob' })
      if (!mounted.current || !isChatIdentityCurrent(identity)) return
      const url = URL.createObjectURL(data); const link = document.createElement('a')
      link.href = url; link.download = 'imported-chat.jsonl'; link.click(); window.setTimeout(() => URL.revokeObjectURL(url), 1000)
    } catch (cause) { if (isChatIdentityCurrent(identity)) setError(localFileError(cause)) }
    finally { if (isChatIdentityCurrent(identity)) setBusy(false) }
  }
  return <section aria-label="Chats from your computer" className="space-y-4 text-sm">
    <div className="space-y-2">
      <h3 className="text-xs font-medium text-muted-foreground">Brought in</h3>
      {busy && <p role="status" className="text-xs text-muted-foreground">Loading…</p>}
      {agents.map(agent => <div key={agent.agent_id} className="flex items-center gap-2 rounded-md border border-border p-2">
        <button type="button" className="min-w-0 flex-1 rounded px-2 py-1 text-left hover:bg-muted" disabled={busy} onClick={() => void choose(agent)}>{agent.name || 'Untitled agent'}</button>
        <button type="button" className="shrink-0 rounded px-2 py-1 text-xs text-muted-foreground hover:bg-muted" disabled={busy} onClick={() => void history(agent)} aria-label={`Download a copy of the chat for ${agent.name || 'Untitled agent'}`}>Download copy</button>
      </div>)}
      {!busy && !error && !agents.length && <p className="text-xs text-muted-foreground">None yet.</p>}
    </div>
    <div className="space-y-2 rounded-lg border border-border p-4">
      <h3 className="text-sm font-semibold">Bring a chat from your computer into this Crew</h3>
      <p className="text-xs text-muted-foreground">Started a chat in Claude Code, Codex or Cursor on your computer? Send it here and keep going.</p>
      <ol className="list-decimal space-y-1 pl-5 text-xs">
        <li>Open the AgentWorks app on your computer.</li>
        <li>Choose <strong>Publish to Crew</strong> and pick this Crew.</li>
        <li>Come back here. The chat shows up in the list above. Click it to continue.</li>
      </ol>
      <p className="text-[11px] text-muted-foreground">Developers can also run <code>agentworks publish</code> from the coding chat.</p>
    </div>
    {error && <p role="alert" className="text-xs text-destructive">{error} <button type="button" className="underline" disabled={busy} onClick={() => setAttempt(value => value + 1)}>Retry</button></p>}
  </section>
}
