import { ForkTeammateAgent } from './ForkTeammateAgent'
import { AgentDetailSection } from '../../components/chat/AgentDetailSection'
import { Avatar } from '../../components/ui/Avatar'
import { useEffect, useState, type ReactNode } from 'react'
import { openTerminalConversation } from './terminalConversationSelection'
import { ArrowLeft, ChevronDown, ChevronRight, X } from 'lucide-react'
import { Switch } from '../../components/ui/Switch'
import { Button } from '../../components/ui/Button'
import ModalPortal from '../../components/ui/ModalPortal'
import { useChatStore } from '../../stores/useChatStore'
import { fillMainDraft, fillOwnTabDraft } from '../../utils/agentPurposeDraft'
import { teamAgentChatTab } from './AgentSharingRow'
import { openTeammateAgent } from './openTeammateAgent'
import { AgentDetails, Saving, TeamTab, teamSettlingServer, type TeamGoOnline } from './TeamTab'
import { openCrewAccess } from './MacSharedAgents'
import { crewSharePage, setCrewAction, teamChanged, teamFailure, teamRequest, useTeamTab, type CrewShareAction, type CrewShareActionsGroup, type CrewShareAgent, type CrewSharePage, type TeamTarget } from './teamApi'

/** Opens the viewer's own Main with the words in its chat box, not sent; nothing is saved by this page. */
const toMain = (projectId: string, draft: string) => {
  if (!fillMainDraft(projectId, draft)) useChatStore.getState().addToast('Open one of your agents in this project, then ask from there.', 'error')
}

/**
 * The Team page (owner, 11 Nov: Share split into Team and Online; 12 Nov: Share agents moved into Team › Agents). It
 * prints the server's /share reply: the team option's title, line and status, the Team (the first-time wizard,
 * People, Activity, Sync), and the Agents tab: Ask and actions per agent. With the Team off there are no tabs, so the
 * agents follow People. The server writes every line and says what this person may change; the page prints it and
 * sends their change to the existing routes. The same page for the owner and every teammate.
 */
export function ShareCrew({ projectId, onBack, goOnline }: { projectId: string; onBack?: () => void; goOnline?: TeamGoOnline }) {
  const state = useTeamTab(projectId)
  const { target } = state
  const [page, setPage] = useState<CrewSharePage>()
  const [error, setError] = useState('')
  useEffect(() => {
    if (!target) return
    let active = true
    // A project that just went online may not answer at once (#3797): try again while it settles, then show the error.
    let timer: ReturnType<typeof setTimeout> | undefined
    const load = () => void crewSharePage(target).then(data => { if (active) { setPage(data); setError('') } }).catch(e => { if (!active) return; if (teamSettlingServer(projectId)) timer = setTimeout(load, 2000); else setError(teamFailure(e).message) })
    load()
    window.addEventListener('agentworks:team-updated', load)
    return () => { active = false; clearTimeout(timer); window.removeEventListener('agentworks:team-updated', load) }
  }, [target?.project, target?.connection]) // eslint-disable-line react-hooks/exhaustive-deps
  // The Mac app's own Crew before Mac online pairs it has no server copy yet: its Team switch.
  if (state.unbound) return <TeamTab projectId={projectId} goOnline={goOnline} onBack={onBack} />
  if (!page || !target) return error ? <p role="alert" className="text-xs text-muted-foreground">{error}</p> : <p role="status" className="text-sm text-muted-foreground">{teamSettlingServer(projectId) ? `Putting this project online on ${teamSettlingServer(projectId)}…` : 'Loading…'}</p>
  const option = page.options.find(item => item.key === 'team')
  if (!option) return null
  // With the Team on, the Team page starts with its tabs and the back arrow beside them, like every other side panel
  // page (owner, 11 Nov: "follow what we have everywhere"; YourComputerPanel, 4 Nov: no title row).
  const teamOn = !!state.view?.team.on
  const teamTabs = teamOn && !state.view?.new_team
  const agents = <ShareAgents page={page} target={target} projectId={projectId} onPeople={() => openCrewAccess(projectId, '', 'team', 'people')} />
  return <div className="space-y-3">
    {!teamTabs && <div className="flex items-start gap-2 px-1">
      {onBack && <button type="button" aria-label="Back to workspace" title="Back (Esc)" onClick={onBack} className="mt-0.5 grid h-7 w-7 shrink-0 place-items-center rounded-lg text-muted-foreground hover:bg-muted hover:text-foreground"><ArrowLeft className="h-4 w-4" aria-hidden="true" /></button>}
      <div className="min-w-0 flex-1">
        <h2 className="text-[17px] font-semibold">{option.title}</h2>
        <p className="text-[13px] text-muted-foreground">{option.line}</p>
      </div>
      <span className={`shrink-0 whitespace-nowrap pt-1 text-xs ${option.on ? 'text-emerald-600 dark:text-emerald-400' : 'text-muted-foreground'}`}>{option.status}</span>
    </div>}
    {error && <p role="alert" className="px-1 text-xs text-destructive">{error}</p>}
    <TeamTab projectId={projectId} goOnline={goOnline} onBack={teamTabs ? onBack : undefined} agents={agents} />
    {!teamOn && !state.view?.new_team && agents}
  </div>
}

/** Team › Agents: teammates first, then own sharing cards and actions, then "Who can use these". */
const copySize = (bytes: number) => bytes < 1 << 20 ? `${Math.max(1, Math.round(bytes / 1024))} KB` : `${(bytes / (1 << 20)).toFixed(bytes < 10 << 20 ? 1 : 0)} MB`

function ShareAgents({ page, target, projectId, onPeople }: { page: CrewSharePage; target: TeamTarget; projectId: string; onPeople: () => void }) {
  const [wizard, setWizard] = useState('')
  const shared = page.shared_with_you || []
  return <div className="space-y-2.5">
    {/* Teammates' agents (owner, 18 Nov mock, #3084): one group per person, then one line per agent: name · state,
        Watch only when its chat is shared live, Ask through the viewer's own agent. No switches, they are the teammate's. */}
    {shared.length > 0 && <>
      <p className="px-1 pt-2 text-xs font-medium text-muted-foreground">Teammates' agents</p>
      {shared.map(person => <div key={person.name} className="rounded-xl border border-border">
        <div className="flex items-center gap-2 border-b border-border px-3 py-2"><Avatar {...person} name={person.name} connection={target.connection} className="h-6 w-6 text-xs" /><span className="text-sm font-medium">{person.name}</span>{person.where && <span className="text-xs text-muted-foreground">· {person.where}</span>}</div>
        {person.items.map(item => {
          const watch = item.terminal_agent_id ? () => openTerminalConversation(projectId, target, { id: item.terminal_agent_id!, name: item.name }) : item.key ? () => openTeammateAgent(projectId, item.key!, item.owner_id) : undefined
          const ask = item.address ? () => toMain(projectId, `Ask ${person.name}'s ${item.name} (${item.address}): `) : undefined
          return <div key={person.name + item.name + item.line} className="flex items-center gap-2 px-3 py-1.5 text-sm hover:bg-muted/50">
            <span className="min-w-0 flex-1 truncate"><span className="font-medium">{item.name}</span> <span className={`text-xs ${item.working ? 'text-emerald-600 dark:text-emerald-400' : 'text-muted-foreground'}`}>· {item.line}</span>{!!item.copy_bytes && <span className="text-xs text-muted-foreground" title="Size of its conversation copy online"> · {copySize(item.copy_bytes)} online</span>}</span>
            {item.watch && watch && <button type="button" title={`Watch ${person.name}'s ${item.name}`} onClick={watch} className="shrink-0 text-xs text-muted-foreground hover:text-primary">Watch</button>}
            {item.agent_id && <ForkTeammateAgent target={target} agent={item.agent_id} name={item.name} label="Fork" />}
            {ask && <button type="button" title={`Ask ${person.name}'s ${item.name} from your agent`} onClick={ask} className="shrink-0 text-xs text-primary hover:underline">Ask</button>}
          </div>
        })}
      </div>)}
    </>}
    {page.agents.length > 0 && <div className="flex items-center gap-2 px-1"><p className="text-xs font-medium text-muted-foreground">Your agents</p>
      {page.agents_setup_ask && <button type="button" onClick={() => toMain(projectId, page.agents_setup_ask || '')} className="ml-auto text-xs text-primary hover:underline">✦ Ask AI to configure</button>}</div>}
    {page.agents.map(agent => <ShareAgentCard key={`${agent.team || ''}:${agent.key}`} agent={agent} target={target} projectId={projectId} />)}
    {page.agents_empty && <p className="text-xs text-muted-foreground">{page.agents_empty}</p>}
    {/* Actions run only on the hosting server; its reply decides visibility. */}
    <ShareActions group={page.actions_group} target={target} projectId={projectId} onWizard={setWizard} />
    <p className="px-1 text-xs text-muted-foreground">{page.who_can_use} · <button type="button" onClick={onPeople} className="text-primary hover:underline">change</button></p>
    {wizard && <ActionWizard name={wizard} projectId={projectId} onClose={() => setWizard('')} />}
  </div>
}

/** Compact header settings disclose in place; the server still owns each choice. */
function AgentSetting({ compact, name, label, value, children }: { compact: boolean; name: string; label: string; value?: string; children: ReactNode }) {
  const [open, setOpen] = useState(false)
  return <section aria-label={name} className="min-w-0">
    {compact ? <button type="button" aria-expanded={open} onClick={() => setOpen(!open)} className="flex w-full min-w-0 items-center gap-3 rounded-md py-2 text-left hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
      <span className="max-w-[45%] shrink-0 text-muted-foreground">{label}</span><span className="min-w-0 flex-1 truncate text-right">{value}</span>
      {open ? <ChevronDown aria-hidden className="h-3 w-3 shrink-0" /> : <ChevronRight aria-hidden className="h-3 w-3 shrink-0" />}
    </button> : <div className="mb-1.5 text-xs text-muted-foreground">{label}</div>}
    {(!compact || open) && <div className={compact ? 'space-y-2 rounded-lg bg-muted/30 p-2' : 'space-y-1.5'}>{children}</div>}
  </section>
}

export function ShareAgentCard({ agent, target, projectId, compact = false, defaultOpen = false, status }: { agent: CrewShareAgent; target: TeamTarget; projectId: string; compact?: boolean; defaultOpen?: boolean; status?: ReactNode }) {
  const team = useTeamTab(projectId)
  const { view } = team
  const [pending, setPending] = useState('')
  const [error, setError] = useState('')
  const [rules, setRules] = useState(false)
  const [noteOpen, setNoteOpen] = useState(false)
  const [note, setNote] = useState('')
  const [open, setOpen] = useState(false)
  // An online project's agent is shared in the Team the project is in: its changes go there, by that Team's key.
  const where = { project: agent.team || target.project, connection: target.connection }
  const key = agent.team_key || agent.key
  const run = async (key: string, save: () => Promise<unknown>) => {
    setPending(key); setError('')
    try { await save(); return true } catch (e) { setError(teamFailure(e).message); return false } finally { setPending('') }
  }
  const updated = teamChanged
  const share = (on: boolean) => run('ask', async () => { await teamRequest(where, '/agent-visibility', 'POST', { agent_key: key, in_team: on }); updated() })
  const rule = (key: string, change: { chat_share?: string; audience?: 'whole_team' | 'chosen'; chosen_people?: string[]; when_others_ask?: string }) => run(key, async () => { await teamRequest(where, '/agent-rule', 'POST', { agent_key: key, ...change }); updated() })
  const ask = agent.ask
  // The rule editor (When others ask) stays one click away, on the viewer's own agent.
  const row = agent.row || view?.agents.find(item => item.key === agent.key && item.mine)
  const busy = Boolean(pending)
  // One small card per agent (owner, 15 Nov mock): name and place with the switch on top, two compact choices, one hint
  // line that follows them. Off greys the choices and says who can still reach it.
  const chatChoice = ask?.chat_share_options.find(option => option.value === ask.chat_share)
  // Who may ask: the whole Team or chosen people (the server's audience). The picker lists current members, not you;
  // chosen_people is always the complete list, and Chosen with nobody means nobody else.
  const members = (agent.members || view?.people || []).filter(person => !person.you && person.note !== 'you' && person.note !== 'invited')
  const picked = (row?.chosen_people || []).map(person => person.user_id)
  const togglePerson = (id: string) => void rule('who', { audience: 'chosen', chosen_people: picked.includes(id) ? picked.filter(item => item !== id) : [...picked, id] })
  const explain=()=>{
   const tab=teamAgentChatTab(team,projectId,agent.key)
   if(!tab||!ask?.setup_ask||!fillOwnTabDraft(tab.tabId,ask.setup_ask))useChatStore.getState().addToast(`Open ${agent.name}'s own chat to explain these settings.`,'info')
  }
  const example=(short?:string,long?:string)=>short?<details className="text-[11px] text-muted-foreground"><summary title={long} className="cursor-pointer list-none">{short}</summary><p className="mt-1 rounded border border-border p-2">{long}</p></details>:null
  const segment = (on: boolean) => `max-w-full whitespace-normal px-2.5 py-1.5 text-left text-xs ${on ? 'bg-primary font-medium text-primary-foreground' : 'text-muted-foreground hover:bg-muted'}`
  // Team page (owner, 18 Nov mock, #3084): one line per agent with its switch; the settings open from the row, one line
  // each. The header keeps the summary visible and groups the controls under Sharing.
  const audienceLine = row?.audience === 'chosen' ? 'chosen people' : 'everyone'
  const rowLine = [agent.line, ask ? (ask.on ? audienceLine : 'not shared') : ''].filter(Boolean).join(' · ')
  const shareSwitch = ask && <span title={ask.fixed || undefined} className={`inline-flex items-center gap-2 text-xs text-muted-foreground ${compact ? 'w-full justify-between' : 'shrink-0'}`}>
    <Saving on={pending === 'ask'}>{null}</Saving>
    {ask.title}
    <Switch aria-label={ask.title} checked={ask.on} disabled={!ask.can_change || busy} onCheckedChange={on => void share(on)} />
  </span>
  const summary = ask?.summary && <p role="note" className={`${compact ? "mb-2 px-1" : "my-2"} text-xs leading-relaxed text-muted-foreground`}>{ask.summary}</p>
  const settings = <>
    {ask?.setup_ask&&<button type="button" onClick={explain} className="text-[11px] text-primary hover:underline">✦ Ask AI to explain</button>}
    {ask && (ask.note || (!ask.can_change && ask.fixed)) && <p className="mt-1.5 text-xs text-amber-600 dark:text-amber-400">{ask.note || ask.fixed}</p>}
    {ask?.can_edit && <div className={ask.on ? '' : 'pointer-events-none opacity-40'} aria-disabled={!ask.on || undefined}>
      <div className={"mt-2 min-w-0 divide-y divide-border/50"}>
        {row && <section aria-label="Teammate instructions" className={"min-w-0 space-y-1 py-2"}><div className="flex min-w-0 items-center gap-3"><span className="max-w-[45%] shrink-0 text-xs text-muted-foreground">Your instructions for teammates</span><span className="min-w-0 flex-1 truncate text-right" title={row.when_others_ask}>{row.when_others_ask || 'None'}</span><button type="button" disabled={busy || !ask.on} aria-expanded={noteOpen} className="text-primary hover:underline" onClick={() => { setNote(row.when_others_ask || ''); setNoteOpen(!noteOpen) }}>Edit</button></div>
          {noteOpen && <div><textarea aria-label="Your instructions for teammates" placeholder='e.g. "Project questions only; explain the risks before making changes."' disabled={busy || !ask.on} value={note} maxLength={1000} onChange={event => setNote(event.target.value)} className="w-full rounded border border-border bg-background p-2" /><button type="button" disabled={busy || !ask.on} className="text-primary hover:underline" onClick={() => void rule('note', { when_others_ask: note }).then(saved => { if (saved) setNoteOpen(false) })}>Save note</button></div>}
        </section>}
        {row?.audience && <AgentSetting compact name="Asking audience" label="Who can ask" value={row.audience === 'chosen' ? (row.chosen_people?.map(person => person.name).join(', ') || 'Only people I pick · nobody selected') : 'Everyone on the team'}>
          <span className="flex items-center gap-2">
            <span role="radiogroup" aria-label="Who can ask" className={`inline-flex min-w-0 max-w-full flex-wrap overflow-hidden rounded-lg border border-border`}>
              {([['whole_team', 'Everyone on the team'], ['chosen', 'Only people I pick']] as const).map(([value, label]) => <button key={value} type="button" role="radio" aria-checked={row.audience === value} disabled={busy || !ask.on}
                onClick={() => { if (row.audience !== value) void rule('who', value === 'chosen' ? { audience: 'chosen', chosen_people: picked } : { audience: 'whole_team' }) }}
                className={segment(row.audience === value)}>{label}</button>)}
            </span>
            <Saving on={pending === 'who'}>{null}</Saving>
          </span>
          {row.audience === 'chosen' && <>
            <span role="group" aria-label="Chosen people" className="flex flex-wrap items-center gap-1.5">
              {(row.chosen_people || []).map(person => <span key={person.user_id} className="inline-flex items-center gap-1 rounded-full border border-border bg-muted/60 px-2 py-0.5 text-xs">{person.name}
                <button type="button" aria-label={`Remove ${person.name}`} disabled={busy || !ask.on} onClick={() => togglePerson(person.user_id)} className="text-muted-foreground hover:text-foreground">✕</button></span>)}
              {members.some(person => !picked.includes(person.user_id))
                ? <select aria-label="Add person" value="" disabled={busy || !ask.on} onChange={event => { if (event.target.value) togglePerson(event.target.value) }}
                    className="rounded-full border border-dashed border-border bg-transparent px-2 py-0.5 text-xs text-primary">
                    <option value="">+ Add person</option>
                    {members.filter(person => !picked.includes(person.user_id)).map(person => <option key={person.user_id} value={person.user_id}>{person.name}</option>)}
                  </select>
                : members.length === 0 && <span className="text-xs text-muted-foreground">Nobody else is on the Team yet.</span>}
            </span>
          </>}
        </AgentSetting>}
        <AgentSetting compact name="Chat visibility" label={ask.chat_share_label} value={chatChoice?.label}>
        <span className="flex items-center gap-2">
          <span role="radiogroup" aria-label={ask.chat_share_label} className={`inline-flex min-w-0 max-w-full flex-wrap overflow-hidden rounded-lg border border-border`}>
            {ask.chat_share_options.map(option => <button title={option.line} key={option.value} type="button" role="radio" aria-checked={ask.chat_share === option.value} disabled={busy || !ask.on}
              onClick={() => { if (ask.chat_share !== option.value) void rule('see', { chat_share: option.value }) }}
              className={segment(ask.chat_share === option.value)}>{option.label}</button>)}
          </span>
          <Saving on={pending === 'see'}>{null}</Saving>
        </span>
        {example(chatChoice?.example,chatChoice?.line)}</AgentSetting>
      </div>
    </div>}
    {ask?.can_edit && row && <button type="button" aria-expanded={rules} className="mt-2 flex w-full items-center justify-between gap-2 text-xs" onClick={() => setRules(!rules)}><span className="text-muted-foreground">Uses</span><span className="min-w-0 truncate">{ask.uses_line || row.uses_note || 'No connections selected'}</span><span className="shrink-0 text-primary">{rules ? 'Done' : 'See all ›'}</span></button>}
    {ask?.on && rules && row && <div className="mt-2 border-t border-border pt-2"><AgentDetails agent={row} target={{ ...target, project: where.project }} projectId={projectId} showNote={false} /></div>}
    {status}
  </>
  const failure = error && <p role="alert" className="mt-1.5 text-xs text-destructive">{error}</p>
  if (compact) return <div id={`header-agent-card-${agent.team ? `${agent.team}-` : ''}${agent.key}`} className="text-xs">
    <AgentDetailSection title={agent.team_name ? `Sharing · ${agent.team_name}` : 'Sharing'} value={ask ? (ask.on ? `Shared · ${ask.chat_share === 'agent' ? 'Chat hidden' : 'Chat visible'}` : 'Not shared with the team') : undefined} summary={summary} defaultOpen={defaultOpen}>
      <div className="flex items-center gap-2.5">{shareSwitch}</div>
      {settings}
    </AgentDetailSection>
    {failure}
  </div>
  return <div id={`team-agent-card-${agent.key}`} className="rounded-xl border border-border bg-card px-3.5 py-2 text-sm">
    <div className="flex items-center gap-2.5">
      <button type="button" aria-expanded={open} onClick={() => setOpen(!open)} className="flex min-w-0 flex-1 items-center gap-2 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        {open ? <ChevronDown aria-hidden className="h-3.5 w-3.5 shrink-0 text-muted-foreground" /> : <ChevronRight aria-hidden className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />}
        <span className="shrink-0 truncate font-semibold">{agent.name}</span>
        {rowLine && <span title={agent.hint || undefined} className="min-w-0 truncate text-xs text-muted-foreground">{rowLine}</span>}
      </button>
      {shareSwitch}
    </div>
    {open && <div className="mt-1 min-w-0 pl-5 text-xs">{summary}{settings}</div>}
    {failure}
  </div>
}

/** The one Actions group: every action once, each with what it starts, its inputs, its switch and who can ask. */
function ShareActions({ group, target, projectId, onWizard }: { group: CrewShareActionsGroup; target: TeamTarget; projectId: string; onWizard: (name: string) => void }) {
  const [pending, setPending] = useState('')
  const [error, setError] = useState('')
  const turn = async (item: CrewShareAction, on: boolean) => {
    setPending(item.name); setError('')
    try { await setCrewAction(target, { name: item.name, on }) } catch (e) { setError(teamFailure(e).message) } finally { setPending('') }
  }
  if (group.hidden) return null
  return <div className="rounded-xl border border-border px-3.5 py-3 text-sm">
    <p className="text-[15px] font-semibold">{group.title}</p>
    <p className="text-xs text-muted-foreground">{group.line}</p>
    {group.rows.map(item => <div key={item.name} className="flex items-center gap-3 border-t border-border pt-2 mt-2">
      <span className="min-w-0 flex-1" title={item.line || undefined}>
        <span className="block font-medium">{item.title}</span>
        <span className="block text-xs text-muted-foreground">{item.starts}</span>
        {item.inputs.length > 0 && <span className="mt-0.5 flex flex-wrap gap-1">
          {item.inputs.map(input => <span key={input} className="rounded-full border border-border px-2 py-px text-[11px] text-muted-foreground">{input}</span>)}
        </span>}
        <span className="mt-0.5 block text-xs text-muted-foreground">{item.who}</span>
      </span>
      <span title={item.fixed || undefined} className="inline-flex shrink-0 items-center gap-1.5">
        <Saving on={pending === item.name}>{null}</Saving>
        <Switch aria-label={`${item.title}: on`} checked={item.on} disabled={!item.can_change || Boolean(pending)} onCheckedChange={on => void turn(item, on)} />
      </span>
    </div>)}
    {group.setup_ask && <button type="button" onClick={() => toMain(projectId, group.setup_ask || '')} className="mt-2 text-[13px] text-primary hover:underline">✦ Ask AI to set up an action</button>}
    {group.wizard_for && <button type="button" onClick={() => onWizard(group.wizard_for || '')} className="mt-2 text-[13px] text-primary hover:underline">+ Set up an action</button>}
    {error && <p role="alert" className="mt-1.5 text-xs text-destructive">{error}</p>}
  </div>
}

// The wizard's answers become the words of a draft in Main; the Crew's AI builds the action from them and shows it in
// chat before anything is saved. The page saves nothing.
const GETS_BACK = [
  { label: 'A short written answer', words: 'a short written answer' },
  { label: 'A file (report, sheet)', words: 'a file (report, sheet)' },
  { label: 'Just “done”, with a link to the run', words: 'just “done”, with a link to the run' },
]
const WHO_ASKS = [
  { label: 'Your team', words: 'my team' },
  { label: 'Other projects you choose', words: 'other projects I choose' },
  { label: 'Apps connected to you (Claude, ChatGPT)', words: 'apps connected to me (Claude, ChatGPT)' },
]

/** The first-time action wizard (owner, 9 Nov mock): what it should do, what people give it, what they get back and
 * who can ask; "Ask AI to build it" opens the Crew's Main with a draft built from the answers. */
function ActionWizard({ name, projectId, onClose }: { name: string; projectId: string; onClose: () => void }) {
  const [step, setStep] = useState(1)
  const [job, setJob] = useState('')
  const [details, setDetails] = useState<string[]>([''])
  const [back, setBack] = useState(0)
  const [who, setWho] = useState(0)
  useEffect(() => {
    const key = (event: KeyboardEvent) => { if (event.key === 'Escape') onClose() }
    document.addEventListener('keydown', key)
    return () => document.removeEventListener('keydown', key)
  }, [onClose])
  const finish = () => {
    const given = details.map(item => item.trim()).filter(Boolean)
    toMain(projectId, `Set up an action for ${name}: ${job.trim()}. People give: ${given.length ? given.join(', ') : 'nothing'}. They get back: ${GETS_BACK[back].words}. Who can ask: ${WHO_ASKS[who].words}. Show me the action before you save it.`)
    onClose()
  }
  const choice = (items: { label: string }[], chosen: number, choose: (index: number) => void, label: string) => <div role="radiogroup" aria-label={label} className="space-y-2">
    {items.map((item, index) => <button key={item.label} type="button" role="radio" aria-checked={chosen === index} onClick={() => choose(index)}
      className={`block w-full rounded-lg border px-3 py-2 text-left text-sm ${chosen === index ? 'border-primary bg-primary/10' : 'border-border hover:bg-muted'}`}>{item.label}</button>)}
  </div>
  return <ModalPortal>
    <div className="fixed inset-0 z-[10000] flex items-center justify-center bg-black/50 p-4" onClick={onClose}>
      <div role="dialog" aria-modal="true" aria-label={`Set up an action for ${name}`} onClick={event => event.stopPropagation()} className="w-full max-w-md overflow-hidden rounded-2xl border border-border bg-card text-sm shadow-md">
        <div className="flex items-center gap-2.5 border-b border-border px-4 py-3">
          <div aria-hidden="true" className="flex flex-1 gap-1.5">{[1, 2, 3, 4].map(n => <i key={n} className={`h-1 flex-1 rounded ${n <= step ? 'bg-primary' : 'bg-border'}`} />)}</div>
          <button type="button" aria-label="Close" onClick={onClose} className="rounded p-1 text-muted-foreground hover:bg-muted"><X className="h-4 w-4" /></button>
        </div>
        <div className="space-y-2 p-4">
          {step === 1 && <>
            <h3 className="text-base font-semibold">What should {name} do?</h3>
            <p className="text-[13px] text-muted-foreground">An action is one job people can ask for. Describe it in a sentence.</p>
            <input autoFocus aria-label="What it should do" value={job} onChange={event => setJob(event.target.value)} placeholder="Write a weekly summary of team feedback" className="w-full rounded-lg border border-border bg-background px-3 py-2 text-sm" />
            <p className="text-xs text-muted-foreground">Examples: “Review a pull request”, “Summarise this week's sales”, “Draft a reply to a customer”.</p>
          </>}
          {step === 2 && <>
            <h3 className="text-base font-semibold">What do people give it?</h3>
            <p className="text-[13px] text-muted-foreground">The details someone fills in each time. Leave empty if it needs nothing.</p>
            {details.map((item, index) => <input key={index} aria-label={`Detail ${index + 1}`} value={item} placeholder={index === 0 ? 'Week (for example 2026-W45)' : 'Another detail'}
              onChange={event => setDetails(details.map((old, at) => at === index ? event.target.value : old))} className="w-full rounded-lg border border-border bg-background px-3 py-2 text-sm" />)}
            <button type="button" onClick={() => setDetails([...details, ''])} className="w-full rounded-lg border border-dashed border-border px-3 py-2 text-left text-sm text-primary hover:bg-muted">+ Add another detail</button>
          </>}
          {step === 3 && <>
            <h3 className="text-base font-semibold">What do they get back?</h3>
            <p className="text-[13px] text-muted-foreground">What the person receives when it is done.</p>
            {choice(GETS_BACK, back, setBack, 'What they get back')}
          </>}
          {step === 4 && <>
            <h3 className="text-base font-semibold">Who can ask for it?</h3>
            <p className="text-[13px] text-muted-foreground">It runs as you, for this job only.</p>
            {choice(WHO_ASKS, who, setWho, 'Who can ask for it')}
            <p className="text-xs text-muted-foreground">Next, the project's AI builds the action from your answers. You see it in chat before anything is saved.</p>
          </>}
        </div>
        <div className="flex justify-end gap-2 border-t border-border px-4 py-3">
          <Button size="sm" variant="outline" disabled={step === 1} onClick={() => setStep(step - 1)}>Back</Button>
          {step < 4
            ? <Button size="sm" disabled={step === 1 && !job.trim()} onClick={() => setStep(step + 1)}>Next</Button>
            : <Button size="sm" onClick={finish}>Ask AI to build it</Button>}
        </div>
      </div>
    </div>
  </ModalPortal>
}


/** Header and right panel render the very same server-backed agent card. */
export function HeaderAgentSharing({ projectId, agentKey, status, defaultOpen = false }: { projectId: string; agentKey: string; status?: ReactNode; defaultOpen?: boolean }) {
  const { target } = useTeamTab(projectId)
  const scope = JSON.stringify([projectId, agentKey, target?.project, target?.connection])
  const [page, setPage] = useState<{ scope: string; value: CrewSharePage }>()
  const [error, setError] = useState('')
  useEffect(() => {
    setError('')
    if (!target) return
    let active = true
    const load = () => void crewSharePage(target).then(value => { if (active) { setPage({ scope, value }); setError('') } }).catch(e => { if (active) setError(teamFailure(e).message) })
    load(); window.addEventListener('agentworks:team-updated', load)
    return () => { active = false; window.removeEventListener('agentworks:team-updated', load) }
  }, [projectId, agentKey, target?.project, target?.connection])
  // An online project's agent has one card per Team that uses the project, all under the agent's own key.
  const agents = page?.scope === scope ? page.value.agents.filter(item => item.key === agentKey) : []
  return <>{target && agents.map((agent, i) => <ShareAgentCard key={`${agent.team || ''}:${agent.key}`} agent={agent} target={target} projectId={projectId} compact defaultOpen={defaultOpen} status={i === agents.length - 1 ? status : undefined} />)}{!agents.length && status}{target && page?.scope !== scope && !error && <p role="status" className="border-t border-border px-1 py-2 text-xs text-muted-foreground">Loading sharing…</p>}{error && <p role="alert" className="text-xs text-destructive">{error}</p>}</>
}
