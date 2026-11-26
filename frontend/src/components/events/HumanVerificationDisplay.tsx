import React from 'react'
import { MarkdownRenderer } from '../ui/MarkdownRenderer'

interface RequestHumanFeedbackEvent {
  objective?: string
  todo_list_markdown?: string
  session_id?: string
  workflow_id?: string
  request_id?: string
  verification_type?: string
  next_phase?: string
  title?: string
  action_label?: string
  action_description?: string
}

interface HumanVerificationDisplayProps {
  event: {
    type: string
    data: RequestHumanFeedbackEvent
    timestamp: string
  }
  onApprove: (requestId: string, eventData?: RequestHumanFeedbackEvent) => void
  onFeedbackSubmitted?: () => void
  isApproving?: boolean  // Loading state
}

export const HumanVerificationDisplay: React.FC<HumanVerificationDisplayProps> = ({
  event,
  onApprove,
  onFeedbackSubmitted,
  isApproving = false
}) => {
  // Use backend-provided content directly
  const title = event.data.title || 'Human Verification Required'
  const description = event.data.action_description
  const buttonText = event.data.action_label || 'Approve & Continue'

  const handleApprove = async () => {
    if (event.data.request_id) {
      // Call onApprove to update workflow status and phase
      await onApprove(event.data.request_id, event.data)
      
      // Call onFeedbackSubmitted callback to re-enable auto-scroll and scroll to bottom
      if (onFeedbackSubmitted) {
        // Use setTimeout to ensure the UI has updated before scrolling
        setTimeout(() => {
          onFeedbackSubmitted()
        }, 100)
      }
    }
  }

  return (
    <div className="bg-indigo-50 dark:bg-indigo-900/20 border border-indigo-200 dark:border-indigo-800 rounded-md p-3 my-2">
      <div className="flex items-start gap-3">
        <div className="flex-1">
          <h3 className="text-sm font-semibold text-indigo-900 dark:text-indigo-100 mb-2">
            {title}
          </h3>
          {description && (
            <div className="text-xs text-indigo-700 dark:text-indigo-300 mb-3">
              <MarkdownRenderer content={description} className="text-xs" />
            </div>
          )}
          
          {event.data.objective && (
            <div className="mb-3">
              <h4 className="text-xs font-medium text-indigo-900 dark:text-indigo-100 mb-1">Objective:</h4>
              <div className="text-xs text-indigo-700 dark:text-indigo-300 bg-indigo-100 dark:bg-indigo-800 p-2 rounded">
                <MarkdownRenderer content={event.data.objective} className="text-xs" />
              </div>
            </div>
          )}

          {event.data.todo_list_markdown && (
            <div className="mb-3">
              <h4 className="text-xs font-medium text-indigo-900 dark:text-indigo-100 mb-1">Generated Todo List:</h4>
              <div className="bg-card border border-indigo-200 dark:border-indigo-700 rounded p-2 max-h-120 overflow-y-auto">
                <MarkdownRenderer 
                  content={event.data.todo_list_markdown}
                  className="text-xs"
                  maxHeight="480px"
                  showScrollbar={true}
                />
              </div>
            </div>
          )}

          <div className="flex justify-end">
            <button
              onClick={handleApprove}
              disabled={isApproving}
              className="px-3 py-1.5 bg-primary hover:bg-primary/90 disabled:bg-primary/50 text-primary-foreground text-xs font-medium rounded transition-colors"
            >
              {isApproving ? '⏳ Processing...' : `✅ ${buttonText}`}
            </button>
          </div>

        </div>
      </div>
    </div>
  )
}