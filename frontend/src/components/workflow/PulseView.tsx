import { useState } from 'react'
import { useWorkspaceViewTarget } from '../../hooks/useWorkspaceViewTarget'
import { PulseIcon } from './pulseIcon'
import { PulseWorkspace, type PulseWorkspaceTab } from './PulseWorkspace'
import { GoalStatusCard } from './GoalStatusCard'
import { GoalLeadPanel } from './GoalLeadPanel'
import { PulseOffCard } from './PulseOffCard'
import { AutonomySlider } from './AutonomySlider'
import { PaceSelector } from './PaceSelector'
import { WorkspaceViewHeader } from './WorkspaceViewHeader'
import { WorkspaceViewIconButton } from './WorkspaceViewIconButton'
import { WORKFLOW_SOUL_REFRESH_EVENT } from './SoulViewer'
import { DEFAULT_PULSE_AUTONOMY } from '../../services/api-types'
import type { WorkflowRunSetup, PulsePace, PulseAutonomy, PulseFinalCommandState, PulseGoalStatus, PulseGoalWorkItem, PulseModuleState, PulseNextRun, PulsePlanDriftDueItem, PulseReviewFocus, PulseReviewerModule } from '../../services/api-types'

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
  pace?: PulsePace
  paceSaving?: boolean
  onChangePace?: (next: PulsePace) => void
  disabledReviewModules: PulseReviewerModule[]
  reviewModuleSaving: PulseReviewerModule | null
  onToggleReviewModule: (module: PulseReviewerModule) => void
  moduleStates: PulseModuleState[]
  planDriftDue: boolean
  planDriftDueItems: PulsePlanDriftDueItem[]
  planDriftDueError: string | null
  finalCommandStates: PulseFinalCommandState[]
  nextRun?: PulseNextRun | null
  goalWork?: PulseGoalWorkItem[]
  goalStatus?: PulseGoalStatus | null
  autonomy?: PulseAutonomy
  autonomySaving?: boolean
  onChangeAutonomy?: (next: PulseAutonomy) => void
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
  pace = 'steady',
  paceSaving = false,
  onChangePace,
  disabledReviewModules,
  reviewModuleSaving,
  onToggleReviewModule,
  moduleStates,
  planDriftDue,
  planDriftDueItems,
  planDriftDueError,
  finalCommandStates,
  goalWork = [],
  goalStatus = null,
  autonomy = DEFAULT_PULSE_AUTONOMY,
  autonomySaving = false,
  onChangeAutonomy,
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
          {!ownerManaged && workspacePath && <GoalStatusCard goal={goalStatus} workspacePath={workspacePath} />}
          {!ownerManaged && <AutonomySlider autonomy={autonomy} saving={autonomySaving} onChange={onChangeAutonomy} />}
          {!ownerManaged && <PaceSelector pace={pace} saving={paceSaving} onChange={onChangePace} />}
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
              disabledReviewModules={disabledReviewModules}
              reviewModuleSaving={reviewModuleSaving}
              onToggleReviewModule={onToggleReviewModule}
              statusError={statusError}
              goalWork={goalWork}
              autonomy={autonomy}
              autonomySaving={autonomySaving}
              onChangeAutonomy={onChangeAutonomy}
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
