import type { ReactNode } from 'react'
import { RunDot } from '../../components/automation/AutomationKit'

/**
 * One step of a run as a single card: its name, whether it passed or failed, when it ran, and what it reported.
 */
const STATUS = { finished: 'Passed', stopped: 'Failed', running: 'Running', idle: 'Not run' } as const
const STATUS_COLOUR = { finished: 'text-emerald-600 dark:text-emerald-400', stopped: 'text-red-600 dark:text-red-400', running: 'text-amber-600 dark:text-amber-400', idle: 'text-muted-foreground' } as const

export function CrewRunStepCard({ title, kind, state, when, checks, detail }: {
  title: string
  /** Plain Python, or a step an agent works out. */
  kind?: 'script' | 'agent'
  state: 'running' | 'finished' | 'stopped' | 'idle'
  when?: string
  checks?: ReactNode
  detail?: string
}) {
  return <div data-testid="run-step" className="overflow-hidden rounded-lg border border-border">
    <div className="flex items-start gap-3 px-3 py-2.5">
      <span className="mt-1 flex shrink-0"><RunDot state={state} /></span>
      <div className="min-w-0 flex-1">
        <p className="flex items-baseline justify-between gap-2"><span className="text-sm font-semibold">{title}{kind && <span className="ml-2 rounded-full border border-border px-1.5 py-px text-[10px] font-normal text-muted-foreground">{kind === 'agent' ? 'Agentic' : 'Scripted'}</span>}</span><span className={`shrink-0 text-xs font-medium ${STATUS_COLOUR[state]}`}>{STATUS[state]}</span></p>
        {when && <p className="text-xs text-muted-foreground">{when}</p>}
        {checks && <p className="text-xs text-muted-foreground">{checks}</p>}
        {detail && <p className={`mt-1 whitespace-pre-wrap break-words text-xs ${state === 'stopped' ? 'text-red-600 dark:text-red-400' : 'text-muted-foreground'}`}>{detail}</p>}
      </div>
    </div>
  </div>
}
