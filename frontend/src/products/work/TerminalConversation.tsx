import { useEffect, useState } from 'react'
import { ArrowLeft, Terminal } from 'lucide-react'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { AskMyAgentAction } from './TeamAgentAsk'
import { teamRequest, teamFailure, useTeamTab, type TeamTarget } from './teamApi'
import type { TerminalConversationSelection } from './terminalConversationSelection'

type Message = { Role: string; Parts: { Text: string }[] }

/** The same server-admitted read-only native transcript for owners and teammates.
 * A failed read removes private text; nothing is inserted into writable app tabs. */
export function TerminalTranscript({ target, agent }: { target: TeamTarget; agent: { id: string; name: string } }) {
  const scope = JSON.stringify([captureChatIdentity(), target.project, target.connection, agent.id])
  const [state, setState] = useState<{ scope: string; messages: Message[]; error?: string }>()
  useEffect(() => {
    let active = true
    let timer: ReturnType<typeof setTimeout> | undefined
    const identity = captureChatIdentity()
    const load = async () => {
      if (document.visibilityState !== 'visible') {
        if (active) timer = setTimeout(() => void load(), 5000)
        return
      }
      try {
        const data = await teamRequest<{ messages: Message[] }>(target, `/terminal/${encodeURIComponent(agent.id)}/live`)
        if (active && isChatIdentityCurrent(identity)) setState({ scope, messages: data.messages })
      } catch (e) {
        if (active && isChatIdentityCurrent(identity)) setState({ scope, messages: [], error: teamFailure(e).message })
      } finally { if (active) timer = setTimeout(() => void load(), 5000) }
    }
    void load()
    return () => { active = false; clearTimeout(timer) }
  }, [scope]) // eslint-disable-line react-hooks/exhaustive-deps
  const current = state?.scope === scope ? state : undefined
  return <div data-terminal-transcript className="min-h-0 flex-1 overflow-y-auto px-4 py-6 text-sm">
    {current?.error ? <p role="alert" className="text-muted-foreground">{current.error}</p> : !current ? <p role="status">Loading…</p> : current.messages.length === 0 ? <p className="text-muted-foreground">No messages yet.</p> : <div className="mx-auto max-w-3xl space-y-6">{current.messages.map((message, index) => <article key={index}><p className="mb-1 text-xs text-muted-foreground">{message.Role === 'user' ? 'Person' : agent.name}</p><p className="whitespace-pre-wrap break-words">{message.Parts.map(part => part.Text).join('\n\n')}</p></article>)}</div>}
  </div>
}

export function TerminalConversationHeader({ selection, close }: { selection: TerminalConversationSelection; close: () => void }) {
  const { view } = useTeamTab(selection.projectId)
  const agent = view?.agents.find(row => row.id === selection.agent.id)
  const reachable = view?.reachable?.find(row => row.agent_id === selection.agent.id)
  const working = agent?.activity === 'running' || reachable?.activity === 'running'
  const online = reachable?.online ?? agent?.online
  return <div data-agent-header className="flex min-w-0 items-center gap-2 text-sm">
    <button type="button" aria-label="Back to app chat" onClick={close} className="rounded p-1 text-muted-foreground hover:bg-muted"><ArrowLeft className="h-4 w-4" /></button>
    <Terminal aria-hidden="true" className="h-4 w-4 shrink-0 text-muted-foreground" />
    <span className="truncate font-semibold">{agent?.name || selection.agent.name}</span>
    <span className="truncate rounded-full border border-border px-2 py-0.5 text-xs text-muted-foreground">Terminal · {selection.owner ? 'this Mac' : 'their Mac'}</span>
    <span className="shrink-0 text-xs text-muted-foreground">{working ? 'Working' : online === false ? 'Offline' : online === true ? 'Idle' : ''}</span>
  </div>
}

export function TerminalConversation({ selection }: { selection: TerminalConversationSelection }) {
  const { view, error } = useTeamTab(selection.projectId)
  const agent = view?.agents.find(row => row.id === selection.agent.id)
  const reachable = view?.reachable?.find(row => row.agent_id === selection.agent.id)
  return <section aria-label={`${selection.agent.name} conversation`} className="flex min-h-0 flex-1 flex-col">
    {!selection.owner && (agent?.chat_share === 'agent' || agent?.chat_private_line) ? <p className="min-h-0 flex-1 px-4 py-8 text-sm text-muted-foreground">{agent.chat_private_line || "This agent's chat isn't shared"}</p> : <TerminalTranscript target={selection.target} agent={selection.agent} />}
    {selection.owner ? <p role="note" className="shrink-0 border-t border-border px-4 py-3 text-sm text-muted-foreground">This chat runs in your terminal. Reply there.</p>
      : <AskMyAgentAction project={selection.projectId} person={agent?.owner_name || reachable?.person || 'your teammate'} name={agent?.name || selection.agent.name} address={reachable?.address} enabled={!!view?.team.on && !!reachable && !error} />}
  </section>
}
