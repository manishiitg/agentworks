import { useEffect, useState } from 'react'
import { ChevronRight, Lock } from 'lucide-react'
import { liveFeed } from '../../services/liveFeed'
import { useChatStore } from '../../stores/useChatStore'
import { openAgentTab } from '../../utils/openAgentTab'
import { teamRequest, type TeamTarget } from './teamApi'

/** The Team's activity log as the server writes it (crew_team_activity_log.go): every line is the server's; the text of
 * a message is there only when both agents' chats are shared Live with the viewer. */
type ActivityEnd = { person: string; agent: string; you?: boolean; label: string; chat_key: string; watch_key?: string }
type ActivityMessage = { id: string; from: ActivityEnd; to: ActivityEnd; sent_at: string; status: 'answered' | 'waiting' | 'mac_off'; status_line: string; answered_at?: string; question?: string; answer?: string; hidden_line?: string }
type ActivityView = { now: { person: string; agent: string; you?: boolean; state: 'running' | ''; line: string }[]; messages: ActivityMessage[]; other: { kind: string; line: string }[]; foot: string }

const pill: Record<ActivityMessage['status'], string> = {
  answered: 'border-blue-500/25 bg-blue-500/10 text-blue-600 dark:text-blue-300',
  waiting: 'border-amber-500/30 bg-amber-500/10 text-amber-600 dark:text-amber-400',
  mac_off: 'border-border bg-muted text-muted-foreground',
}
const clock = (at: string) => new Date(at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })

/** The tab here of one Team agent, by its server key (a Mac app tab carries it after ":team:"). */
function tabFor(end: ActivityEnd) {
  const tabs = useChatStore.getState().chatTabs
  return Object.keys(tabs).find(id => {
    const key = tabs[id]?.metadata?.agentProfileConversationKey
    return key && (key === end.chat_key || key === end.watch_key || key.endsWith(`:team:${end.chat_key}`))
  })
}

/** Read again when the Team panel changes (a crew_panel notice or a Team change), like the Team tab itself. */
function useTeamActivity(target: TeamTarget, projectId: string) {
  const [view, setView] = useState<ActivityView>()
  useEffect(() => {
    let active = true
    const load = () => { void teamRequest<ActivityView>({ project: target.project, connection: target.connection }, '/activity').then(data => { if (active) setView(data) }).catch(() => {}) }
    load()
    window.addEventListener('agentworks:team-updated', load)
    const unlive = liveFeed.subscribe(['crew_panel'], null, load, projectId)
    return () => { active = false; window.removeEventListener('agentworks:team-updated', load); unlive() }
  }, [target.project, target.connection, projectId])
  return view
}

export function TeamActivity({ target, projectId }: { target: TeamTarget; projectId: string }) {
  const view = useTeamActivity(target, projectId)
  const [open, setOpen] = useState('')
  if (!view) return null
  const heading = 'mb-1.5 mt-1 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground'
  return <section aria-label="Activity" className="space-y-2.5 rounded-xl border border-border px-4 py-3 text-sm">
    <p className={heading}>Now</p>
    <div className="flex flex-wrap gap-2">
      {view.now.map((agent, i) => <span key={i} className="flex items-center gap-2 rounded-lg border border-border px-2.5 py-1 text-[13px]">
        <span aria-hidden="true" className={`h-2 w-2 shrink-0 rounded-full ${agent.state === 'running' ? 'animate-pulse bg-emerald-500' : 'bg-muted-foreground/40'}`} />
        <span className="font-medium">{agent.you ? 'You' : agent.person} · {agent.agent}</span>
        <span className={agent.state === 'running' ? 'text-emerald-600 dark:text-emerald-400' : 'text-muted-foreground'}>{agent.line}</span>
      </span>)}
    </div>
    <p className={heading}>Agent messages</p>
    {view.messages.length === 0 ? <p className="text-xs text-muted-foreground">No messages between the Team's agents yet.</p>
      : <div className="divide-y divide-border overflow-hidden rounded-lg border border-border">
        {view.messages.map(message => {
          const text = Boolean(message.question)
          const isOpen = text && open === message.id
          const tab = isOpen ? tabFor(message.to) : undefined
          return <div key={message.id}>
            <button type="button" disabled={!text} aria-expanded={text ? isOpen : undefined} onClick={() => setOpen(isOpen ? '' : message.id)}
              className="grid w-full grid-cols-[14px_1fr_auto_auto] items-center gap-2.5 px-3 py-2 text-left enabled:hover:bg-muted/50 max-sm:grid-cols-[14px_1fr_auto]">
              <ChevronRight aria-hidden="true" className={`h-3 w-3 text-muted-foreground transition-transform ${text ? '' : 'invisible'} ${isOpen ? 'rotate-90' : ''}`} />
              <span className={`min-w-0 truncate ${message.status === 'mac_off' ? 'text-muted-foreground' : ''}`}>{message.from.label}<span className="mx-1 text-muted-foreground">→</span>{message.to.label}<span className="ml-1.5 text-muted-foreground">asked</span></span>
              <span className={`whitespace-nowrap rounded-full border px-2 py-0.5 text-[11.5px] ${pill[message.status]}`}>{message.status_line}</span>
              <span className="text-xs tabular-nums text-muted-foreground max-sm:col-start-2">{clock(message.sent_at)}</span>
            </button>
            {message.hidden_line && <p className="flex items-center gap-1.5 px-3 pb-2 pl-9 text-xs text-muted-foreground"><Lock aria-hidden="true" className="h-3 w-3 shrink-0" />{message.hidden_line}</p>}
            {isOpen && <div className="space-y-1.5 px-3 pb-3 pl-9 text-[13px]">
              <p className="max-w-[520px] whitespace-pre-wrap rounded-lg border border-blue-500/30 bg-blue-500/10 px-2.5 py-1.5"><span className="block text-[11px] text-muted-foreground">{message.from.label} asked</span>{message.question}</p>
              {message.answer && <p className="ml-10 max-w-[520px] whitespace-pre-wrap rounded-lg border border-border bg-muted/40 px-2.5 py-1.5 max-sm:ml-4"><span className="block text-[11px] text-muted-foreground">{message.to.label} answered</span>{message.answer}</p>}
              {tab && <button type="button" onClick={() => openAgentTab(tab)} className="text-[12.5px] text-primary hover:underline">Open {message.to.label} →</button>}
            </div>}
          </div>
        })}
      </div>}
    {view.other.length > 0 && <>
      <p className={heading}>Other</p>
      <div className="divide-y divide-border rounded-lg border border-border">
        {view.other.map((item, i) => <p key={i} className="px-3 py-2 text-[13px]"><span className="mr-1.5 rounded bg-destructive/10 px-1.5 py-px text-[11px] text-destructive">{item.kind === 'clash' ? 'Clash' : 'Team'}</span>{item.line}</p>)}
      </div>
    </>}
    <p className="text-xs text-muted-foreground">{view.foot}</p>
  </section>
}
