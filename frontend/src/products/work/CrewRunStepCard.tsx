import type { ReactNode } from 'react'
import { AiStepTag, Spinner } from '../../components/automation/AutomationKit'

/**
 * One step of a run as a single card: its name, whether it passed or failed, when it ran, and what it reported.
 */
const STATUS = { finished: 'Passed', stopped: 'Failed', running: 'Running now', idle: 'Not run' } as const
const STATUS_COLOUR = { finished: 'text-emerald-600 dark:text-emerald-400', stopped: 'text-red-600 dark:text-red-400', running: 'text-amber-600 dark:text-amber-400', idle: 'text-muted-foreground' } as const
// Passed is green, failed is red, the step that is running is amber and pulses, a step that has not run is quiet.
const CARD = { finished: 'border-emerald-500/40 bg-emerald-500/5', stopped: 'border-red-500/50 bg-red-500/5', running: 'border-amber-400/70 bg-amber-400/10 ring-1 ring-amber-400/40', idle: 'border-border opacity-80' } as const
const DOT = { finished: 'bg-emerald-500', stopped: 'bg-red-500', running: 'bg-amber-400 animate-pulse', idle: 'bg-muted-foreground/40' } as const

export function CrewRunStepCard({ title, kind, state, when, checks, detail, description, position }: {
  title: string
  /** Plain Python, or a step an agent works out. */
  kind?: 'script' | 'agent'
  state: 'running' | 'finished' | 'stopped' | 'idle'
  when?: string
  checks?: ReactNode
  detail?: string
  /** What the step does, in one line. */
  description?: string
  /** Where the step sits in its task, such as "Step 2 of 4". */
  position?: string
}) {
  return <div data-testid="run-step" data-state={state} className={`overflow-hidden rounded-lg border ${CARD[state]}`}>
    <div className="flex items-start gap-3 px-3 py-2.5">
      {state === 'running'
        ? <span className="mt-1 flex shrink-0"><Spinner className="h-4 w-4" /></span>
        : <span aria-hidden="true" className={`mt-1.5 inline-block h-2.5 w-2.5 shrink-0 rounded-full ${DOT[state]}`} />}
      <div className="min-w-0 flex-1">
        {position && <p className="text-[10px] font-medium uppercase tracking-wide text-muted-foreground">{position}</p>}
        <p className="flex items-baseline justify-between gap-2"><span className="text-sm font-semibold">{title}<AiStepTag kind={kind} /></span><span className={`shrink-0 text-xs font-medium ${STATUS_COLOUR[state]}`}>{STATUS[state]}</span></p>
        {description && <p className="text-xs text-muted-foreground">{description}</p>}
        {when && <p className="text-xs text-muted-foreground">{when}</p>}
        {checks && <p className="text-xs text-muted-foreground">{checks}</p>}
        {detail && <p className={`mt-1 whitespace-pre-wrap break-words text-xs ${state === 'stopped' ? 'text-red-600 dark:text-red-400' : 'text-muted-foreground'}`}>{detail}</p>}
      </div>
    </div>
  </div>
}
