import React from 'react'
import type { 
  TodoTaskRouteSelectedEvent, 
  TodoTaskStepCompletedEvent 
} from '../../../generated/event-types'

interface TodoTaskEventProps<T> {
  event: T
  compact?: boolean
}

export const TodoTaskRouteSelectedEventDisplay: React.FC<TodoTaskEventProps<TodoTaskRouteSelectedEvent>> = ({ event, compact }) => {
  const [instructionsExpanded, setInstructionsExpanded] = React.useState(false)
  const getActionIcon = (action: string) => {
    switch (action) {
      case 'delegate': return '🤖'
      case 'complete': return '🏁'
      case 'continue': return '📝'
      default: return '❓'
    }
  }

  return (
    <div className={`border-l-2 border-purple-600 pl-3 py-2 bg-purple-50/30 dark:bg-purple-900/10 rounded-r-md ${compact ? 'text-xs' : 'text-sm'}`}>
      <div className="flex items-center justify-between gap-2 mb-1">
        <div className="flex items-center gap-2">
          <span className="text-purple-600 dark:text-purple-400">{getActionIcon(event.next_action || '')}</span>
          <span className="font-bold text-purple-800 dark:text-purple-200 capitalize">
            {event.next_action === 'delegate' ? 'Delegating Task' : 
             event.next_action === 'complete' ? 'Step Finishing' : 'Managing Todos'}
          </span>
        </div>
        {event.iteration && (
          <span className="text-[10px] bg-purple-100 dark:bg-purple-900/50 text-purple-700 dark:text-purple-300 px-1.5 py-0.5 rounded-full">
            Iter {event.iteration}
          </span>
        )}
      </div>

      {event.next_action === 'delegate' && (
        <div className="space-y-1 mt-2">
          <div className="flex items-center gap-2">
            <span className="text-muted-foreground">Agent:</span>
            <span className="font-semibold text-blue-600 dark:text-blue-400">
              {event.use_generic_agent ? 'Generic Agent' : (event.selected_route_name || event.selected_route_id)}
            </span>
          </div>
          {event.preferred_tier_label && (
            <div className="flex items-center gap-2">
              <span className="text-muted-foreground">Tier:</span>
              <span className={`text-[11px] px-1.5 py-0.5 rounded-full font-medium ${
                event.preferred_tier === 1 ? 'bg-purple-100 dark:bg-purple-900/50 text-purple-700 dark:text-purple-300' :
                event.preferred_tier === 2 ? 'bg-blue-100 dark:bg-blue-900/50 text-blue-700 dark:text-blue-300' :
                'bg-green-100 dark:bg-green-900/50 text-green-700 dark:text-green-300'
              }`}>
                {event.preferred_tier_label}
              </span>
            </div>
          )}
          <div className="flex items-center gap-2">
            <span className="text-muted-foreground">Task:</span>
            <span className="font-medium">{event.todo_title || event.todo_id_to_execute}</span>
          </div>
          {event.instructions_to_sub_agent && (
            <div className="mt-2">
              <button
                onClick={() => setInstructionsExpanded(!instructionsExpanded)}
                className="flex items-center gap-1 text-muted-foreground hover:text-purple-600 dark:hover:text-purple-400 text-[11px] font-medium transition-colors"
              >
                <span className={`transform transition-transform ${instructionsExpanded ? 'rotate-90' : ''}`}>▶</span>
                <span>Instructions</span>
              </button>
              {instructionsExpanded && (
                <div className="mt-1 text-foreground bg-card/50 p-2 rounded border border-purple-100 dark:border-purple-900/30 italic text-xs">
                  "{event.instructions_to_sub_agent}"
                </div>
              )}
              {!instructionsExpanded && (
                <div
                  className="mt-1 text-muted-foreground text-[11px] truncate max-w-md cursor-pointer italic"
                  onClick={() => setInstructionsExpanded(true)}
                >
                  "{event.instructions_to_sub_agent}"
                </div>
              )}
            </div>
          )}
        </div>
      )}

      {event.next_action === 'complete' && event.completion_reason && (
        <div className="mt-1 text-muted-foreground italic">
          {event.completion_reason}
        </div>
      )}

      {event.progress_summary && (
        <div className="mt-2 flex items-center gap-2 text-[11px] text-purple-700 dark:text-purple-400 font-medium">
          <span>📊</span>
          <span>{event.progress_summary}</span>
        </div>
      )}
      
      {event.model && !compact && (
        <div className="mt-2 text-[10px] text-muted-foreground flex items-center gap-1">
          <span>🧠</span>
          <span>{event.model}</span>
        </div>
      )}
    </div>
  )
}

export const TodoTaskStepCompletedEventDisplay: React.FC<TodoTaskEventProps<TodoTaskStepCompletedEvent>> = ({ event, compact }) => {
  return (
    <div className={`bg-purple-100 dark:bg-purple-900/30 border border-purple-200 dark:border-purple-800 rounded-lg ${compact ? 'p-2' : 'p-3'}`}>
      <div className="flex items-center gap-3 mb-2">
        <span className="text-xl">🏆</span>
        <div>
          <div className={`${compact ? 'text-xs' : 'text-sm'} font-bold text-purple-900 dark:text-purple-100`}>
            Todo Step Completed
          </div>
          <div className="text-[10px] text-purple-700 dark:text-purple-400">
            {event.completed_count} of {event.total_todos_count} tasks done • {event.total_iterations} iterations
          </div>
        </div>
      </div>
      {event.completion_reason && (
        <div className={`${compact ? 'text-[11px]' : 'text-xs'} text-purple-800 dark:text-purple-200 bg-card/40 p-2 rounded mt-1`}>
          {event.completion_reason}
        </div>
      )}
    </div>
  )
}
