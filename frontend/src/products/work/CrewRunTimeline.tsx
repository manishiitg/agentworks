import { useEffect, useState } from 'react'
import type { RunPythonStep } from '../../services/api-types'
import { RowButton, RunDot } from '../../components/automation/AutomationKit'
import { CrewRunStepCard } from './CrewRunStepCard'
import { formatCost, formatDuration } from '../../components/workflow/costs/helpers'

/**
 * An opened run's steps on a time axis (#3618). Steps one parallel() call ran together share a band that says how many
 * ran at the same time and which was slowest. The run record (python_steps) says when each step ran and which ran
 * together; this only draws it. Narrow screens use the list instead (the caller hides this below md).
 */
type Dot = 'running' | 'finished' | 'stopped' | 'idle'
const dot = (status: string): Dot => status === 'running' ? 'running' : status === 'failed' ? 'stopped' : status === 'completed' ? 'finished' : 'idle'
const BAR: Record<Dot, string> = { finished: 'border border-emerald-500/60 bg-emerald-500/25', stopped: 'border border-red-500/60 bg-red-500/25', running: 'border border-amber-400/70 bg-amber-400/25 animate-pulse', idle: 'border border-border bg-muted' }

type Row = { kind: 'step'; step: RunPythonStep } | { kind: 'group'; id: string; steps: RunPythonStep[] }

function useNow(live: boolean) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!live) return
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [live])
  return now
}

function rowsOf(steps: RunPythonStep[]): Row[] {
  const rows: Row[] = []
  const groups = new Map<string, RunPythonStep[]>()
  for (const step of steps) {
    if (!step.parallel_group) {
      rows.push({ kind: 'step', step })
      continue
    }
    const group = groups.get(step.parallel_group)
    if (group) {
      group.push(step)
      continue
    }
    const members = [step]
    groups.set(step.parallel_group, members)
    rows.push({ kind: 'group', id: step.parallel_group, steps: members })
  }
  return rows
}

function axisLabel(ms: number) {
  const seconds = Math.round(ms / 1000)
  return seconds < 60 ? `${seconds}s` : `${Math.floor(seconds / 60)}m ${String(seconds % 60).padStart(2, '0')}s`
}

export function CrewRunTimeline({ steps, costOf, onAskStep, onAskResume }: {
  steps: RunPythonStep[]
  costOf: (step: RunPythonStep) => number | undefined
  onAskStep?: (step: RunPythonStep) => void
  onAskResume?: (failed: RunPythonStep) => void
}) {
  const ordered = [...steps].sort((a, b) => a.started_at.localeCompare(b.started_at))
  const live = ordered.some(step => step.status === 'running')
  const now = useNow(live)
  const [openId, setOpenId] = useState<string | null>(null)
  if (ordered.length === 0) return null
  const startOf = (step: RunPythonStep) => Date.parse(step.started_at)
  const endOf = (step: RunPythonStep) => step.completed_at ? Date.parse(step.completed_at) : step.duration_ms !== undefined && step.status !== 'running' ? startOf(step) + step.duration_ms : now
  const origin = Math.min(...ordered.map(startOf))
  const span = Math.max(1000, Math.max(...ordered.map(endOf)) - origin)
  const ticks = [0, 0.2, 0.4, 0.6, 0.8, 1].map(part => part * span)
  const rows = rowsOf(ordered)
  const open = ordered.find(step => step.id === openId)
  const failedGroup = rows.find((row): row is Extract<Row, { kind: 'group' }> => row.kind === 'group' && row.steps.some(step => step.status === 'failed'))
  const failedPart = failedGroup?.steps.find(step => step.status === 'failed')

  const bar = (step: RunPythonStep) => {
    const left = ((startOf(step) - origin) / span) * 100
    const width = Math.max(0.6, ((endOf(step) - startOf(step)) / span) * 100)
    const cost = costOf(step)
    const label = [formatDuration(endOf(step) - startOf(step)), cost ? formatCost(cost) : ''].filter(Boolean).join(' · ')
    const wide = width > 14
    return <button type="button" key={step.id} data-testid="timeline-step" data-state={dot(step.status)} onClick={() => setOpenId(openId === step.id ? null : step.id)}
      aria-pressed={openId === step.id} className="grid w-full grid-cols-[minmax(8rem,14rem)_1fr] items-center gap-3 rounded px-1 py-1 text-left hover:bg-muted/60">
      <span className="flex min-w-0 items-center gap-2 text-xs"><RunDot state={dot(step.status)} /><span className="truncate" title={step.name}>{step.name}</span></span>
      <span className="relative h-5">
        <span className={`absolute top-0 flex h-5 items-center rounded ${BAR[dot(step.status)]} ${openId === step.id ? 'ring-2 ring-ring' : ''}`} style={{ left: `${left}%`, width: `${width}%` }}>
          {wide && <span className="truncate px-2 text-[11px] font-medium text-foreground">{label}</span>}
        </span>
        {!wide && <span className="absolute top-0.5 whitespace-nowrap text-[11px] text-muted-foreground" style={left + width > 85 ? { right: `${100 - left + 1}%` } : { left: `calc(${left + width}% + 6px)` }}>{label}</span>}
      </span>
    </button>
  }

  return <div data-testid="run-timeline" className="px-4 py-2">
    {failedGroup && failedPart && <div role="alert" className="mb-2 rounded-lg border border-red-500/50 bg-red-500/5 px-3 py-2 text-xs">
      <p className="font-medium text-red-700 dark:text-red-400">{failedPart.name} failed.</p>
      <p className="text-muted-foreground">The other {failedGroup.steps.length - 1} {failedGroup.steps.length - 1 === 1 ? 'result is' : 'results are'} kept. Resume runs only the failed part again.</p>
      {onAskResume && <div className="mt-1.5"><RowButton primary onClick={() => onAskResume(failedPart)}>Resume</RowButton></div>}
    </div>}
    <div className="grid grid-cols-[minmax(8rem,14rem)_1fr] gap-3 px-1 pb-1 text-[10px] text-muted-foreground">
      <span />
      <span className="relative h-3">{ticks.map((tick, index) => <span key={index} className="absolute -translate-x-1/2" style={{ left: `${(tick / span) * 100}%` }}>{axisLabel(tick)}</span>)}</span>
    </div>
    {rows.map(row => {
      if (row.kind === 'step') return bar(row.step)
      const slowest = row.steps.reduce((a, b) => (endOf(a) - startOf(a)) >= (endOf(b) - startOf(b)) ? a : b)
      return <div key={row.id} data-testid="timeline-parallel" className="my-1 rounded-md border-l-2 border-violet-500 bg-violet-500/5 py-1">
        <p className="px-2 pb-0.5 text-[10px] font-semibold uppercase tracking-wide text-violet-700 dark:text-violet-300">{row.steps.length} at the same time · slowest: {slowest.name}</p>
        {row.steps.map(bar)}
      </div>
    })}
    {open && <div className="mt-2 space-y-1.5">
      <CrewRunStepCard title={open.name} state={dot(open.status)} when={[formatDuration(endOf(open) - startOf(open)), costOf(open) ? formatCost(costOf(open)!) : ''].filter(Boolean).join(' · ')} detail={open.detail} />
      {onAskStep && <RowButton onClick={() => onAskStep(open)}>Ask about this step</RowButton>}
    </div>}
  </div>
}
