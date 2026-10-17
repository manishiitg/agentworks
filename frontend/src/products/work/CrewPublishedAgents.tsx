import { useState } from 'react'
import api from '../../services/api'
import { localFileError } from './codeLocalFiles'

export type PublishedAgentChat = { conversation_key: string; conversation_id: string; session_id: string; name: string; provider?: string; model_id?: string; connection_id?: string }
type Agent = { agent_id: string; name: string }

/** Closing a conversation tab keeps the reusable published agent. */
export function CrewPublishedAgents({ profileId, projectId, onOpen }: { profileId: string; projectId: string; onOpen: (chat: PublishedAgentChat) => Promise<void> }) {
  const [open, setOpen] = useState(false)
  const [agents, setAgents] = useState<Agent[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const workspaceId = `${profileId}:${projectId}`
  const load = async () => {
    setOpen(true); setBusy(true); setError('')
    try { const { data } = await api.post<{ agents: Agent[] }>('/api/workspaces', { action: 'agents', workspace_id: workspaceId }); setAgents(data.agents) }
    catch (cause) { setError(localFileError(cause)) }
    finally { setBusy(false) }
  }
  const choose = async (agent: Agent) => {
    setBusy(true); setError('')
    try {
      const { data } = await api.post<PublishedAgentChat>('/api/workspaces', { action: 'open_agent', workspace_id: workspaceId, agent_id: agent.agent_id })
      await onOpen(data); setOpen(false)
    } catch (cause) { setError(localFileError(cause)) }
    finally { setBusy(false) }
  }
  const history = async (agent: Agent) => {
    setBusy(true); setError('')
    try {
      const { data } = await api.get<Blob>('/api/external/v1/publication-history', { params: { workspace_id: workspaceId, agent_id: agent.agent_id }, responseType: 'blob' })
      const url = URL.createObjectURL(data); const link = document.createElement('a')
      link.href = url; link.download = 'imported-chat.jsonl'; link.click(); window.setTimeout(() => URL.revokeObjectURL(url), 1000)
    } catch (cause) { setError(localFileError(cause)) }
    finally { setBusy(false) }
  }
  return <div className="relative shrink-0">
    <button type="button" className="rounded px-2 py-1 text-xs text-muted-foreground hover:bg-muted" aria-expanded={open} onClick={() => open ? setOpen(false) : void load()}>Agents</button>
    {open && <div className="absolute right-0 top-full z-50 mt-1 w-64 space-y-1 rounded border border-border bg-background p-2 shadow-lg">
      <p className="px-2 py-1 text-xs text-muted-foreground">Published agents · private Builder chats</p>
      {busy && <p role="status" className="px-2 text-xs">Loading…</p>}
      {agents.map(agent => <div key={agent.agent_id} className="flex items-center gap-1"><button type="button" className="min-w-0 flex-1 rounded px-2 py-1 text-left text-sm hover:bg-muted" disabled={busy} onClick={() => void choose(agent)}>{agent.name || 'Main'}</button><button type="button" className="shrink-0 rounded px-2 py-1 text-xs text-muted-foreground hover:bg-muted" disabled={busy} onClick={() => void history(agent)} aria-label={`Download imported chat for ${agent.name || 'Main'}`}>Saved chat</button></div>)}
      {!busy && !agents.length && <p className="px-2 text-xs text-muted-foreground">Publish from your computer to add an agent to this Crew.</p>}
      {error && <p role="alert" className="px-2 text-xs text-destructive">{error}</p>}
    </div>}
  </div>
}
