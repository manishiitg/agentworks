import type { ReactNode } from 'react'
import { GoalProgress } from './GoalProgress'
import { PulseIcon } from './pulseIcon'
import { GoalLeadPanel } from './GoalLeadPanel'
import { SoulViewer, WORKFLOW_SOUL_REFRESH_EVENT } from './SoulViewer'
import { WorkspaceViewHeader } from './WorkspaceViewHeader'
import { WorkspaceViewIconButton } from './WorkspaceViewIconButton'
import { openWorkflowPulseChatTab } from '../../utils/pulseChatTab'
import { useChatStore } from '../../stores/useChatStore'
import type { PulseOutwardActions, PulseGoalStatus, PulseGoalWorkItem, PulseNextRun, PulseImpactLedger } from '../../services/api-types'

export interface PulseViewProps {
  workspacePath: string | null
  monitorOn: boolean
  monitorSaving: boolean
  onToggleMonitor: () => void
  hasSoul?: boolean
  goalStatus?: PulseGoalStatus | null
  impact?: PulseImpactLedger | null
  lastChange?: PulseGoalWorkItem | null
  nextRun?: PulseNextRun | null
  outwardActions?: PulseOutwardActions
  outwardSaving?: boolean
  onChangeOutward?: (next: PulseOutwardActions) => void
  statusError: string | null
  statusLoading: boolean
  onRefresh: () => void
  headerAction?: ReactNode
}

const date = (value?: string) => value ? new Date(value).toLocaleString() : ''

export default function PulseView({ workspacePath, monitorOn, monitorSaving, onToggleMonitor, hasSoul = false,
  goalStatus, impact, lastChange, nextRun, outwardActions = 'ask', outwardSaving = false,
  onChangeOutward, statusError, statusLoading, onRefresh, headerAction }: PulseViewProps) {
  const check = goalStatus?.latest_check
  return <div className="flex h-full min-h-0 w-full flex-col bg-background">
    <WorkspaceViewHeader icon={PulseIcon} title="Pulse" helpTopic="Pulse" subtitle="Your goal, what changed, and what happens next."
      actions={<>{headerAction}<WorkspaceViewIconButton label="Refresh Pulse" disabled={statusLoading} spinning={statusLoading}
        onClick={() => { window.dispatchEvent(new CustomEvent(WORKFLOW_SOUL_REFRESH_EVENT)); onRefresh() }} /></>} />
    <div className="min-h-0 flex-1 overflow-y-auto"><div className="mx-auto max-w-4xl space-y-4 p-4 sm:p-6">
      {statusError && <p role="alert" className="rounded-lg border border-destructive/30 p-3 text-sm text-destructive">{statusError}</p>}
      <section aria-label="Goal" className="rounded-xl border bg-background p-4">
        <h2 className="text-sm font-semibold">Goal</h2>
        {workspacePath && <SoulViewer workspacePath={workspacePath} pulseSummary />}
        {impact && workspacePath && <GoalProgress workspacePath={workspacePath} impact={impact} />}

      </section>
      <section aria-label="Pulse settings" className="rounded-xl border bg-background p-4">
        <div className="flex items-center justify-between gap-4"><div><h2 className="text-sm font-semibold">Pulse</h2>
          <p className="mt-1 text-xs text-muted-foreground">{monitorOn ? 'Checks the goal and works with Builder on improvements.' : 'Fully off. No checks, reports, reminders or messages.'}</p></div>
          <button type="button" role="switch" aria-label="Pulse on/off" aria-checked={monitorOn} disabled={monitorSaving} onClick={onToggleMonitor}
            className={`shrink-0 rounded-full px-4 py-2 text-sm font-medium disabled:opacity-50 ${monitorOn ? 'bg-primary text-primary-foreground' : 'bg-muted text-muted-foreground'}`}>{monitorOn ? 'On' : 'Off'}</button></div>
        <label className="mt-4 flex flex-wrap items-center justify-between gap-3 border-t pt-4 text-sm"><span>Outward actions<span className="block text-xs text-muted-foreground">Posts and messages within your existing limits</span></span>
          <select aria-label="Pulse outward actions" value={outwardActions} disabled={outwardSaving || !onChangeOutward} onChange={event => onChangeOutward?.(event.target.value as PulseOutwardActions)} className="rounded-md border bg-background px-3 py-2 text-sm">
            <option value="ask">Ask first</option><option value="allow">Allowed within limits</option></select></label>
        {monitorOn && !hasSoul && <p className="mt-3 text-sm text-muted-foreground">Set the goal with Builder so Pulse knows what to pursue.</p>}
      </section>
      <section aria-label="Last change" className="rounded-xl border bg-background p-4"><h2 className="text-sm font-semibold">Last change</h2>
        {lastChange ? <><p className="mt-2 text-sm font-medium">{lastChange.title}</p><p className="mt-1 whitespace-pre-line text-sm">{lastChange.action_taken}</p>
          <p className="mt-2 text-xs text-muted-foreground">{lastChange.effect_note || 'The effect has not been verified yet.'}</p><p className="mt-1 text-xs text-muted-foreground">{date(lastChange.updated_at)}</p></> : <p className="mt-2 text-sm text-muted-foreground">No completed change has been recorded.</p>}
        {check && <div aria-label="Last check" className="mt-3 border-t pt-3 text-sm"><h3 className="text-xs font-semibold text-muted-foreground">Last check</h3><p className="mt-1">{check.summary}</p>
          {check.key_number && <p className="mt-2 font-medium">{check.key_number}</p>}
          <p className="mt-1 text-xs text-muted-foreground">Last checked {date(check.checked_at)}</p></div>}
      </section>
      {workspacePath && <GoalLeadPanel workspacePath={workspacePath} />}
      <section aria-label="Next" className="rounded-xl border bg-background p-4"><h2 className="text-sm font-semibold">Next</h2>
        <p className="mt-2 text-sm">{monitorOn ? nextRun?.next_at ? date(nextRun.next_at) : 'No check is scheduled yet.' : 'Pulse is off. No next check is scheduled.'}</p>
        {monitorOn && nextRun?.reason && <p className="mt-1 text-sm text-muted-foreground">{nextRun.reason}</p>}
      </section>
      <section aria-label="Talk to Pulse" className="rounded-xl border bg-background p-4"><h2 className="text-sm font-semibold">Talk to Pulse</h2>
        <p className="mt-1 text-sm text-muted-foreground">{monitorOn ? 'Ask about progress or give Pulse direction in its chat.' : 'Turn Pulse on before sending it a message.'}</p>
        <button type="button" disabled={!monitorOn || !hasSoul || !workspacePath} className="mt-3 rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-foreground disabled:opacity-50"
          onClick={() => { if (workspacePath) void openWorkflowPulseChatTab(workspacePath).catch(err => useChatStore.getState().addToast(err instanceof Error ? err.message : 'Could not open Pulse.', 'error')) }}>Open Pulse chat</button>
      </section>
    </div></div>
  </div>
}
