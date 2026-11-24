import { useEffect, useState } from 'react'
import { createPortal } from 'react-dom'
import { Check, ListChecks, X } from 'lucide-react'
import { MarkdownRenderer } from '../ui/MarkdownRenderer'
import { summarizePlan } from '../../utils/planSummary'

// Plan mode in the message box (#3711, approved mockup): no button while Plan is off. Shift+Tab, /plan or
// "Plan first" in the chat menu turn it on; while on, a small tag beside the model turns it off again.
export function PlanModeTag({ onToggle, onStart }: { onToggle: () => void; onStart?: () => void }) {
  // Quiet, like the model name beside it (owner: the filled pink tag read as an error).
  const base = 'group inline-flex items-center gap-1 rounded px-1 py-0.5 text-[11px] text-muted-foreground'
  if (onStart) {
    // Codex and Cursor answer a plan as a reply, so the tag offers the one step that starts the work and turns
    // Plan off. A Claude turn that ended without its plan card gets it too. Typing still sends changes.
    return (
      <span className={base} data-testid="plan-mode-chip" title="The plan is ready. Start runs it and turns Plan first off; or type changes.">
        <ListChecks className="h-3 w-3" aria-hidden="true" />
        Plan ready ·
        <button type="button" onClick={onStart} data-testid="plan-start-button"
          className="rounded px-0.5 font-medium text-primary hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
          Start
        </button>
      </span>
    )
  }
  return (
    <button type="button" onClick={onToggle} aria-pressed="true" data-testid="plan-mode-chip"
      title="Plan first is on: the agent proposes a plan and changes nothing until you approve. Click or press Shift+Tab to turn it off."
      className={`${base} hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring`}>
      <ListChecks className="h-3 w-3" aria-hidden="true" />
      Plan first
      <X className="hidden h-2.5 w-2.5 group-hover:inline" aria-hidden="true" />
    </button>
  )
}

type PlanAnswer = { approve: () => void; reject: () => void; busy?: boolean }

const buttonClass = 'rounded-md px-2.5 py-1 text-xs font-medium focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50'

/** One slim line for a plan: title, step count, its first line and the answer buttons. */
export function PlanLine({ plan, agent, answer, outcome, typeChanges, start }: {
  plan: string
  agent?: string
  answer?: PlanAnswer
  // Set once answered: the line shrinks to the outcome.
  outcome?: 'approved' | 'rejected' | 'changed' | 'closed'
  // The message box sends changes to this plan while it waits.
  typeChanges?: boolean
  // Codex and Cursor: their plan is a reply; one step starts the work.
  start?: () => void
}) {
  const [open, setOpen] = useState(false)
  const summary = summarizePlan(plan)
  const meta = [summary.steps ? `${summary.steps} step${summary.steps === 1 ? '' : 's'}` : '', agent || ''].filter(Boolean).join(' · ')
  const view = <button type="button" onClick={() => setOpen(true)} className="rounded px-1.5 py-1 text-xs font-medium text-primary hover:bg-primary/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">View plan</button>
  const pane = open && <PlanPane plan={summary.body} title={summary.title} meta={meta} answer={outcome ? undefined : answer} typeChanges={typeChanges} onClose={() => setOpen(false)} />
  if (outcome) {
    return <div className="flex w-full min-w-0 items-center gap-2 rounded-lg border border-border bg-card px-2.5 py-1 text-xs" data-testid="plan-line" data-outcome={outcome}>
      {outcome === 'approved' ? <Check className="h-3.5 w-3.5 shrink-0 text-green-600 dark:text-green-400" aria-hidden="true" /> : <X className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />}
      <span className={`shrink-0 font-semibold ${outcome === 'approved' ? 'text-green-700 dark:text-green-400' : 'text-muted-foreground'}`}>
        {outcome === 'approved' ? 'Plan approved' : outcome === 'rejected' ? 'Plan rejected' : outcome === 'changed' ? 'Changes asked' : 'Plan closed'}
      </span>
      <span className="min-w-0 truncate text-muted-foreground">{summary.title}{meta ? ` · ${meta}` : ''}</span>
      <span className="ml-auto shrink-0">{view}</span>
      {pane}
    </div>
  }
  return <div className="w-full min-w-0 rounded-lg border border-border bg-card px-2.5 py-1.5" data-testid="plan-line">
    <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
      <ListChecks className="h-3.5 w-3.5 shrink-0 text-primary" aria-hidden="true" />
      <span className="min-w-0 truncate text-[13px] font-semibold">{summary.title}</span>
      {meta && <span className="shrink-0 text-xs text-muted-foreground">{meta}</span>}
      <span className="ml-auto flex shrink-0 items-center gap-1.5">
        {view}
        {start && <button type="button" onClick={start} className={`${buttonClass} bg-primary text-primary-foreground hover:bg-primary/90`}>Start</button>}
        {answer && <>
          <button type="button" disabled={answer.busy} onClick={answer.reject} className={`${buttonClass} border border-border bg-background hover:bg-muted`}>Reject</button>
          <button type="button" disabled={answer.busy} onClick={answer.approve} className={`${buttonClass} bg-primary text-primary-foreground hover:bg-primary/90`}>Approve</button>
        </>}
      </span>
    </div>
    {summary.peek && <p className="mt-0.5 truncate text-xs text-muted-foreground">{summary.peek}</p>}
    {(typeChanges || start) && <p className="mt-0.5 text-[11px] text-muted-foreground">Or type changes in the message box.</p>}
    {pane}
  </div>
}

/** The full plan beside the chat, so the conversation stays where it is. Narrow windows get it over the chat. */
export function PlanPane({ plan, title, meta, answer, typeChanges, onClose }: { plan: string; title: string; meta?: string; answer?: PlanAnswer; typeChanges?: boolean; onClose: () => void }) {
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => { if (event.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])
  const done = (action: () => void) => () => { action(); onClose() }
  return createPortal(<aside role="dialog" aria-label={`Plan: ${title}`} data-testid="plan-pane"
    className="fixed inset-y-0 right-0 z-[90] flex w-[min(440px,100vw)] flex-col border-l border-border bg-background text-foreground shadow-2xl">
    <header className="flex items-center gap-2 border-b border-border px-3 py-2.5">
      <ListChecks className="h-4 w-4 text-primary" aria-hidden="true" />
      <b className="text-sm">Plan</b>
      {meta && <span className="text-xs text-muted-foreground">{meta}</span>}
      <button type="button" onClick={onClose} aria-label="Close the plan" className="ml-auto rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><X className="h-4 w-4" aria-hidden="true" /></button>
    </header>
    <div className="min-h-0 flex-1 overflow-y-auto px-4 py-3">
      <MarkdownRenderer content={plan} className="text-sm" />
    </div>
    {answer && <footer className="flex items-center justify-end gap-2 border-t border-border px-3 py-2.5">
      <span className="mr-auto text-[11px] text-muted-foreground">{typeChanges ? 'Or type changes in the message box' : 'Reject, then say what to change'}</span>
      <button type="button" disabled={answer.busy} onClick={done(answer.reject)} className={`${buttonClass} border border-border bg-background hover:bg-muted`}>Reject</button>
      <button type="button" disabled={answer.busy} onClick={done(answer.approve)} className={`${buttonClass} bg-primary text-primary-foreground hover:bg-primary/90`}>Approve</button>
    </footer>}
  </aside>, document.body)
}
