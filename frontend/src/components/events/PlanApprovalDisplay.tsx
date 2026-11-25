import React, { useState } from 'react'
import { MarkdownRenderer } from '../ui/MarkdownRenderer'

export interface PlanApprovalEventData {
  question?: string
  context?: string
  yes_label?: string
}

interface PlanApprovalDisplayProps {
  event: {
    type: string
    data: PlanApprovalEventData
    timestamp: string
  }
  onSendMessage: (msg: string) => void
}

export const PlanApprovalDisplay: React.FC<PlanApprovalDisplayProps> = ({
  event,
  onSendMessage
}) => {
  const [hasApproved, setHasApproved] = useState(false)

  const context = event.data.context || ''
  const yesLabel = event.data.yes_label || 'Approve & Execute'

  const handleApprove = () => {
    setHasApproved(true)
    onSendMessage('Approved. Execute the plan.')
  }

  // Approved state — compact confirmation + keep plan visible
  if (hasApproved) {
    return (
      <div className="bg-green-50 dark:bg-green-950/30 border border-green-200 dark:border-green-800 rounded-md px-3 py-2 my-2">
        <div className="flex items-center gap-2">
          <svg className="w-4 h-4 text-green-600 dark:text-green-400 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
          </svg>
          <span className="text-xs font-medium text-green-800 dark:text-green-200">
            {yesLabel}
          </span>
        </div>
        {context && (
          <details className="mt-2">
            <summary className="text-[10px] text-green-600 dark:text-green-400 cursor-pointer">Show plan</summary>
            <div className="mt-1 p-3 bg-card border border-border rounded max-h-[500px] overflow-y-auto">
              <MarkdownRenderer content={context} className="text-xs" />
            </div>
          </details>
        )}
      </div>
    )
  }

  // Waiting state — just the approve button + plan content
  return (
    <div className="bg-indigo-50 dark:bg-indigo-950/30 border border-indigo-200 dark:border-indigo-800/60 rounded-md px-3 py-2.5 my-2">
      <div className="flex items-center justify-between gap-3">
        <span className="text-xs text-indigo-700 dark:text-indigo-300">
          Plan is ready for review. Approve or type feedback in the chat.
        </span>
        <button
          onClick={handleApprove}
          className="px-3 py-1.5 bg-success hover:bg-success/90 text-success-foreground text-xs font-medium rounded transition-colors flex-shrink-0"
        >
          {yesLabel}
        </button>
      </div>

      {/* Plan content */}
      {context && (
        <div className="mt-2 p-3 bg-card border border-border rounded max-h-[500px] overflow-y-auto">
          <MarkdownRenderer content={context} className="text-xs" />
        </div>
      )}
    </div>
  )
}
