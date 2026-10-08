import { useState } from 'react'
import { schedulerApi } from '../../api/scheduler'
import { openWorkflowPulseChatTab } from '../../utils/pulseChatTab'
import type { PulseGoalStatus } from '../../services/api-types'

type Tone = 'good' | 'warn' | 'bad' | 'muted'

const TONE_CLASSES: Record<Tone, string> = {
  good: 'border-emerald-500/30 bg-emerald-500/5',
  warn: 'border-amber-500/30 bg-amber-500/5',
  bad: 'border-red-500/30 bg-red-500/5',
  muted: 'border-border bg-muted/40',
}

const BADGE_CLASSES: Record<Tone, string> = {
  good: 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-300',
  warn: 'bg-amber-500/15 text-amber-700 dark:text-amber-300',
  bad: 'bg-red-500/15 text-red-700 dark:text-red-300',
  muted: 'bg-muted text-muted-foreground',
}

function formatDate(value?: string): string {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  return date.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
}

/** The status label: the latest goal check's verdict, or what the code facts show before one exists. */
function statusOf(goal: PulseGoalStatus): { label: string; tone: Tone } {
  const verdict = goal.latest_check?.status
  const factStatus = goal.facts.status
  // A fresh code alarm outranks an older "on track" verdict.
  if (verdict === 'on_track' && goal.facts.alarms.length === 0) return { label: 'On track', tone: 'good' }
  if (verdict === 'off_track') return { label: 'Off track', tone: 'bad' }
  if (verdict === 'not_measured' || factStatus === 'not_measured') return { label: 'Not measured', tone: 'bad' }
  if (verdict === 'at_risk' || factStatus === 'at_risk' || verdict === 'on_track') return { label: 'At risk', tone: 'warn' }
  return { label: 'On track', tone: 'good' }
}

/** Goal status at the top of the Pulse tab (PLAT-697): on track / at risk / off track / not measured,
 * the key number, when it was last measured, and the silence alarms. */
export function GoalStatusCard({ goal, workspacePath }: { goal: PulseGoalStatus | null | undefined; workspacePath?: string }) {
  const [checkState, setCheckState] = useState<'idle' | 'starting' | 'started' | string>('idle')
  if (!goal?.facts?.has_goal) return null
  const { label, tone } = statusOf(goal)
  const facts = goal.facts
  const check = goal.latest_check
  const keyNumber = check?.key_number || (facts.key_value !== undefined && facts.key_metric ? `${facts.key_value} (${facts.key_metric})` : '')
  const lastRunMeasured = formatDate(facts.last_run_measured_at)
  const lastMeasured = formatDate(facts.last_measured_at)
  return (
    <section className={`mb-3 rounded-lg border p-3 ${TONE_CLASSES[tone]}`} aria-label="Goal status">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-xs font-semibold text-foreground">Goal</span>
        <span className={`rounded-full px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide ${BADGE_CLASSES[tone]}`}>{label}</span>
        {keyNumber && <span className="text-xs text-foreground">{keyNumber}</span>}
        <span className="text-[11px] text-muted-foreground">
          {lastRunMeasured ? `Last measured by a run ${lastRunMeasured}` : 'Never measured by a run'}
          {lastMeasured && lastMeasured !== lastRunMeasured ? ` · latest reading ${lastMeasured}` : ''}
        </span>
      </div>
      {check?.summary && <p className="mt-1.5 text-xs text-foreground">{check.summary}</p>}
      {check?.action_taken && <p className="mt-1 text-[11px] text-muted-foreground">Done: {check.action_taken}</p>}
      {facts.alarms.length > 0 && (
        <ul className="mt-2 space-y-1">
          {facts.alarms.map(alarm => (
            <li key={alarm.kind} className="text-[11px] text-foreground">• {alarm.message}</li>
          ))}
        </ul>
      )}
      {facts.schedules_paused && (
        <p className="mt-1.5 text-[11px] text-muted-foreground">
          Schedules are paused{facts.pause_already_reported ? '; this was reported once and stays quiet until something changes.' : '.'}
        </p>
      )}
      <div className="mt-1.5 flex flex-wrap items-center gap-2">
        <p className="text-[10px] text-muted-foreground">
          {check ? `Goal check ${formatDate(check.checked_at)}` : 'No goal check yet; Pulse checks the goal on its own.'}
          {check?.next_check_at && ` · Next check ${formatDate(check.next_check_at)}${check.next_check_reason ? `: ${check.next_check_reason}` : ''}`}
        </p>
        {workspacePath && (
          <button
            type="button"
            className="rounded border border-border px-1.5 py-0.5 text-[10px] text-foreground hover:bg-muted disabled:opacity-50"
            disabled={checkState === 'starting'}
            onClick={() => {
              setCheckState('starting')
              schedulerApi.runGoalCheck(workspacePath)
                .then(() => { setCheckState('started'); void openWorkflowPulseChatTab(workspacePath) })
                .catch((err: unknown) => {
                  const data = (err as { response?: { data?: unknown } })?.response?.data
                  setCheckState(typeof data === 'string' && data.trim() ? data.trim() : 'Could not start the goal check')
                })
            }}
          >
            {checkState === 'starting' ? 'Starting…' : 'Run goal check now'}
          </button>
        )}
        {checkState === 'started' && <span className="text-[10px] text-muted-foreground">Started in the Pulse chat tab; the result shows here in a minute or two.</span>}
        {checkState !== 'idle' && checkState !== 'starting' && checkState !== 'started' && <span className="text-[10px] text-red-600 dark:text-red-400">{checkState}</span>}
      </div>
    </section>
  )
}
