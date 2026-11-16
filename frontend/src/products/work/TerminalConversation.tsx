import { MarkdownRenderer } from '../../components/ui/MarkdownRenderer'
import { useEffect, useRef, useState } from 'react'
import { ArrowLeft, Terminal } from 'lucide-react'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { AskMyAgentAction } from './TeamAgentAsk'
import { teamRequest, teamFailure, useTeamTab, type TeamTarget } from './teamApi'
import type { TerminalConversationSelection } from './terminalConversationSelection'

type Message = { native_message_id?: string; Role: string; Parts: { Text: string }[]; tool_id?: string; tool_name?: string; tool_phase?: 'call' | 'result' }
const LOAD_TIMEOUT_MS = 25_000

/** The native display projection is already admitted/redacted by the server.
 * Markdown is untrusted, and tool records are closed disclosures, never actions. */
function NativeMessages({ messages, name }: { messages: Message[]; name: string }) {
  const rows: { message: Message; result?: Message }[] = []
  const tools = new Map<string, typeof rows[number]>()
  for (const message of messages) {
    if (message.Role === 'tool' && message.tool_id) {
      const previous = tools.get(message.tool_id)
      if (message.tool_phase === 'result' && previous) { previous.result = message; continue }
      const row = { message }; rows.push(row); tools.set(message.tool_id, row)
    } else rows.push({ message })
  }
  const text = (message: Message) => message.Parts.map(part => part.Text).join('\n\n')
  return <div className="mx-auto max-w-3xl space-y-6">{rows.map(({ message, result }, index) => message.Role === 'tool'
    ? <details key={message.native_message_id || index} className="rounded-xl border border-border bg-muted/30 px-4 py-3 text-xs">
        <summary className="cursor-pointer font-medium">{message.tool_name || 'Tool'} · {result || message.tool_phase === 'result' ? 'Result' : 'Call'}</summary>
        <pre className="mt-3 overflow-x-auto whitespace-pre-wrap break-words text-muted-foreground">{text(message)}{result ? `\n\n${text(result)}` : ''}</pre>
      </details>
    : <article key={message.native_message_id || index} aria-label={message.Role === 'human' || message.Role === 'user' ? 'Your message' : `${name} message`} className={message.Role === 'human' || message.Role === 'user' ? 'ml-auto max-w-[90%] rounded-2xl bg-muted px-4 py-3' : 'px-1'}>
        <p className="mb-2 text-xs font-medium text-muted-foreground">{message.Role === 'human' || message.Role === 'user' ? 'Person' : name}</p>
        <MarkdownRenderer content={text(message)} untrustedContent disablePathLinking copyableCode />
      </article>)}</div>
}

/** A failed or timed-out read drops private text; all callbacks are identity fenced. */
export function TerminalTranscript({ target, agent }: { target: TeamTarget; agent: { id: string; name: string } }) {
  const scope = JSON.stringify([captureChatIdentity(), target.project, target.connection, agent.id])
  const [state, setState] = useState<{ scope: string; messages: Message[]; error?: string }>()
  const retry = useRef(() => {})
  useEffect(() => {
    let active = true, reading = false
    let timer: ReturnType<typeof setTimeout> | undefined
    let request: AbortController | undefined
    const identity = captureChatIdentity()
    const load = async () => {
      if (!active || reading || document.visibilityState !== 'visible') return
      reading = true
      request = new AbortController()
      const controller = request, started = performance.now()
      const timeout = setTimeout(() => controller.abort(), LOAD_TIMEOUT_MS)
      const log = (outcome: string, reason?: string) => console.info('[NATIVE_CONVERSATION]', { project: target.project, agent: agent.id, outcome, ...(reason ? { reason } : {}), ms: Math.round(performance.now() - started) })
      log('load_started')
      try {
        const data = await teamRequest<{ messages: Message[] }>(target, `/terminal/${encodeURIComponent(agent.id)}/live`, 'GET', undefined, false, controller.signal)
        if (!active || !isChatIdentityCurrent(identity)) { log('skipped', 'scope_changed'); return }
        if (!Array.isArray(data?.messages) || data.messages.some(message => typeof message.Role !== 'string' || !Array.isArray(message.Parts) || message.Parts.some(part => typeof part.Text !== 'string'))) {
          setState({ scope, messages: [], error: 'The native conversation could not be read. Try again.' }); log('failed', 'invalid_response'); return
        }
        setState({ scope, messages: data.messages }); log('loaded')
      } catch (e) {
        if (active && isChatIdentityCurrent(identity)) {
          const timedOut = controller.signal.aborted
          setState({ scope, messages: [], error: timedOut ? 'The terminal did not answer in time. Keep AgentWorks open on its Mac, then try again.' : teamFailure(e).message })
          log('failed', timedOut ? 'timeout' : 'request_failed')
        }
      } finally {
        clearTimeout(timeout); reading = false
        if (active && document.visibilityState === 'visible') timer = setTimeout(() => void load(), 5000)
      }
    }
    const wake = () => { clearTimeout(timer); void load() }
    retry.current = wake
    document.addEventListener('visibilitychange', wake)
    void load()
    return () => { active = false; clearTimeout(timer); request?.abort(); retry.current = () => {}; document.removeEventListener('visibilitychange', wake) }
  }, [scope]) // eslint-disable-line react-hooks/exhaustive-deps
  const current = state?.scope === scope ? state : undefined
  return <div data-terminal-transcript className="min-h-0 flex-1 overflow-y-auto px-4 py-6 text-sm">
    {current?.error ? <div><p role="alert" className="text-muted-foreground">{current.error}</p><button type="button" onClick={() => retry.current()} className="mt-2 text-primary hover:underline">Try again</button></div> : !current ? <p role="status">Loading conversation…</p> : current.messages.length === 0 ? <p className="text-muted-foreground">No messages yet.</p> : <NativeMessages messages={current.messages} name={agent.name} />}
  </div>
}

function useNativeIdentity(selection: TerminalConversationSelection) {
  const team = useTeamTab(selection.projectId)
  const agent = team.view?.agents.find(row => row.id === selection.agent.id)
  const reachable = team.view?.reachable?.find(row => row.agent_id === selection.agent.id)
  const engine = agent?.engine || reachable?.engine || selection.agent.provider || ''
  const cli = agent?.cli || reachable?.cli || ''
  const provider = /codex/i.test(engine + cli) ? (/desktop/i.test(cli) ? 'Codex desktop' : 'Codex') : /claude/i.test(engine + cli) ? 'Claude Code' : selection.agent.provider_name || ''
  const person = agent?.owner_name || reachable?.person || 'your teammate'
  const place = selection.owner ? 'this Mac' : `${person.split('@')[0]}'s Mac`
  const online = reachable?.online ?? agent?.online
  const state = agent?.activity === 'running' || reachable?.activity === 'running' ? 'Working' : online === true ? 'Idle' : online === false ? 'Offline' : ''
  return { team, agent, reachable, line: `Native agent${provider ? ` · ${provider}` : ''} running in ${selection.owner ? 'your' : 'a'} terminal on ${place}`, state }
}

export function TerminalConversationHeader({ selection, close }: { selection: TerminalConversationSelection; close: () => void }) {
  const { agent, state } = useNativeIdentity(selection)
  return <div data-agent-header className="flex min-w-0 items-center gap-2 text-sm">
    <button type="button" aria-label="Back to app chat" onClick={close} className="rounded p-1 text-muted-foreground hover:bg-muted"><ArrowLeft className="h-4 w-4" /></button>
    <Terminal aria-hidden="true" className="h-4 w-4 shrink-0 text-muted-foreground" />
    <span className="truncate font-semibold">{agent?.name || selection.agent.name}</span>
    <span className="truncate rounded-full border border-border px-2 py-0.5 text-xs text-muted-foreground">Terminal · {selection.owner ? 'this Mac' : 'their Mac'}</span>
    <span className="shrink-0 text-xs text-muted-foreground">{state}</span>
  </div>
}

export function TerminalConversation({ selection }: { selection: TerminalConversationSelection }) {
  const { team: { view, error }, agent, reachable, line, state } = useNativeIdentity(selection)
  return <section aria-label={`${selection.agent.name} conversation`} className="flex min-h-0 flex-1 flex-col">
    <div role="note" className="flex shrink-0 flex-wrap items-center justify-between gap-2 border-b border-border bg-muted/40 px-4 py-3 text-sm"><span>{line}</span><span className="text-xs font-medium text-muted-foreground">{state}</span></div>
    <AskMyAgentAction project={selection.projectId} person={agent?.owner_name || (selection.owner ? 'you' : reachable?.person || 'your teammate')} name={agent?.name || selection.agent.name} address={reachable?.address || agent?.address || (selection.owner ? selection.agent.address : undefined)} enabled={selection.owner ? !!(reachable?.address || agent?.address || selection.agent.address) : !!view?.team.on && !!reachable && !error} replyInTerminal={selection.owner} />
    {!selection.owner && (agent?.chat_share === 'agent' || agent?.chat_private_line) ? <p className="min-h-0 flex-1 px-4 py-8 text-sm text-muted-foreground">{agent.chat_private_line || "This agent's chat isn't shared"}</p> : <TerminalTranscript target={selection.target} agent={selection.agent} />}

  </section>
}
