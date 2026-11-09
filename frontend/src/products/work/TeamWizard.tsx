import { useEffect, useState, type ReactNode } from 'react'
import { Loader2 } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { finishTeamWizard, teamFailure, teamWizard, type CrewPeopleLevelKey, type TeamTarget, type TeamWizardView } from './teamApi'

const STEPS = 4

/** One option row of the wizard: a radio or a check, its title and line, and the control on its right. */
function Option({ kind, on, title, line, disabled, onClick, aside }: { kind: 'radio' | 'check'; on: boolean; title: string; line?: string; disabled?: boolean; onClick: () => void; aside?: ReactNode }) {
  return <div className={`flex items-center gap-2.5 rounded-lg border px-3 py-2.5 ${on ? 'border-primary bg-primary/5' : 'border-border'} ${disabled ? 'opacity-60' : ''}`}>
    <button type="button" role={kind === 'radio' ? 'radio' : 'checkbox'} aria-checked={on} aria-label={title} disabled={disabled} onClick={onClick} className="flex min-w-0 flex-1 items-center gap-2.5 text-left">
      <span aria-hidden="true" className={`grid h-4 w-4 shrink-0 place-items-center border-[1.5px] text-[10px] leading-none ${kind === 'radio' ? 'rounded-full' : 'rounded'} ${on ? 'border-primary bg-primary text-primary-foreground' : 'border-muted-foreground'}`}>{on && kind === 'check' ? '✓' : ''}</span>
      <span className="min-w-0"><span className="block truncate font-medium">{title}</span>{line && <span className="block text-xs text-muted-foreground">{line}</span>}</span>
    </button>
    {aside}
  </div>
}

/**
 * The first-time Team wizard (owner, 9 Nov, approved mock): shown instead of the empty Team tab when the server says the
 * Crew has no Team yet (new_team). Four steps, one question each. The server supplies every step's options (/setup);
 * this only renders them and sends the final choices at Finish. Nothing is saved before Finish; Back keeps the choices.
 */
export function TeamWizard({ target, onNo }: { target: TeamTarget; onNo: () => void }) {
  const [options, setOptions] = useState<TeamWizardView>()
  const [error, setError] = useState('')
  const [step, setStep] = useState(1)
  const [yes, setYes] = useState(true)
  const [picked, setPicked] = useState<Record<string, CrewPeopleLevelKey>>({})
  const [whole, setWhole] = useState(false)
  const [folder, setFolder] = useState('')
  const [agents, setAgents] = useState<string[]>([])
  const [saving, setSaving] = useState(false)
  const [needsDashboard, setNeedsDashboard] = useState(false)
  useEffect(() => {
    let active = true
    void teamWizard(target).then(data => { if (active) { setOptions(data); setFolder(data.folders.default) } }).catch(e => { if (active) setError(teamFailure(e).message) })
    return () => { active = false }
  }, [target.project, target.connection]) // eslint-disable-line react-hooks/exhaustive-deps
  if (!options) return error ? <p role="alert" className="text-xs text-destructive">{error}</p> : <p role="status" className="text-sm text-muted-foreground">Loading Team…</p>
  const available = options.levels.filter(level => level.available)
  const firstLevel = available.find(level => level.key === options.default_level)?.key || available[0]?.key
  const finish = async (createDashboard = false) => {
    setSaving(true); setError('')
    try {
      await finishTeamWizard(target, {
        create_dashboard: createDashboard,
        people: Object.entries(picked).map(([user_id, level]) => ({ user_id, level })), whole, folder,
        agents: options.agents.filter(agent => agent.main || agents.includes(agent.key)).map(agent => agent.key),
      })
    } catch (e) {
      const failure = teamFailure(e)
      setNeedsDashboard(failure.code === 'dashboard_needed'); setError(failure.message); setSaving(false)
    }
  }
  const next = () => { if (step === 1 && !yes) onNo(); else setStep(step + 1) }
  const heading = ['Share this Crew with a team?', 'Who is on the Team?', 'What do people work on?', 'Which agents can the Team ask?'][step - 1]
  const sub = ['A Team lets other people use this Crew with you. You can change it any time.', 'Choose people, then how each uses this Crew.', 'The part of the Crew people with “Works with me” can open.', 'Main answers by default. Add more if the Team should reach them.'][step - 1]
  return <section aria-label="Set up the Team" className="rounded-xl border border-border text-sm">
    <div className="flex items-center gap-2.5 border-b border-border px-4 py-3">
      <div className="flex flex-1 gap-1" aria-hidden="true">{Array.from({ length: STEPS }, (_, i) => <i key={i} className={`h-1 flex-1 rounded ${i < step ? 'bg-primary' : 'bg-border'}`} />)}</div>
      <span className="whitespace-nowrap text-xs text-muted-foreground">Step {step} of {STEPS}</span>
    </div>
    <div className="space-y-2 px-4 py-4">
      <h3 className="text-[15px] font-semibold">{heading}</h3>
      <p className="pb-1 text-xs text-muted-foreground">{sub}</p>
      {step === 1 && <div role="radiogroup" aria-label={heading} className="space-y-2">
        <Option kind="radio" on={yes} title="Yes, set up a Team" line="Pick the people, the folders and the agents next." onClick={() => setYes(true)} />
        <Option kind="radio" on={!yes} title="No, keep it only mine" line="Nobody else can use it. You can add a Team later." onClick={() => setYes(false)} />
      </div>}
      {step === 2 && <div className="space-y-2">
        {options.people.length === 0 && <p className="text-xs text-muted-foreground">There is nobody else on this server to add yet.</p>}
        {options.people.map(person => {
          const level = picked[person.user_id]
          return <Option key={person.user_id} kind="check" on={Boolean(level)} title={person.name} line={person.email}
            onClick={() => setPicked(current => { const { [person.user_id]: removed, ...rest } = current; return removed || !firstLevel ? rest : { ...rest, [person.user_id]: firstLevel } })}
            aside={level && <select aria-label={`${person.name}: how they use this Crew`} value={level} onChange={event => setPicked(current => ({ ...current, [person.user_id]: event.target.value as CrewPeopleLevelKey }))} className="shrink-0 rounded-md border border-border bg-background px-2 py-0.5 text-xs">
              {options.levels.map(item => <option key={item.key} value={item.key} disabled={!item.available} title={item.why || undefined}>{item.label}</option>)}
            </select>} />
        })}
      </div>}
      {step === 3 && <div role="radiogroup" aria-label={heading} className="space-y-2">
        <Option kind="radio" on={whole} title="The whole Crew" line="Every file and folder in it." onClick={() => setWhole(true)} />
        <Option kind="radio" on={!whole} title="Only a team folder" line="Just this folder, shared with the Team." onClick={() => setWhole(false)}
          aside={!whole && <select aria-label="Team folder" value={folder} onChange={event => setFolder(event.target.value)} className="shrink-0 rounded-md border border-border bg-background px-2 py-0.5 text-xs">
            {options.folders.folders.map(item => <option key={item} value={item}>{item}/</option>)}
          </select>} />
        {options.folders.offline && <p className="text-xs text-muted-foreground">{options.folders.offline}</p>}
      </div>}
      {step === 4 && <div className="space-y-2">
        {options.agents.map(agent => <Option key={agent.key} kind="check" on={agent.main || agents.includes(agent.key)} disabled={agent.main} title={agent.name}
          line={agent.main ? 'The default agent. Always on.' : 'Add so the Team can ask it too.'}
          onClick={() => setAgents(current => current.includes(agent.key) ? current.filter(key => key !== agent.key) : [...current, agent.key])}
          aside={agent.main && <span className="shrink-0 rounded-full border border-border px-2 py-px text-xs text-muted-foreground">Default</span>} />)}
      </div>}
      {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
    </div>
    <div className="flex justify-end gap-2 border-t border-border px-4 py-3">
      {step > 1 && <Button size="sm" variant="outline" disabled={saving} onClick={() => { setStep(step - 1); setNeedsDashboard(false); setError('') }}>Back</Button>}
      {step < STEPS
        ? <Button size="sm" onClick={next}>Continue</Button>
        : <Button size="sm" disabled={saving} onClick={() => void finish(needsDashboard)}>{saving && <Loader2 aria-label="Saving" className="animate-spin" />}{needsDashboard ? 'Create dashboard and finish' : 'Finish'}</Button>}
    </div>
  </section>
}
