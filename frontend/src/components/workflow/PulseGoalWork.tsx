import { useState } from 'react'
import { CheckCircle2, FileText, Lightbulb, Loader2, Play, Plus, Scale } from 'lucide-react'
import { agentApi } from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'
import { PULSE_FOCUS_AREAS_CHANGED_EVENT } from './pulseAutonomy'
import type { PulseAutonomy, PulseGoalWorkItem } from '../../services/api-types'
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

// Focus areas live in one card at the top of the Pulse tab (GoalLeadPanel);
// playbook focus areas show here as suggestions the owner can adopt there.
function PlaybookFocusSuggestions({ workspacePath, areas }: { workspacePath: string; areas: Array<{ area: string; source: string }> }) {
  const [busy, setBusy] = useState('')
  if (areas.length === 0) return null
  const adopt = async (area: string) => {
    setBusy(area)
    try {
      const end = new Date(Date.now() + 30 * 86_400_000).toISOString().slice(0, 10)
      const result = await agentApi.updateGoalLeadFocusArea(workspacePath, { action: 'add', text: area, end_date: end })
      if (!result.success) throw new Error(result.error || 'Could not adopt the focus area.')
      window.dispatchEvent(new CustomEvent(PULSE_FOCUS_AREAS_CHANGED_EVENT))
      useChatStore.getState().addToast('Focus area added', 'success')
    } catch (err) {
      useChatStore.getState().addToast(err instanceof Error ? err.message : 'Could not adopt the focus area.', 'error')
    } finally {
      setBusy('')
    }
  }
  return <section aria-label="Playbook focus suggestions" className="rounded-lg border bg-background px-3 py-2 text-[11px]">
    <p className="font-medium text-muted-foreground">Focus suggestions from installed playbooks</p>
    <ul className="mt-1 space-y-1">{areas.map(item => <li key={`${item.source}:${item.area}`} className="flex items-center gap-2">
      <span className="min-w-0 flex-1 text-foreground">{item.area} <span className="text-muted-foreground">· {item.source}</span></span>
      <button type="button" disabled={busy !== ''} onClick={() => void adopt(item.area)}
        className="inline-flex shrink-0 items-center gap-1 rounded-md border px-1.5 py-0.5 hover:bg-muted disabled:opacity-50">
        {busy === item.area ? <Loader2 className="h-3 w-3 animate-spin" /> : <Plus className="h-3 w-3" />}Adopt</button>
    </li>)}</ul>
  </section>
}

export function PulseGoalWork({ workspacePath, items, onRunGoalWork, running = false, runBlockedReason, playbookFocusAreas = [] }: {
  workspacePath: string
  items: PulseGoalWorkItem[]
  /** Shown at the top of the Pulse tab now (AutonomySlider); kept for callers. */
  autonomy?: PulseAutonomy
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

    <PlaybookFocusSuggestions workspacePath={workspacePath} areas={playbookFocusAreas} />

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

  </div>
}

