import { memo, useMemo, type ReactElement } from 'react'
import { Handle, Position } from '@xyflow/react'
import { CheckCircle, XCircle, Loader2, Plus, RefreshCw, Bot } from 'lucide-react'
import type { StepNodeData } from '../hooks/usePlanToFlow'
import type { ChangeType } from '../hooks/usePlanData'
import { getExecutionModeVisuals } from './executionModeVisuals'
import { effectiveExecutionMode, effectiveExecutionModeReason } from '../../../utils/stepConfigMatching'
import { planStepTypeLabel } from '../canvas/scriptedStepPresentation'

interface StepNodeProps {
  data: StepNodeData
  selected?: boolean
}

const statusBorderColors: Record<string, string> = {
  pending: 'border-gray-300 dark:border-gray-600',
  running: 'border-blue-500 dark:border-blue-400',
  completed: 'border-green-500 dark:border-green-400',
  failed: 'border-red-500 dark:border-red-400'
}

const changeHighlightStyles: Record<ChangeType, string> = {
  added: 'ring-2 ring-emerald-500/60 shadow-emerald-500/20',
  updated: 'ring-2 ring-blue-500/60 shadow-blue-500/20',
  deleted: 'ring-2 ring-red-500/60 shadow-red-500/20 opacity-50'
}

const changeBadgeStyles: Record<ChangeType, { bg: string; icon: ReactElement }> = {
  added: { bg: 'bg-emerald-500', icon: <Plus className="w-3 h-3" /> },
  updated: { bg: 'bg-blue-500', icon: <RefreshCw className="w-3 h-3" /> },
  deleted: { bg: 'bg-red-500', icon: <XCircle className="w-3 h-3" /> }
}

const statusIcons: Record<string, ReactElement | null> = {
  pending: null,
  running: <Loader2 className="w-4 h-4 text-blue-500 animate-spin" />,
  completed: <CheckCircle className="w-4 h-4 text-green-500" />,
  failed: <XCircle className="w-4 h-4 text-red-500" />
}

export const StepNode = memo(({ data, selected }: StepNodeProps) => {
  const { id, title, status, changeType, isOrphan, step } = data
  const orphanReuseCount = (data as { orphanReuseCount?: number }).orphanReuseCount ?? 0

  // Check if this is a sub-agent (part of a routing step)
  const isSubAgent = useMemo(() => id.includes('-sub-agent-'), [id])
  const executionMode = effectiveExecutionMode(step)
  const executionModeReason = effectiveExecutionModeReason(step)
  const executionModeVisuals = getExecutionModeVisuals(executionMode, executionModeReason)
  const ModeIcon = executionModeVisuals.Icon
  const statusIcon = statusIcons[status]
  const nodeBorderColor = status === 'pending' && executionModeVisuals.borderClassName
    ? executionModeVisuals.borderClassName
    : statusBorderColors[status]
  const defaultTopHandleColor = isSubAgent ? '!bg-indigo-400 dark:!bg-indigo-600' : '!bg-gray-400 dark:!bg-gray-500'
  const modeHandleColor = executionModeVisuals.handleClassName || defaultTopHandleColor
  const showFooterMetadata = Boolean(statusIcon)
  // Simple title-only cards still say what kind of step they are.
  const label = step?.type ? planStepTypeLabel(step.type) : ''
  const typeLabel = label === 'Agent' || label === 'Script' ? label : ''

  return (
    <div className={`
      relative w-[280px] rounded-xl border-2 bg-white dark:bg-gray-900 shadow-lg
      ${isSubAgent ? 'overflow-visible' : 'overflow-hidden'}
      ${nodeBorderColor}
      ${isSubAgent ? 'border-dashed border-indigo-400 dark:border-indigo-500' : ''}
      ${isOrphan ? 'border-dashed border-amber-400 dark:border-amber-500' : ''}
      ${selected ? 'ring-2 ring-purple-500/60' : ''}
      ${changeType ? changeHighlightStyles[changeType] : ''}
    `}>
      {/* Status badge - positioned at top-right edge (shows for running/failed status) */}
      {status === 'running' && (
        <div className="absolute top-0 right-0 z-10 flex items-center gap-1 px-1.5 py-0.5 rounded-bl-lg rounded-tr-xl bg-blue-500 text-white text-[10px] font-medium shadow-lg">
          <Loader2 className="w-3 h-3 animate-spin" />
          <span>Running</span>
        </div>
      )}
      {status === 'failed' && (
        <div className="absolute top-0 right-0 z-10 flex items-center gap-1 px-1.5 py-0.5 rounded-bl-lg rounded-tr-xl bg-red-500 text-white text-[10px] font-medium shadow-lg">
          <XCircle className="w-3 h-3" />
          <span>Failed</span>
        </div>
      )}
      
      {/* Change badge - positioned below status badge (or top-right if no status badge) */}
      {changeType && (
        <div className={`absolute ${status === 'running' || status === 'failed' ? 'top-6 right-0' : 'top-0 right-0'} z-10 flex items-center gap-1 px-1.5 py-0.5 rounded-bl-lg rounded-tr-xl ${changeBadgeStyles[changeType].bg} text-white text-[10px] font-medium shadow-lg`}>
          {changeBadgeStyles[changeType].icon}
          <span className="capitalize">{changeType}</span>
        </div>
      )}

      {/* Orphan badge (top-left): shows whether this orphan is reused or unused */}
      {isOrphan && (
        <div className="absolute top-0 left-0 z-10 flex items-center gap-1 px-1.5 py-0.5 rounded-br-lg rounded-tl-xl bg-amber-500 text-white text-[10px] font-medium shadow-lg">
          <span>{orphanReuseCount > 0 ? `Orphan · reused ×${orphanReuseCount}` : 'Orphan · unused'}</span>
        </div>
      )}

      <Handle
        type="target"
        position={Position.Top}
        id={isSubAgent ? 'top' : undefined}
        className={`!w-3 !h-3 !border-2 !border-white dark:!border-gray-900 ${modeHandleColor}`}
        style={{ top: '-6px', left: '50%' }}
      />

      {/* Header */}
      <div className="px-4 py-3 bg-gray-50 dark:bg-gray-800/50 border-b border-gray-200 dark:border-gray-700">
        {/* First row: execution indicator and title */}
        <div className="flex items-start gap-3 mb-2">
          {ModeIcon ? (
            <div
              className={`flex items-center justify-center w-8 h-8 rounded-md flex-shrink-0 ${executionModeVisuals.iconBoxClassName}`}
              title={executionModeVisuals.title}
            >
              <ModeIcon className="w-4 h-4" />
            </div>
          ) : isSubAgent ? (
            <div className="flex items-center justify-center w-8 h-8 rounded-md bg-indigo-100 dark:bg-indigo-900/40 text-indigo-700 dark:text-indigo-300 flex-shrink-0" title="Sub-agent">
              <Bot className="w-4 h-4" />
            </div>
          ) : null}
          <div className="flex-1 min-w-0">
            <h3 className="text-sm font-semibold text-gray-900 dark:text-white leading-relaxed">
              {title}
            </h3>
            {typeLabel && (
              <span className="mt-1 inline-block rounded bg-gray-200/70 px-1.5 py-0.5 text-[10px] font-medium text-gray-600 dark:bg-gray-700/60 dark:text-gray-300">
                {typeLabel}
              </span>
            )}
          </div>
        </div>
        {showFooterMetadata && (
          <div className="flex items-center gap-1.5">
            <div className="flex-1" />
            {statusIcon}
          </div>
        )}
      </div>

      <Handle type="source" position={Position.Bottom} className={`!w-3 !h-3 ${modeHandleColor} !border-2 !border-white dark:!border-gray-900`} />
      
      {/* Retry Handle - for validation loop-back (hidden by default) */}
      <Handle
        type="target"
        position={Position.Top}
        id="retry"
        className="!w-2 !h-2 !bg-transparent !border-0"
      />
    </div>
  )
})

StepNode.displayName = 'StepNode'
export default StepNode
