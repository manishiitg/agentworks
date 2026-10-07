import { useState } from 'react'
import { useWorkspaceViewTarget } from '../../hooks/useWorkspaceViewTarget'
import { Activity } from 'lucide-react'
import { PulseWorkspace, type PulseWorkspaceTab } from './PulseWorkspace'
import { GoalStatusCard } from './GoalStatusCard'
import { WorkspaceViewHeader } from './WorkspaceViewHeader'
import { WorkspaceViewIconButton } from './WorkspaceViewIconButton'
import { WORKFLOW_SOUL_REFRESH_EVENT } from './SoulViewer'
import { DEFAULT_PULSE_AUTONOMY } from '../../services/api-types'
import type { PulseAutonomy, PulseFinalCommandState, PulseGoalStatus, PulseGoalWorkItem, PulseModuleState, PulseNextRun, PulsePlanDriftDueItem, PulseReviewFocus, PulseReviewerModule } from '../../services/api-types'

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

function nextPulseLabel(nextRun: PulseNextRun | null): string {
  if (!nextRun?.next_at) return 'Pulse is choosing its next run'
  const date = new Date(nextRun.next_at)
  if (Number.isNaN(date.getTime())) return 'Pulse is choosing its next run'
  const when = date.toLocaleString(undefined, { weekday: 'short', month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' })
  return date.getTime() <= Date.now() ? `Next Pulse due now (${when})` : `Next Pulse: ${when}`
}

export default function PulseView({
  workspacePath,
  monitorOn,
  monitorSaving,
  onToggleMonitor,
  disabledReviewModules,
  reviewModuleSaving,
  onToggleReviewModule,
  moduleStates,
  planDriftDue,
  planDriftDueItems,
  planDriftDueError,
  finalCommandStates,
  nextRun = null,
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
  useWorkspaceViewTarget('pulse', target => { if (target === 'for_you' || target === 'platform') setTab(target) })
  return (
    <div className="flex h-full min-h-0 w-full max-w-none flex-col bg-background">
      <WorkspaceViewHeader
        icon={Activity}
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
          {workspacePath && <GoalStatusCard goal={goalStatus} />}
          {workspacePath && (
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
            />
          )}
        </div>
      </div>

      <div className="flex shrink-0 items-center gap-3 border-t border-border bg-background px-4 py-3 sm:px-5">
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
          <div className="text-xs font-medium text-foreground">{monitorOn ? nextPulseLabel(nextRun) : 'Pulse is off'}</div>
          <div className="truncate text-[11px] text-muted-foreground" title={monitorOn ? nextRun?.reason : undefined}>{monitorOn
            ? (nextRun?.reason ? `Because ${nextRun.reason}` : 'Pulse runs on its own schedule. Normal runs only back up, publish and notify.')
            : 'Turn on to let Pulse review this workflow on its own schedule.'}</div>
        </div>
      </div>
    </div>
  )
}
