import { useEffect, useState, type ReactNode } from 'react'
import { ArrowLeft, BarChart3, MessageSquare, Users, X, type LucideIcon } from 'lucide-react'
import { Switch } from '../../components/ui/Switch'
import { Button } from '../../components/ui/Button'
import ModalPortal from '../../components/ui/ModalPortal'
import { useChatStore } from '../../stores/useChatStore'
import { fillMainDraft } from '../../utils/agentPurposeDraft'
import { AgentDetails, Saving, TeamTab } from './TeamTab'
import { CrewDashboardsTab } from './CrewDashboardsTab'
import { crewSharePage, setCrewAction, teamFailure, teamRequest, useTeamTab, type CrewShareAgent, type CrewShareOption, type CrewShareOptionKey, type CrewSharePage, type TeamTarget } from './teamApi'

/** Opens the viewer's own Main with the words in its chat box, not sent; nothing is saved by this page. */
const toMain = (projectId: string, draft: string) => {
  if (!fillMainDraft(projectId, draft)) useChatStore.getState().addToast('Open your own Main tab in this Crew, then ask from there.', 'error')
}

const optionIcons: Record<CrewShareOptionKey, { icon: LucideIcon; tone: string }> = {
  team: { icon: Users, tone: 'bg-sky-500/15 text-sky-700 dark:text-sky-300' },
  agents: { icon: MessageSquare, tone: 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-300' },
  read_only: { icon: BarChart3, tone: 'bg-amber-500/15 text-amber-700 dark:text-amber-300' },
}

/**
 * Share this Crew (owner, 9 Nov, approved mock "Share a Crew"): one page with three options, each a card with the
 * server's one-line explanation, chips and status; a click opens its setup. Work on it with your team is the Team
 * (the first-time wizard, People and the folder line), Share agents is Ask and actions per agent, Read only is the
 * Crew's dashboards. The server's /share reply writes every line and says what this person may change; the page
 * prints it and sends their change to the existing routes. The same page for the owner and every teammate.
 */
export function ShareCrew({ projectId, onBack, onTurnOn, focus }: { projectId: string; onBack?: () => void; onTurnOn?: () => void; focus?: CrewShareOptionKey }) {
  const state = useTeamTab(projectId)
  const { target } = state
  const [page, setPage] = useState<CrewSharePage>()
  const [error, setError] = useState('')
  const [open, setOpen] = useState<Partial<Record<CrewShareOptionKey, boolean>>>({})
  useEffect(() => {
    if (!target) return
    let active = true
    const load = () => void crewSharePage(target).then(data => { if (active) { setPage(data); setError('') } }).catch(e => { if (active) setError(teamFailure(e).message) })
    load()
    window.addEventListener('agentworks:team-updated', load)
    return () => { active = false; window.removeEventListener('agentworks:team-updated', load) }
  }, [target?.project, target?.connection]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => { if (focus) setOpen(prev => ({ ...prev, [focus]: true })) }, [focus])
  // The Mac app's own Crew before Mac online pairs it has no server copy yet: its Team switch and its dashboards.
  if (state.unbound) return <div className="space-y-3"><TeamTab projectId={projectId} onTurnOn={onTurnOn} /><CrewDashboardsTab projectId={projectId} /></div>
  if (!page || !target) return error ? <p role="alert" className="text-xs text-muted-foreground">{error}</p> : <p role="status" className="text-sm text-muted-foreground">Loading…</p>
  const isOpen = (option: CrewShareOption) => open[option.key] ?? option.open
  return <div className="space-y-3">
    <div className="flex items-start gap-2 px-1">
      {onBack && <button type="button" aria-label="Back to workspace" title="Back (Esc)" onClick={onBack} className="mt-0.5 grid h-7 w-7 shrink-0 place-items-center rounded-lg text-muted-foreground hover:bg-muted hover:text-foreground"><ArrowLeft className="h-4 w-4" aria-hidden="true" /></button>}
      <div>
        <h2 className="text-[17px] font-semibold">{page.title}</h2>
        <p className="text-[13px] text-muted-foreground">{page.lead}</p>
      </div>
    </div>
    {error && <p role="alert" className="px-1 text-xs text-destructive">{error}</p>}
    {page.options.map(option => <ShareOption key={option.key} option={option} open={isOpen(option)} onToggle={() => setOpen(prev => ({ ...prev, [option.key]: !isOpen(option) }))}>
      {option.key === 'team' && <TeamTab projectId={projectId} onTurnOn={onTurnOn} />}
      {option.key === 'agents' && <ShareAgents page={page} target={target} projectId={projectId} onPeople={() => setOpen(prev => ({ ...prev, team: true }))} />}
      {option.key === 'read_only' && <CrewDashboardsTab projectId={projectId} embedded />}
    </ShareOption>)}
  </div>
}

function ShareOption({ option, open, onToggle, children }: { option: CrewShareOption; open: boolean; onToggle: () => void; children: ReactNode }) {
  const { icon: Icon, tone } = optionIcons[option.key] || optionIcons.team
  return <section aria-label={option.title} className="overflow-hidden rounded-2xl border border-border bg-card">
    <button type="button" aria-expanded={open} onClick={onToggle} className="flex w-full items-start gap-4 px-4 py-4 text-left hover:bg-muted/30">
      <span aria-hidden="true" className={`grid h-11 w-11 shrink-0 place-items-center rounded-xl ${tone}`}><Icon className="h-5 w-5" /></span>
      <span className="min-w-0 flex-1">
        <span className="block text-[15px] font-semibold">{option.title}</span>
        <span className="block text-[13px] text-muted-foreground">{option.line}</span>
        {option.chips.length > 0 && <span className="mt-2 flex flex-wrap gap-1.5">
          {option.chips.map(chip => <span key={chip} className="rounded-full border border-border px-2.5 py-px text-xs text-muted-foreground">{chip}</span>)}
        </span>}
      </span>
      <span className={`shrink-0 whitespace-nowrap pt-0.5 text-xs ${option.on ? 'text-emerald-600 dark:text-emerald-400' : 'text-muted-foreground'}`}>{option.status}</span>
    </button>
    {open && <div className="border-t border-border px-4 py-3.5">{children}</div>}
  </section>
}

/** Share agents: one card per agent (Ask and Actions), then "Who can use these". */
function ShareAgents({ page, target, projectId, onPeople }: { page: CrewSharePage; target: TeamTarget; projectId: string; onPeople: () => void }) {
  const [wizard, setWizard] = useState('')
  return <div className="space-y-2.5">
    {page.agents.map(agent => <ShareAgentCard key={agent.key} agent={agent} intro={page.agents_intro} target={target} projectId={projectId} onWizard={setWizard} />)}
    {page.agents_empty && <p className="text-xs text-muted-foreground">{page.agents_empty}</p>}
    <p className="px-1 text-xs text-muted-foreground">{page.who_can_use} · <button type="button" onClick={onPeople} className="text-primary hover:underline">change</button></p>
    {wizard && <ActionWizard name={wizard} projectId={projectId} onClose={() => setWizard('')} />}
  </div>
}

function ShareAgentCard({ agent, intro, target, projectId, onWizard }: { agent: CrewShareAgent; intro: string; target: TeamTarget; projectId: string; onWizard: (name: string) => void }) {
  const { view } = useTeamTab(projectId)
  const [pending, setPending] = useState('')
  const [error, setError] = useState('')
  const [rules, setRules] = useState(false)
  const where = { project: target.project, connection: target.connection }
  const run = async (key: string, save: () => Promise<unknown>) => {
    setPending(key); setError('')
    try { await save() } catch (e) { setError(teamFailure(e).message) } finally { setPending('') }
  }
  const updated = () => window.dispatchEvent(new CustomEvent('agentworks:team-updated'))
  const share = (on: boolean) => run('ask', async () => { await teamRequest(where, '/agent-visibility', 'POST', { agent_key: agent.key, in_team: on }); updated() })
  const rule = (key: string, change: { can_make_changes?: boolean; chat_share?: string }) => run(key, async () => { await teamRequest(where, '/agent-rule', 'POST', { agent_key: agent.key, ...change }); updated() })
  const turn = (name: string, on: boolean) => run(`action:${name}`, () => setCrewAction(target, { target: agent.target, name, on }))
  const ask = agent.ask
  // The rule editor (When others ask) stays one click away, on the viewer's own agent.
  const row = view?.agents.find(item => item.key === agent.key && item.mine)
  const busy = Boolean(pending)
  return <div className="rounded-xl border border-border px-3.5 py-3 text-sm">
    <p className="text-[15px] font-semibold">{agent.name}</p>
    {agent.line && <p className="text-xs text-muted-foreground">{agent.line}</p>}
    {ask && <div className="mt-2.5 border-t border-border pt-2.5">
      <div className="flex items-center gap-3">
        <span className="min-w-0 flex-1"><span className="block font-semibold">Ask</span><span className="block text-xs text-muted-foreground">People talk to it.</span></span>
        <span title={ask.fixed || undefined} className="inline-flex shrink-0 items-center gap-1.5">
          <Saving on={pending === 'ask'}>{null}</Saving>
          <Switch aria-label={`${agent.name}: Ask`} checked={ask.on} disabled={!ask.can_change || busy} onCheckedChange={on => void share(on)} />
        </span>
      </div>
      {(ask.note || (!ask.can_change && ask.fixed)) && <p className="mt-1 text-xs text-muted-foreground">{ask.note || ask.fixed}</p>}
      {ask.on && ask.can_edit && <>
        <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-2 text-xs">
          <span className="inline-flex items-center gap-2">
            <Switch aria-label={`${agent.name}: ${ask.changes_label}`} checked={ask.can_make_changes} disabled={busy} onCheckedChange={on => void rule('can', { can_make_changes: on })} />
            <span>{ask.changes_label}</span><Saving on={pending === 'can'}>{null}</Saving>
          </span>
          <span className="inline-flex items-center gap-2">
            <span className="text-muted-foreground">{ask.chat_share_label}</span>
            <span role="radiogroup" aria-label={`${agent.name}: ${ask.chat_share_label}`} className="flex overflow-hidden rounded-md border border-border">
              {ask.chat_share_options.map(option => <button key={option.value} type="button" role="radio" aria-checked={ask.chat_share === option.value} disabled={busy}
                onClick={() => { if (ask.chat_share !== option.value) void rule('see', { chat_share: option.value }) }}
                className={`px-2 py-0.5 ${ask.chat_share === option.value ? 'bg-primary font-medium text-primary-foreground' : 'text-muted-foreground hover:bg-muted'}`}>{option.label}</button>)}
            </span>
            <Saving on={pending === 'see'}>{null}</Saving>
          </span>
          {row && <button type="button" aria-expanded={rules} onClick={() => setRules(!rules)} className="text-primary hover:underline">{row.has_rule ? '1 rule…' : 'Rules…'}</button>}
        </div>
        {!ask.can_make_changes && <p className="mt-1 text-xs text-muted-foreground">{ask.changes_line}</p>}
        {rules && row && <AgentDetails agent={row} target={target} projectId={projectId} />}
      </>}
    </div>}
    <div className="mt-2.5 border-t border-border pt-2.5">
      <p className="font-semibold">Actions</p>
      <p className="text-xs text-muted-foreground">{agent.actions.length ? intro : agent.actions_line}</p>
      {agent.actions.map(item => <div key={item.name} className="flex items-center gap-3 pt-2">
        <span className="min-w-0 flex-1" title={item.line || undefined}>
          <span className="block font-medium">{item.title}</span>
          {item.inputs.length > 0 && <span className="mt-0.5 flex flex-wrap gap-1">
            {item.inputs.map(input => <span key={input} className="rounded-full border border-border px-2 py-px text-[11px] text-muted-foreground">{input}</span>)}
          </span>}
        </span>
        <span title={item.fixed || undefined} className="inline-flex shrink-0 items-center gap-1.5">
          <Saving on={pending === `action:${item.name}`}>{null}</Saving>
          <Switch aria-label={`${item.title}: on`} checked={item.on} disabled={!item.can_change || busy} onCheckedChange={on => void turn(item.name, on)} />
        </span>
      </div>)}
      {agent.connected_line && <p className="pt-2 text-xs text-muted-foreground">{agent.connected_line}</p>}
      {agent.setup_ask && <button type="button" onClick={() => toMain(projectId, agent.setup_ask || '')} className="mt-2 text-[13px] text-primary hover:underline">✦ Ask AI to set up an action</button>}
      {agent.wizard_for && <button type="button" onClick={() => onWizard(agent.wizard_for || '')} className="mt-2 text-[13px] text-primary hover:underline">+ Set up an action</button>}
    </div>
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
  { label: 'Other Crews you choose', words: 'other Crews I choose' },
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
            <p className="text-xs text-muted-foreground">Next, the Crew's AI builds the action from your answers. You see it in chat before anything is saved.</p>
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
