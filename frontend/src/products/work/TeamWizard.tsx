import { useEffect, useState, type ReactNode } from 'react'
import { Loader2 } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { finishTeamWizard, teamFailure, teamWizard, type CrewPeopleLevelKey, type TeamTarget, type TeamWizardView } from './teamApi'

const STEPS = 3

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
 * The first-time Team wizard (owner, 9 Nov; #3797 approved mockup; online projects are added from the server since #4087): shown instead of the
 * empty Team tab when the server says the Crew has no Team yet (new_team). Turning the Team on is the yes, so the steps
 * are Who, What they share, and Agents. The server supplies every step's options (/setup). Nothing is saved before
 * Finish; Back keeps the choices. offset counts the Where step a Mac project went through before this.
 */
export function TeamWizard({ target, onNo, offset = 0 }: { target: TeamTarget; onNo: () => void; offset?: number }) {
  const [options, setOptions] = useState<TeamWizardView>()
  const [error, setError] = useState('')
  const [step, setStep] = useState(1)
  const [picked, setPicked] = useState<Record<string, CrewPeopleLevelKey>>({})
  const [folder, setFolder] = useState('')
  const [typed, setTyped] = useState<string>()
  const [agents, setAgents] = useState<string[]>([])
  const [saving, setSaving] = useState(false)
  const [needsDashboard, setNeedsDashboard] = useState(false)
  useEffect(() => {
    let active = true
    void teamWizard(target).then(data => { if (active) { setOptions(data); setFolder(data.folders.default); setAgents(data.agents.filter(agent => agent.in_team).map(agent => agent.key)) } }).catch(e => { if (active) setError(teamFailure(e).message) })
    return () => { active = false }
  }, [target.project, target.connection]) // eslint-disable-line react-hooks/exhaustive-deps
  if (!options) return error ? <p role="alert" className="text-xs text-destructive">{error}</p> : <p role="status" className="text-sm text-muted-foreground">Loading Team…</p>
  const available = options.levels.filter(level => level.available)
  const firstLevel = available.find(level => level.key === options.default_level)?.key || available[0]?.key
  const shared = typed !== undefined ? typed.trim() : folder
  const finish = async (createDashboard = false) => {
    setSaving(true); setError('')
    try {
      await finishTeamWizard(target, {
        create_dashboard: createDashboard,
        people: Object.entries(picked).map(([user_id, level]) => ({ user_id, level })), folder: shared,
        agents: options.agents.filter(agent => agents.includes(agent.key)).map(agent => agent.key),
      })
    } catch (e) {
      const failure = teamFailure(e)
      setNeedsDashboard(failure.code === 'dashboard_needed'); setError(failure.message); setSaving(false)
    }
  }
  const next = () => setStep(step + 1)
  const total = STEPS + offset
  const heading = ['Who is in the Team?', 'What does the Team share?', 'Which agents can the Team ask?'][step - 1]
  const sub = ['Pick people. You can add more later.', 'One folder everyone works in. Only this folder is shared; you can add more later.', 'New agents start in the Team. Your saved choices stay the same. Untick any you want to keep to yourself.'][step - 1]
  const canNext = step !== 2 || Boolean(shared)
  return <section aria-label="Set up the Team" className="rounded-xl border border-border text-sm">
    <div className="flex items-center gap-2.5 border-b border-border px-4 py-3">
      <div className="flex flex-1 gap-1" aria-hidden="true">{Array.from({ length: total }, (_, i) => <i key={i} className={`h-1 flex-1 rounded ${i < step + offset ? 'bg-primary' : 'bg-border'}`} />)}</div>
      <span className="whitespace-nowrap text-xs text-muted-foreground">Step {step + offset} of {total}</span>
    </div>
    <div className="space-y-2 px-4 py-4">
      <h3 className="text-[15px] font-semibold">{heading}</h3>
      <p className="pb-1 text-xs text-muted-foreground">{sub}</p>
      {step === 1 && <div className="space-y-2">
        <p className="pt-1 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">People</p>
        {options.people.length === 0 && <p className="text-xs text-muted-foreground">There is nobody else on this server to add yet.</p>}
        {options.people.map(person => {
          const level = picked[person.user_id]
          return <Option key={person.user_id} kind="check" on={Boolean(level)} title={person.name} line={person.email}
            onClick={() => setPicked(current => { const { [person.user_id]: removed, ...rest } = current; return removed || !firstLevel ? rest : { ...rest, [person.user_id]: firstLevel } })}
            aside={level && <select aria-label={`${person.name}: how they use this project`} value={level} onChange={event => setPicked(current => ({ ...current, [person.user_id]: event.target.value as CrewPeopleLevelKey }))} className="shrink-0 rounded-md border border-border bg-background px-2 py-0.5 text-xs">
              {options.levels.map(item => <option key={item.key} value={item.key} disabled={!item.available} title={item.why || undefined}>{item.label}</option>)}
            </select>} />
        })}
      </div>}
      {step === 2 && <div role="radiogroup" aria-label={heading} className="space-y-2">
        {[options.folders.default, ...options.folders.folders.filter(item => item !== options.folders.default)].map(item => <Option key={item} kind="radio" on={typed === undefined && folder === item} title={`${item}/`}
          line={item === options.folders.default ? 'Suggested' : 'A folder in this project'}
          onClick={() => { setFolder(item); setTyped(undefined) }} />)}
        <Option kind="radio" on={typed !== undefined} title="A new folder" line="Type a name" onClick={() => setTyped(typed ?? '')} />
        {typed !== undefined && <input aria-label="New folder name" autoFocus value={typed} onChange={event => setTyped(event.target.value)} placeholder="Folder name" className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm" />}
        {options.folders.offline && <p className="text-xs text-muted-foreground">{options.folders.offline}</p>}
      </div>}
      {step === 3 && <div className="space-y-2">
        <p className="text-xs text-muted-foreground">Terminal agents already joined to this project are shared when the Team turns on, unless you chose Only me. Their saved choices stay the same.</p>
        {options.agents.map(agent => <Option key={agent.key} kind="check" on={agents.includes(agent.key)} title={agent.name}
          line="Untick to keep it to yourself."
          onClick={() => setAgents(current => current.includes(agent.key) ? current.filter(key => key !== agent.key) : [...current, agent.key])}
          aside={agent.is_default && <span className="shrink-0 rounded-full border border-border px-2 py-px text-xs text-muted-foreground">Default</span>} />)}
      </div>}
      {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
    </div>
    <div className="flex justify-end gap-2 border-t border-border px-4 py-3">
      <Button size="sm" variant="outline" disabled={saving} onClick={() => { if (step === 1) onNo(); else { setStep(step - 1); setNeedsDashboard(false); setError('') } }}>{step === 1 ? 'Cancel' : 'Back'}</Button>
      {step < STEPS
        ? <Button size="sm" disabled={!canNext} onClick={next}>Continue</Button>
        : <Button size="sm" disabled={saving} onClick={() => void finish(needsDashboard)}>{saving && <Loader2 aria-label="Saving" className="animate-spin" />}{needsDashboard ? 'Create dashboard and finish' : 'Turn the Team on'}</Button>}
    </div>
  </section>
}
