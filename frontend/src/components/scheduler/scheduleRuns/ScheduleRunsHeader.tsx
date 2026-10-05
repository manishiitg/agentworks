import React from 'react'
import { X, Play, Loader, Pause, Calendar } from 'lucide-react'
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
  headerAction?: React.ReactNode
  compact?: boolean
  navigation?: React.ReactNode
  helpTopic?: string
}

export const ScheduleRunsHeader: React.FC<ScheduleRunsHeaderProps> = ({ panel, onClose, showClose = true, headerAction, compact = false, navigation, helpTopic }) => {
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
      helpTopic={helpTopic ?? (compact ? 'Schedules' : panelTitle)}
      context={compact ? <>
        {navigation}
        <span className="text-xs text-muted-foreground">{summary.total} schedules{isSchedulerPaused ? ' · Scheduling paused' : ''}</span>
      </> : undefined}
      below={!compact ? statusPills : undefined}
      actions={<>
        {!isWorkflowScoped && !isReadOnlyUser && canPauseSchedules && (
          <button
            onClick={handleToggleGlobalPause}
            disabled={isUpdatingSchedulerPause}
            className={`inline-flex items-center gap-2 rounded-md border px-3 py-1.5 text-xs font-medium transition-colors disabled:opacity-60 ${
              isSchedulerPaused
                ? 'border-border bg-background text-foreground hover:bg-muted'
                : 'border-border bg-background text-muted-foreground hover:bg-muted hover:text-foreground'
            }`}
          >
            {isUpdatingSchedulerPause ? (
              <Loader className="w-3.5 h-3.5 animate-spin" />
            ) : isSchedulerPaused ? (
              <Play className="w-3.5 h-3.5" />
            ) : (
              <Pause className="w-3.5 h-3.5" />
            )}
            {isSchedulerPaused ? 'Resume schedules' : 'Pause all schedules'}
          </button>
        )}
        {headerAction}
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
