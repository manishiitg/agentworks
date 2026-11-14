import { useEffect, useState } from 'react'

const instant = (value: unknown): number | undefined => {
  if (typeof value !== 'string' || value.startsWith('0001-')) return undefined
  const at = Date.parse(value)
  return Number.isFinite(at) ? at : undefined
}
const duration = (ms: number): string => {
  const seconds = Math.max(0, Math.floor(ms / 1000))
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  return minutes < 60 ? `${minutes}m ${String(seconds % 60).padStart(2, '0')}s` : `${Math.floor(minutes / 60)}h ${minutes % 60}m`
}
export function backgroundTaskLine(record: Record<string, unknown>, now: number): string {
  const helper = record.kind === 'helper'
  const helperType = typeof record.helper_type === 'string' ? record.helper_type.trim() : ''
  const kind = helper ? helperType || 'Background agent' : 'Background shell'
  const state = record.state === 'started' ? 'running…' : ['finished', 'completed'].includes(String(record.state)) ? 'finished' : record.state === 'stopped' ? 'stopped' : record.state === 'failed' ? 'failed' : record.state === 'interrupted' ? 'interrupted' : 'ended'
  const start = instant(record.started_at), end = instant(record.ended_at)
  const elapsed = start !== undefined && (record.state === 'started' || end !== undefined) ? (end ?? now) - start : typeof record.duration_ms === 'number' ? record.duration_ms : undefined
  // Only presentation is delayed: normal tool steps and native ownership stay intact.
  if (helper && record.helper_mode === 'foreground' && (elapsed === undefined || elapsed < 3000)) return ''
  const parts = [helperType && record.state === 'started' ? kind : `${kind} ${state}`]
  if (helper) {
    const activity = record.state === 'started' && typeof record.current_step === 'string' && record.current_step.trim() ? record.current_step : record.helper_description
    if (typeof activity === 'string' && activity.trim()) parts.push(activity)
  }
  if (record.kind === 'background_command' && typeof record.command_label === 'string' && record.command_label.trim()) parts.push(record.command_label)
  if (start !== undefined && (record.state === 'started' || end !== undefined)) parts.push(duration((end ?? now) - start))
  else if (typeof record.duration_ms === 'number' && record.duration_ms > 0) parts.push(duration(record.duration_ms))
  if (typeof record.tokens === 'number' && Number.isSafeInteger(record.tokens) && record.tokens >= 0) {
    const count = new Intl.NumberFormat('en', { notation: 'compact', maximumFractionDigits: 1 }).format(record.tokens).replace('K', 'k')
    parts.push(`${count} tokens`)
  }
  return parts.join(' · ')
}

// State, start/end and task-owned usage are server facts. This clock only
// formats elapsed time; it never ends a task or assigns a parent's tokens.
export function NativeBackgroundTaskLine({ record }: { record: Record<string, unknown> }) {
  const [now, setNow] = useState(Date.now)
  useEffect(() => {
    if (record.state !== 'started') return
    setNow(Date.now())
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [record.work_id, record.state])
  const line = backgroundTaskLine(record, now)
  if (!line) return null
  return <div data-testid="native-background-task-line" data-background-agent-id={String(record.work_id)} className="my-1 flex items-center gap-2 text-xs text-muted-foreground" title={typeof record.name === 'string' ? record.name : undefined}>
    {record.state === 'started' && <span className="h-1.5 w-1.5 shrink-0 animate-pulse rounded-full bg-primary/70" aria-hidden="true" />}
    <span>{line}</span>
  </div>
}
