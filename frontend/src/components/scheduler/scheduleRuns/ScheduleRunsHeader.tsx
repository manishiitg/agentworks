import React from 'react'
import { X, Loader, Pause, Calendar } from 'lucide-react'
import { Tooltip, TooltipContent, TooltipTrigger } from '../../ui/tooltip'
import { WorkspaceViewHeader } from '../../workflow/WorkspaceViewHeader'
import { WorkspaceViewIconButton } from '../../workflow/WorkspaceViewIconButton'
import { ScheduleStatusPills } from './ScheduleStatusPills'
import type { ScheduleRunsPanelState } from './useScheduleRunsData'
import { useCanPauseSchedules } from '../../../hooks/useCanPauseSchedules'

type ScheduleRunsHeaderProps = {
  panel: Pick<ScheduleRunsPanelState,
    | 'panelTitle' | 'isLoading' | 'isWorkflowScoped' | 'workflowScheduleSummary' | 'summary'
    | 'isSchedulerPaused' | 'isReadOnlyUser' | 'handleToggleGlobalPause' | 'isUpdatingSchedulerPause' | 'loadJobs'
  >
  onClose: () => void
  /** Embedded workspace views are closed by their parent layout, not here. */
  showClose?: boolean
  compact?: boolean
  navigation?: React.ReactNode
}

export const ScheduleRunsHeader: React.FC<ScheduleRunsHeaderProps> = ({ panel, onClose, showClose = true, compact = false, navigation }) => {
  const {
    panelTitle,
    isLoading,
    isWorkflowScoped,
    workflowScheduleSummary,
    summary,
    isSchedulerPaused,
    isReadOnlyUser,
    handleToggleGlobalPause,
    isUpdatingSchedulerPause,
    loadJobs,
  } = panel
  const canPauseSchedules = useCanPauseSchedules()

  const statusPills = <ScheduleStatusPills status={{ summary, workflowScheduleSummary, isLoading, isSchedulerPaused, isWorkflowScoped }} />

  return (
    <WorkspaceViewHeader
      icon={compact ? undefined : Calendar}
      title={compact ? '' : panelTitle}
      context={compact ? <>
        {navigation}
        <span className="text-xs text-muted-foreground">{summary.total} schedules{isSchedulerPaused ? ' · All schedules paused' : ''}</span>
      </> : undefined}
      below={!compact ? statusPills : undefined}
      actions={<>
        {!isWorkflowScoped && !isReadOnlyUser && canPauseSchedules && !isSchedulerPaused && (
          <button
            onClick={handleToggleGlobalPause}
            disabled={isUpdatingSchedulerPause}
            className="inline-flex items-center gap-2 rounded-md border border-border bg-background px-3 py-1.5 text-xs font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:opacity-60"
          >
            {isUpdatingSchedulerPause ? (
              <Loader className="w-3.5 h-3.5 animate-spin" />
            ) : (
              <Pause className="w-3.5 h-3.5" />
            )}
            Pause all schedules
          </button>
        )}
        <Tooltip>
          <TooltipTrigger asChild>
            <WorkspaceViewIconButton
              label={isLoading ? 'Refreshing schedule status' : 'Refresh schedule status'}
              onClick={() => loadJobs(true)}
              disabled={isLoading}
              spinning={isLoading}
              className="disabled:cursor-wait"
            />
          </TooltipTrigger>
          <TooltipContent side="bottom">{isLoading ? 'Refreshing…' : 'Refresh'}</TooltipContent>
        </Tooltip>
        {showClose && (
          <button onClick={onClose} className="p-1.5 rounded-md text-muted-foreground hover:text-foreground hover:bg-muted transition-colors" aria-label="Close schedules">
            <X className="w-4 h-4" />
          </button>
        )}
      </>}
    />
  )
}
