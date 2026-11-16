import { useEffect, useState } from 'react'
import { agentApi } from '../../services/api'
import type { ExecutionLogsResponse } from '../../services/api-types'
import { RunDot } from '../../components/automation/AutomationKit'
import { CrewLogBox } from './CrewLogBox'

/** One execution of a script in a run, as plain text. */
export type RunLogEntry = { id: string; title: string; state: 'finished' | 'stopped' | 'idle'; exitCode?: number; text: string; facts: string }

const text = (value: unknown): string => (typeof value === 'string' ? value.trim() : '')

/** The facts about one execution, as one line: when it ran, its script, why it failed, and whether its output was cut. */
function factsOf(content: Record<string, unknown>): string {
  const at = new Date(text(content.timestamp))
  const when = text(content.timestamp) && !Number.isNaN(at.getTime()) ? at.toLocaleString([], { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit', second: '2-digit' }) : ''
  const script = text(content.script_path).split('/').pop() ?? ''
  const reason = text(content.failure_reason).replace(/_/g, ' ')
  return [when && `Ran ${when}`, script && `script ${script}`, reason && `reason: ${reason}`, content.output_truncated === true && 'output was cut short', text(content.validation_error) && `validation: ${text(content.validation_error)}`].filter(Boolean).join(' · ')
}

/**
 * Every execution the run logged, in order, flattened from the server's per-step logs. The text is what the script
 * printed; the error is used when it printed nothing, and added when it says more than the output does.
 */
export function flattenRunLog(response: ExecutionLogsResponse | null): RunLogEntry[] {
  const entries: RunLogEntry[] = []
  for (const [stepId, step] of Object.entries(response?.steps ?? {})) {
    for (const execution of step.executions ?? []) {
      const content = (execution.content ?? {}) as Record<string, unknown> & { success?: unknown; exit_code?: unknown; output?: unknown; error?: unknown; mode?: unknown }
      const output = text(content.output)
      const error = text(content.error)
      const exitCode = typeof content.exit_code === 'number' ? content.exit_code : undefined
      const failed = content.success === false || (exitCode !== undefined && exitCode !== 0)
      const passed = content.success === true || exitCode === 0
      entries.push({
        id: `${stepId}:${execution.attempt}:${execution.file_path}`,
        title: content.mode === 'scripted_fast_path' || execution.fast_path ? 'Saved script' : `Attempt ${execution.attempt}`,
        state: failed ? 'stopped' : passed ? 'finished' : 'idle',
        exitCode,
        text: [output, error && !output.includes(error) ? error : ''].filter(Boolean).join('\n\n'),
        facts: factsOf(content),
      })
    }
  }
  return entries
}

/** A run's full execution log: plain lines and text, with no viewer, filter, search or cards around it. */
export function CrewRunLog({ workspacePath, runFolder }: { workspacePath: string; runFolder: string }) {
  const [entries, setEntries] = useState<RunLogEntry[] | null>(null)
  const [error, setError] = useState('')
  useEffect(() => {
    let current = true
    setEntries(null); setError('')
    agentApi.getExecutionLogs(workspacePath, runFolder)
      .then(response => { if (current) setEntries(flattenRunLog(response)) })
      .catch(cause => { if (current) setError(cause instanceof Error ? cause.message : 'Could not load the log.') })
    return () => { current = false }
  }, [workspacePath, runFolder])
  if (error) return <p role="alert" className="px-4 py-3 text-xs text-destructive">{error}</p>
  if (!entries) return <p className="px-4 py-3 text-xs text-muted-foreground">Loading the log…</p>
  if (!entries.length) return <p className="px-4 py-3 text-xs text-muted-foreground">No execution log was saved for this run.</p>
  return <div data-testid="run-log" className="space-y-3 px-4 py-3">
    {entries.map(entry => <CrewLogBox key={entry.id} testId="run-log-entry" meta={entry.facts || undefined} text={entry.text || '(nothing was printed)'}
      title={<span className="flex items-center gap-2"><RunDot state={entry.state} />{entry.title}
        <span className="font-normal text-muted-foreground">{entry.state === 'stopped' ? 'failed' : entry.state === 'finished' ? 'finished' : ''}{entry.exitCode !== undefined ? ` · exit code ${entry.exitCode}` : ''}</span></span>} />)}
  </div>
}
