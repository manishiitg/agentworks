import React from 'react'
import type { BatchExecutionCanceledEvent } from '../../../generated/event-types'
import { Ban } from 'lucide-react'

interface BatchExecutionEventProps<T> {
  event: T
  compact?: boolean
}

export const BatchExecutionCanceledEventDisplay: React.FC<BatchExecutionEventProps<BatchExecutionCanceledEvent>> = ({ event, compact }) => {
  return (
    <div className={`border-l-2 border-blue-500 pl-3 py-3 bg-card rounded-r-md ${compact ? 'text-xs' : 'text-sm'}`}>
      <div className="flex items-center gap-2 mb-2">
        <div className="p-1 bg-blue-100 dark:bg-blue-900/20 rounded-md">
          <Ban className="w-4 h-4 text-blue-600 dark:text-blue-400" />
        </div>
        <span className="font-bold text-foreground">
          Batch Execution Canceled
        </span>
      </div>
      
      <div className="p-2.5 bg-muted/50 rounded border border-border mb-2">
        <div className="text-xs text-foreground font-medium mb-1">
          Stopped after {event.completed_groups} of {event.total_groups} groups
        </div>
        {event.canceled_group_name && (
          <div className="text-[10px] text-muted-foreground font-mono">
            Interrupted Group: {event.canceled_group_name}
          </div>
        )}
      </div>
      
      {event.reason && (
        <div className="text-xs text-muted-foreground italic pl-1 border-l-2 border-blue-300 dark:border-blue-600 ml-1 py-0.5">
          "{event.reason}"
        </div>
      )}
    </div>
  )
}
