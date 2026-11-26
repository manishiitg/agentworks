import { MarkdownRenderer } from '../../components/ui/MarkdownRenderer'
import { useEffect, useRef, useState } from 'react'
import { ArrowLeft, Lock } from 'lucide-react'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { TeammateAsk } from './TeamAgentAsk'
import { ForkTeammateAgent } from './ForkTeammateAgent'
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
  const line = selection.kind === 'app'
    ? `${provider ? `${provider} agent` : 'Agent'} on ${place}`
    : `Native agent${provider ? ` · ${provider}` : ''} running in ${selection.owner ? 'your' : 'a'} terminal on ${place}`
  const hidden = !selection.owner && (selection.kind === 'app' || agent?.chat_share === 'agent' || reachable?.chat_share === 'agent' || !!agent?.chat_private_line)
  const privateLine = agent?.chat_private_line || `${person.split('@')[0]} keeps this agent's chat private. You can still ask it.`
  return { team, agent, reachable, line, state, hidden, privateLine, place }
}

/** The same one header line as a teammate's app agent (owner, 20 Nov): back, the agent, where and its state, Fork as an icon. */
export function TerminalConversationHeader({ selection, close }: { selection: TerminalConversationSelection; close: () => void }) {
  const { agent, state, hidden, place, line } = useNativeIdentity(selection)
  const name = agent?.name || selection.agent.name
  return <div data-agent-header className="flex min-w-0 flex-1 items-center gap-2 text-sm">
    <button type="button" aria-label="Back to app chat" onClick={close} className="shrink-0 rounded p-1 text-muted-foreground hover:bg-muted"><ArrowLeft className="h-4 w-4" /></button>
    <span className="min-w-0 shrink truncate font-semibold" title={line}>{name}</span>
    <span className="min-w-0 truncate text-xs text-muted-foreground">{place}{state && <> · <span className={state === 'Working' ? 'text-emerald-600 dark:text-emerald-400' : undefined}>{state}</span></>}{hidden && <> · <Lock aria-label="Chat hidden" className="inline h-3 w-3" /> Chat hidden</>}</span>
    {!selection.owner && <ForkTeammateAgent compact target={selection.target} agent={selection.agent.id} name={name} />}
  </div>
}

/** Conversation above, one Ask box below; a hidden chat keeps the same layout with a private note. */
export function TerminalConversation({ selection }: { selection: TerminalConversationSelection }) {
  const { team: { view, error }, agent, reachable, hidden, privateLine, place } = useNativeIdentity(selection)
  const name = agent?.name || selection.agent.name
  const address = reachable?.address || agent?.address || (selection.owner ? selection.agent.address : undefined)
  const enabled = selection.owner ? !!address : !!view?.team.on && !!reachable && !error
  // The chat lives on its Mac: when that Mac is offline there is nothing to read, so say so instead of a failed load.
  const online = reachable?.online ?? agent?.online
  const offline = !selection.owner && online === false
  return <section aria-label={`${selection.agent.name} conversation`} className="flex min-h-0 flex-1 flex-col">
    {hidden ? <p className="flex min-h-0 flex-1 items-center justify-center px-6 py-8 text-center text-sm text-muted-foreground"><span><Lock aria-hidden="true" className="mr-1 inline h-3.5 w-3.5" />{privateLine}</span></p>
      : offline ? <p role="status" className="flex min-h-0 flex-1 items-center justify-center px-6 py-8 text-center text-sm text-muted-foreground">{place} is offline, so this chat can't be shown right now. It shows here again when it's back.</p>
      : <TerminalTranscript target={selection.target} agent={selection.agent} />}
    <TeammateAsk target={selection.target} address={address} name={name} where={place} online={online} enabled={enabled} agent={{cli:reachable?.cli||agent?.cli,engine:reachable?.engine||agent?.engine,model_id:reachable?.model_id||agent?.model_id,reasoning_effort:reachable?.reasoning_effort||agent?.reasoning_effort}} scope={`${selection.identity}:${selection.target.connection}:${selection.target.project}:${selection.agent.id}`} />
  </section>
}
