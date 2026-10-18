import { useState } from 'react'
import { useWorkspaceViewTarget } from '../../hooks/useWorkspaceViewTarget'
import { PulseIcon } from './pulseIcon'
import { PulseWorkspace, type PulseWorkspaceTab } from './PulseWorkspace'
import { GoalStatusCard } from './GoalStatusCard'
import { GoalLeadPanel } from './GoalLeadPanel'
import { PulseOffCard } from './PulseOffCard'
import { WorkspaceViewHeader } from './WorkspaceViewHeader'
import { WorkspaceViewIconButton } from './WorkspaceViewIconButton'
import { WORKFLOW_SOUL_REFRESH_EVENT } from './SoulViewer'
import type { WorkflowRunSetup, PulseOutwardActions, PulseFinalCommandState, PulseGoalStatus, PulseGoalWorkItem, PulseModuleState, PulseNextRun, PulsePlanDriftDueItem, PulseReviewFocus } from '../../services/api-types'

export interface PulseOverview {
  recorded: number
  total: number
  latest: string
}

interface PulseViewProps {
  workspacePath: string | null
  monitorOn: boolean
  monitorSaving: boolean
  onToggleMonitor: () => void
  hasSoul?: boolean
  runSetup?: WorkflowRunSetup | null
  moduleStates: PulseModuleState[]
  planDriftDue: boolean
  planDriftDueItems: PulsePlanDriftDueItem[]
  planDriftDueError: string | null
  finalCommandStates: PulseFinalCommandState[]
  nextRun?: PulseNextRun | null
  goalWork?: PulseGoalWorkItem[]
  goalStatus?: PulseGoalStatus | null
  outwardActions?: PulseOutwardActions
  outwardSaving?: boolean
  onChangeOutward?: (next: PulseOutwardActions) => void
  focusAreas?: string[]
  focusSaving?: boolean
  onSaveFocusAreas?: (areas: string[]) => Promise<boolean>
  reviewFocuses: PulseReviewFocus[]
  reviewFocusSelections: PulseReviewFocus[]
  statusError: string | null
  statusLoading: boolean
  overview: PulseOverview
  onRefresh: () => void
  headerAction?: React.ReactNode
}

export default function PulseView({
  workspacePath,
  monitorOn,
  monitorSaving,
  onToggleMonitor,
  hasSoul = false,
  runSetup = null,
  moduleStates,
  planDriftDue,
  planDriftDueItems,
  planDriftDueError,
  finalCommandStates,
  goalWork = [],
  goalStatus = null,
  outwardActions = 'ask',
  outwardSaving = false,
  onChangeOutward,
  focusAreas = [],
  focusSaving = false,
  onSaveFocusAreas,
  reviewFocuses,
  reviewFocusSelections,
  statusError,
  statusLoading,
  overview,
  onRefresh,
  headerAction,
}: PulseViewProps) {
  const [tab, setTab] = useState<PulseWorkspaceTab>('for_you')
  // Pulse off, or on without a goal in soul.md: the owner manages the workflow.
  const ownerManaged = !monitorOn || !hasSoul
  useWorkspaceViewTarget('pulse', target => { if (target === 'for_you' || target === 'platform') setTab(target) })
  return (
    <div className="flex h-full min-h-0 w-full max-w-none flex-col bg-background">
      <WorkspaceViewHeader
        icon={PulseIcon}
        title="Pulse"
        helpTopic={`Pulse · ${tab === 'for_you' ? 'For you' : 'Platform health'}`}
        context={<span className={`rounded-full border px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide ${monitorOn ? 'border-primary/25 bg-primary/10 text-primary' : 'border-border bg-muted text-muted-foreground'}`}>
          {monitorOn ? 'On' : 'Off'}
        </span>}
        subtitle={`${overview.recorded}/${overview.total} statuses recorded${overview.latest ? ` · Updated ${overview.latest}` : ''}`}
        actions={<>
          {headerAction}
          <WorkspaceViewIconButton
            label="Refresh Pulse status"
            onClick={() => {
              window.dispatchEvent(new CustomEvent(WORKFLOW_SOUL_REFRESH_EVENT))
              onRefresh()
            }}
            disabled={statusLoading}
            spinning={statusLoading}
          />
        </>}
      />

      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="p-3 sm:p-4">
          {ownerManaged && <PulseOffCard pulseOn={monitorOn} hasSoul={hasSoul} runSetup={runSetup} saving={monitorSaving} onTurnOn={onToggleMonitor} />}
          {!monitorOn && hasSoul && <section className="mt-4" aria-label="Weekly Pulse report">
            <h3 className="mb-2 text-sm font-semibold">Weekly report</h3>
            {goalStatus?.latest_check?.report_only ? <div className="rounded-lg border bg-background p-3">
              <p className="whitespace-pre-line text-sm">{goalStatus.latest_check.summary}</p>
              <p className="mt-2 text-xs text-muted-foreground">Reported {new Date(goalStatus.latest_check.checked_at).toLocaleDateString()}{goalStatus.latest_check.next_check_at && ` · Next report ${new Date(goalStatus.latest_check.next_check_at).toLocaleDateString()}`}</p>
            </div> : <p className="text-xs text-muted-foreground">The report will appear here after its first check. Pulse suggests improvements and makes no changes while it is off.</p>}
          </section>}
          {!ownerManaged && workspacePath && <GoalStatusCard goal={goalStatus} workspacePath={workspacePath} />}
          {!ownerManaged && <label className="my-3 flex flex-wrap items-center justify-between gap-3 rounded-lg border bg-background px-3 py-2 text-sm">
            <span>Outward actions<span className="block text-xs text-muted-foreground">Posts and messages within this workflow’s existing limits</span></span>
            <select aria-label="Pulse outward actions" value={outwardActions} disabled={outwardSaving || !onChangeOutward} onChange={event => onChangeOutward?.(event.target.value as PulseOutwardActions)} className="rounded-md border bg-background px-2 py-1.5 text-sm">
              <option value="ask">Ask first</option><option value="allow">Allowed within limits</option>
            </select>
          </label>}
          {!ownerManaged && workspacePath && <GoalLeadPanel workspacePath={workspacePath} />}
          {!ownerManaged && workspacePath && (
            <PulseWorkspace
              activeTab={tab}
              onTabChange={setTab}
              workspacePath={workspacePath}
              moduleStates={moduleStates}
              planDriftDue={planDriftDue}
              planDriftDueItems={planDriftDueItems}
              planDriftDueError={planDriftDueError}
              finalCommandStates={finalCommandStates}
              reviewFocuses={reviewFocuses}
              reviewFocusSelections={reviewFocusSelections}
              statusError={statusError}
              goalWork={goalWork}
              focusAreas={focusAreas}
              focusSaving={focusSaving}
              onSaveFocusAreas={onSaveFocusAreas}
              goalLeadOwnsReviews={!!goalStatus?.goal_lead}
            />
          )}
        </div>
      </div>

      {monitorOn && <div className="flex shrink-0 items-center gap-3 border-t border-border bg-background px-4 py-3 sm:px-5">
        <button
          type="button"
          role="switch"
          aria-checked={monitorOn}
          onClick={onToggleMonitor}
          disabled={monitorSaving}
          className={`relative inline-flex h-5 w-9 flex-none items-center rounded-full p-0 transition-colors disabled:opacity-50 ${monitorOn ? 'bg-primary' : 'bg-muted-foreground/30'}`}
          aria-label="Toggle Pulse"
        >
          <span className={`inline-block h-4 w-4 rounded-full bg-white shadow-sm transition-transform ${monitorOn ? 'translate-x-[18px]' : 'translate-x-[2px]'}`} />
        </button>
        <div className="min-w-0">
          <div className="text-xs font-medium text-foreground">Pulse is on</div>
          <div className="truncate text-[11px] text-muted-foreground">{hasSoul
            ? 'Pulse owns the goal: it checks it daily and asks the Builder to make changes.'
            : 'Pulse needs the goal in soul.md before it starts.'}</div>
        </div>
      </div>}
    </div>
  )
}
