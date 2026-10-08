import { useEffect, useState } from 'react'
import { CheckCircle2, Crosshair, FileText, Hourglass, Lightbulb, Loader2, Play, Plus, Scale, X } from 'lucide-react'
import type { PulseAutonomy, PulseGoalWorkItem } from '../../services/api-types'
import { AUTONOMY_LEVELS, autonomyLevelIndex } from './pulseAutonomy'
import { openReportFileInViewer } from './reportWidgets/tableHelpers'

// Goal Work is Pulse's main job (docs/design/pulse_goal_work.md): work Pulse
// did to move the user's goals, constraints it is challenging, and gaps it
// found but has not acted on yet. Decisions themselves render in the existing
// "Needs you" panel; this view shows the work behind them.

const effectLabels: Record<string, { label: string; className: string }> = {
  worked: { label: 'Worked', className: 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300' },
  no_effect: { label: 'No effect', className: 'border-border bg-muted text-muted-foreground' },
  unclear: { label: 'Unclear', className: 'border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300' },
}

const constraintClassLabels: Record<string, string> = {
  boundary: 'Boundary: clarification only',
  choice: 'Choice: open to a test',
  unconfirmed: 'Unconfirmed: confirm or remove',
}

function shortDate(value?: string): string {
  if (!value) return ''
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
}

function statusText(item: PulseGoalWorkItem): string {
  if (item.effect) return effectLabels[item.effect]?.label || item.effect
  switch (item.status) {
    case 'done': return item.check_at ? `Waiting to see the effect · check ${shortDate(item.check_at)}` : 'Done'
    case 'in_progress': return 'In progress'
    case 'needs_user': return 'Waiting for you'
    case 'dropped': return 'Dropped'
    default: return 'Idea'
  }
}

function WorkLinks({ workspacePath, links }: { workspacePath: string; links: string[] }) {
  if (!links.length) return null
  return <div className="mt-2 flex flex-wrap gap-1.5">
    {links.map(link => {
      const path = link.startsWith('Workflow/') ? link : `${workspacePath.replace(/\/$/, '')}/${link.replace(/^\//, '')}`
      return <button key={link} type="button" onClick={() => openReportFileInViewer(path)}
        className="inline-flex max-w-full items-center gap-1 truncate rounded-md border bg-background px-2 py-0.5 text-[10px] font-medium text-primary hover:bg-primary/10" title={path}>
        <FileText className="h-3 w-3 shrink-0" /><span className="truncate">{link.split('/').pop() || link}</span>
      </button>
    })}
  </div>
}

function ItemRow({ item, workspacePath }: { item: PulseGoalWorkItem; workspacePath: string }) {
  const effect = item.effect ? effectLabels[item.effect] : null
  return <li className="py-3">
    <div className="flex flex-wrap items-start justify-between gap-2">
      <p className="min-w-0 flex-1 text-xs font-medium text-foreground">{item.title}</p>
      <span className={`shrink-0 rounded-full border px-2 py-0.5 text-[10px] font-semibold ${effect ? effect.className : 'border-border bg-muted/60 text-muted-foreground'}`}>{statusText(item)}</span>
    </div>
    {item.kind === 'constraint_challenge' && item.constraint_text && <p className="mt-1 text-[11px] text-muted-foreground">
      Rule: “{item.constraint_text}”{item.constraint_class ? ` · ${constraintClassLabels[item.constraint_class] || item.constraint_class}` : ''}
    </p>}
    {item.action_taken && <p className="mt-1 text-xs leading-5 text-muted-foreground">{item.action_taken}</p>}
    {!item.action_taken && item.detail && <p className="mt-1 whitespace-pre-line text-xs leading-5 text-muted-foreground">{item.detail}</p>}
    {item.action_taken && item.detail && <details className="mt-1 text-xs">
      <summary className="cursor-pointer font-medium text-foreground/80">Why this should move the goal</summary>
      <p className="mt-1 whitespace-pre-line leading-5 text-muted-foreground">{item.detail}</p>
    </details>}
    {(item.metric || item.effect_note) && <p className="mt-1 text-[11px] text-muted-foreground">
      {item.metric && <>Should move <span className="font-medium text-foreground/80">{item.metric}</span>{item.expected_direction ? ` (${item.expected_direction})` : ''}</>}
      {item.metric && item.effect_note && ' · '}{item.effect_note}
    </p>}
    <WorkLinks workspacePath={workspacePath} links={item.links || []} />
  </li>
}

function Section({ title, hint, icon: Icon, items, empty, workspacePath }: {
  title: string; hint: string; icon: typeof CheckCircle2; items: PulseGoalWorkItem[]; empty: string; workspacePath: string
}) {
  return <section aria-label={title} className="rounded-xl border bg-background p-4">
    <div className="flex items-center gap-2"><Icon className="h-4 w-4 text-primary" /><h3 className="text-sm font-semibold">{title}</h3>
      {items.length > 0 && <span className="rounded-full bg-muted px-2 py-0.5 text-[10px] font-semibold tabular-nums text-muted-foreground">{items.length}</span>}</div>
    <p className="mt-1 text-xs text-muted-foreground">{hint}</p>
    {items.length === 0
      ? <p className="mt-3 rounded-lg border border-dashed p-3 text-xs text-muted-foreground">{empty}</p>
      : <ul className="mt-1 divide-y">{items.map(item => <ItemRow key={item.id} item={item} workspacePath={workspacePath} />)}</ul>}
  </section>
}

const MAX_FOCUS_AREAS = 10

// FocusAreas: the user's own priorities for Goal Work (workflow.json
// pulse.focus_areas), editable here, with installed playbooks' strategy focus
// shown read-only underneath so there is one place to see what Goal Work is
// pointed at.
function FocusAreas({ areas, saving, onSave, playbookAreas }: {
  areas: string[]; saving: boolean; onSave?: (areas: string[]) => Promise<boolean>
  playbookAreas: Array<{ area: string; source: string }>
}) {
  const [draft, setDraft] = useState<string[]>(areas)
  const [adding, setAdding] = useState('')
  useEffect(() => { setDraft(areas) }, [areas])
  const dirty = draft.join('\n') !== areas.join('\n')
  const add = () => {
    const value = adding.trim()
    if (!value || draft.length >= MAX_FOCUS_AREAS || draft.some(item => item.toLowerCase() === value.toLowerCase())) return
    setDraft([...draft, value])
    setAdding('')
  }
  return <section aria-label="Focus areas" className="rounded-xl border bg-background p-4">
    <div className="flex items-center gap-2"><Crosshair className="h-4 w-4 text-primary" /><h3 className="text-sm font-semibold">Focus areas</h3></div>
    <p className="mt-1 text-xs text-muted-foreground">Where Goal Work should look first. It still considers anything that moves your goals, and your goals and rules always win.</p>
    <ul className="mt-3 space-y-1.5">
      {draft.map((area, index) => <li key={area} className="flex items-start gap-2 rounded-md border bg-muted/20 px-2.5 py-1.5 text-xs">
        <span className="min-w-0 flex-1">{area}</span>
        <button type="button" aria-label={`Remove focus area ${area}`} disabled={!onSave || saving} onClick={() => setDraft(draft.filter((_, i) => i !== index))}
          className="shrink-0 rounded p-0.5 text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50"><X className="h-3 w-3" /></button>
      </li>)}
      {draft.length === 0 && <li className="rounded-md border border-dashed px-2.5 py-2 text-xs text-muted-foreground">No focus areas yet. Goal Work picks its own priorities from your goals.</li>}
    </ul>
    {onSave && <div className="mt-2 flex flex-wrap items-center gap-2">
      <input value={adding} onChange={event => setAdding(event.target.value)} maxLength={300} disabled={saving || draft.length >= MAX_FOCUS_AREAS}
        onKeyDown={event => { if (event.key === 'Enter') { event.preventDefault(); add() } }}
        placeholder={draft.length >= MAX_FOCUS_AREAS ? 'Up to 10 focus areas' : 'e.g. Find more audience strategies like SaaS Builder'}
        aria-label="New focus area" className="min-w-0 flex-1 rounded-md border bg-background px-2 py-1 text-xs" />
      <button type="button" onClick={add} disabled={!adding.trim() || saving || draft.length >= MAX_FOCUS_AREAS}
        className="inline-flex items-center gap-1 rounded-md border px-2 py-1 text-xs font-medium hover:bg-muted disabled:opacity-50"><Plus className="h-3 w-3" />Add</button>
      {dirty && <button type="button" onClick={() => { void onSave(draft) }} disabled={saving}
        className="inline-flex items-center gap-1 rounded-md border border-primary/40 bg-primary/10 px-2 py-1 text-xs font-semibold text-primary hover:bg-primary/15 disabled:opacity-50">
        {saving ? <Loader2 className="h-3 w-3 animate-spin" /> : null}Save</button>}
    </div>}
    {playbookAreas.length > 0 && <div className="mt-3 border-t pt-2">
      <p className="text-[11px] font-medium text-muted-foreground">From installed playbooks</p>
      <ul className="mt-1 space-y-1 text-[11px] text-muted-foreground">{playbookAreas.map(item => <li key={`${item.source}:${item.area}`}>{item.area} <span className="opacity-70">· {item.source}</span></li>)}</ul>
    </div>}
  </section>
}

export function PulseGoalWork({ workspacePath, items, autonomy, autonomySaving = false, onChangeAutonomy, onRunGoalWork, running = false, runBlockedReason, focusAreas = [], focusSaving = false, onSaveFocusAreas, playbookFocusAreas = [] }: {
  workspacePath: string
  items: PulseGoalWorkItem[]
  autonomy: PulseAutonomy
  autonomySaving?: boolean
  onChangeAutonomy?: (next: PulseAutonomy) => void
  onRunGoalWork?: () => void
  running?: boolean
  runBlockedReason?: string
  focusAreas?: string[]
  focusSaving?: boolean
  onSaveFocusAreas?: (areas: string[]) => Promise<boolean>
  playbookFocusAreas?: Array<{ area: string; source: string }>
}) {
  const work = items.filter(item => item.kind === 'goal_work')
  const didForYou = work.filter(item => item.status === 'done' || item.status === 'in_progress' || item.status === 'needs_user')
  const nextUp = work.filter(item => item.status === 'idea')
  const challenges = items.filter(item => item.kind === 'constraint_challenge' && item.status !== 'dropped')
  const keptRules = items.filter(item => item.kind === 'constraint_challenge' && item.status === 'dropped')

  return <div className="space-y-4" aria-label="Goal Work">
    <section className="flex flex-wrap items-center justify-between gap-3 rounded-xl border bg-primary/5 p-4">
      <div className="min-w-0">
        <h3 className="text-sm font-semibold">Goal Work</h3>
        <p className="mt-1 text-xs leading-5 text-muted-foreground">Pulse looks for what would move your goals that nobody is doing, does it, and checks whether it worked.</p>
      </div>
      <button type="button" onClick={onRunGoalWork} disabled={!onRunGoalWork || running} title={runBlockedReason || 'Run a Goal Work pass now'}
        className="inline-flex items-center gap-1.5 rounded-md border bg-background px-3 py-1.5 text-xs font-semibold text-foreground hover:bg-muted disabled:cursor-not-allowed disabled:opacity-50">
        {running ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Play className="h-3.5 w-3.5" />}{running ? 'Starting…' : 'Run Goal Work now'}
      </button>
    </section>

    <FocusAreas areas={focusAreas} saving={focusSaving} onSave={onSaveFocusAreas} playbookAreas={playbookFocusAreas} />

    <Section title="Did for you" hint="Work Pulse completed or prepared, what it should move, and whether it worked." icon={CheckCircle2}
      items={didForYou} empty="Nothing yet. After its next pass, Pulse lists the work it did here." workspacePath={workspacePath} />
    <Section title="Challenging your rules" hint="Constraints that may be holding the goal back. Nothing changes until you answer in Needs you; the rule stays in force meanwhile." icon={Scale}
      items={challenges} empty="No constraint is being challenged." workspacePath={workspacePath} />
    <Section title="Next up" hint="Gaps Pulse found but has not acted on yet." icon={Lightbulb}
      items={nextUp} empty="No open ideas." workspacePath={workspacePath} />
    {keptRules.length > 0 && <details className="rounded-xl border bg-background px-4 py-3 text-xs">
      <summary className="cursor-pointer font-medium">Rules you kept ({keptRules.length})</summary>
      <ul className="mt-2 space-y-1 text-muted-foreground">{keptRules.map(item => <li key={item.id}>{item.constraint_text || item.title}{item.effect_note ? `: ${item.effect_note}` : ''}</li>)}</ul>
    </details>}

    <AutonomySlider autonomy={autonomy} saving={autonomySaving} onChange={onChangeAutonomy} />
  </div>
}

function AutonomySlider({ autonomy, saving, onChange }: {
  autonomy: PulseAutonomy
  saving: boolean
  onChange?: (next: PulseAutonomy) => void
}) {
  const index = autonomyLevelIndex(autonomy)
  const level = AUTONOMY_LEVELS[index]
  return <section aria-label="Pulse autonomy" className="rounded-lg border bg-background px-3 py-2">
    <div className="flex items-center justify-between gap-3">
      <label htmlFor="pulse-autonomy" className="flex items-center gap-1.5 text-xs font-semibold"><Hourglass className="h-3.5 w-3.5 text-primary" />Pulse autonomy</label>
      <span className="text-[11px] font-medium text-primary">{level.label}</span>
    </div>
    <input id="pulse-autonomy" type="range" min={0} max={AUTONOMY_LEVELS.length - 1} step={1} value={index}
      disabled={!onChange || saving} aria-valuetext={level.label}
      className="reasoning-effort-slider reasoning-effort-slider--compact mt-2 w-full disabled:opacity-50"
      style={{ ['--reasoning-effort-fill' as string]: `${(index / (AUTONOMY_LEVELS.length - 1)) * 100}%` }}
      onChange={event => onChange?.(AUTONOMY_LEVELS[Number(event.target.value)].value)} />
    <div className="mt-0.5 flex justify-between text-[10px] text-muted-foreground">{AUTONOMY_LEVELS.map(item => <span key={item.label}>{item.label}</span>)}</div>
    <p className="mt-1 text-[11px] leading-4 text-muted-foreground" title="Your goals and rules in soul.md stay yours at every level: Pulse can challenge a rule, but only you change it.">{level.summary}</p>
  </section>
}
