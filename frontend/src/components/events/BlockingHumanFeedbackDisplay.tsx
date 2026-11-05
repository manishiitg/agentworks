import React, { useMemo, useState } from 'react'
import { MarkdownRenderer } from '../ui/MarkdownRenderer'
import {
  hasSubmittedFeedback,
  markFeedbackSubmitted,
} from '../../utils/notificationDedup'
import { isHumanFeedbackRequestResolved } from '../../utils/humanFeedbackAttention'
import { useChatStore } from '../../stores/useChatStore'

export interface BlockingHumanFeedbackEvent {
  question?: string
  allow_feedback?: boolean
  context?: string
  session_id?: string
  workflow_id?: string
  request_id?: string
  yes_no_only?: boolean
  yes_label?: string
  no_label?: string
  options?: string[] // Array of option labels for multiple choice
  routed_to_parent_chat?: boolean // Legacy field; direct UI submission is now always used
}

interface BlockingHumanFeedbackDisplayProps {
  event: {
    type: string
    data: BlockingHumanFeedbackEvent
    timestamp: string
  }
  onApprove: (requestId: string, eventData?: BlockingHumanFeedbackEvent & { feedback?: string }) => void
  onSubmitFeedback?: (requestId: string, feedback: string) => void
  onFeedbackSubmitted?: () => void
  isApproving?: boolean  // Loading state
  surfaceNotifications?: boolean
}

export const BlockingHumanFeedbackDisplay: React.FC<BlockingHumanFeedbackDisplayProps> = ({
  event,
  onApprove,
  onSubmitFeedback,
  onFeedbackSubmitted,
  isApproving = false,
  surfaceNotifications = true,
}) => {
  const cachedSubmission = event.data.request_id ? hasSubmittedFeedback(event.data.request_id) : false
  const [feedback, setFeedback] = useState<string>('')
  const [isSubmittingFeedback, setIsSubmittingFeedback] = useState(false)
  const [submittedLocally, setHasSubmitted] = useState(cachedSubmission)
  // The local cache only knows this browser's answers; the durable
  // human_feedback_resolved marker covers other devices and refreshes.
  // Subscribe to the events map itself so composer keystrokes (tab config
  // writes) don't rescan every event; recompute only when events change.
  const tabEvents = useChatStore(state => state.tabEvents)
  const resolvedByServer = useMemo(
    () => isHumanFeedbackRequestResolved(tabEvents, event.data.request_id || ''),
    [tabEvents, event.data.request_id],
  )
  const hasSubmitted = submittedLocally || resolvedByServer
  const [submitError, setSubmitError] = useState('')
  const electronAPI = (window as unknown as {
    electronAPI?: { setDockBadge?: (badge: string) => void }
  }).electronAPI

  // Use backend-provided content directly
  const question = event.data.question || 'Do you want to continue?'
  const context = event.data.context || ''
  const yesNoOnly = event.data.yes_no_only || false
  const options = event.data.options || []
  const hasMultipleOptions = options.length > 0
  const yesLabel = event.data.yes_label || 'Approve'
  const noLabel = event.data.no_label || ''

  // Keep the Electron dock badge in sync with the pending request.
  React.useEffect(() => {
    if (!surfaceNotifications) return
    if (!electronAPI?.setDockBadge) return
    electronAPI.setDockBadge(hasSubmitted ? '' : '1')
    return () => electronAPI.setDockBadge?.('')
  }, [electronAPI, hasSubmitted, surfaceNotifications])

  const triggerScrollCallback = () => {
    if (onFeedbackSubmitted) {
      setTimeout(() => onFeedbackSubmitted(), 100)
    }
  }

  const handleSubmitFeedback = async () => {
    if (event.data.request_id && feedback.trim() && onSubmitFeedback) {
      setIsSubmittingFeedback(true)
      setSubmitError('')
      try {
        await onSubmitFeedback(event.data.request_id, feedback.trim())
        if (event.data.request_id) markFeedbackSubmitted(event.data.request_id)
        setHasSubmitted(true)
        setFeedback('')
        triggerScrollCallback()
      } catch (error) {
        console.error('Failed to submit feedback:', error)
        setSubmitError('The response could not be delivered. The request may have expired; please retry if it is still active.')
      } finally {
        setIsSubmittingFeedback(false)
      }
    }
  }

  const handleApprove = async () => {
    if (event.data.request_id) {
      setIsSubmittingFeedback(true)
      setSubmitError('')
      try {
        if (onSubmitFeedback) {
          await onSubmitFeedback(event.data.request_id, "Approve")
        } else {
          onApprove(event.data.request_id, { ...event.data, feedback: "Approve" })
        }
        if (event.data.request_id) markFeedbackSubmitted(event.data.request_id)
        setHasSubmitted(true)
        triggerScrollCallback()
      } catch (error) {
        console.error('Failed to approve:', error)
        setSubmitError('The response could not be delivered. The request may have expired; please retry if it is still active.')
      } finally {
        setIsSubmittingFeedback(false)
      }
    }
  }

  const handleReject = async () => {
    if (event.data.request_id) {
      setIsSubmittingFeedback(true)
      setSubmitError('')
      try {
        if (onSubmitFeedback) {
          await onSubmitFeedback(event.data.request_id, "Reject")
        } else {
          onApprove(event.data.request_id, { ...event.data, feedback: "Reject" })
        }
        if (event.data.request_id) markFeedbackSubmitted(event.data.request_id)
        setHasSubmitted(true)
        triggerScrollCallback()
      } catch (error) {
        console.error('Failed to reject:', error)
        setSubmitError('The response could not be delivered. The request may have expired; please retry if it is still active.')
      } finally {
        setIsSubmittingFeedback(false)
      }
    }
  }

  const handleOption = async (index: number) => {
    if (event.data.request_id && onSubmitFeedback) {
      setIsSubmittingFeedback(true)
      setSubmitError('')
      try {
        // Send the actual option label text so the LLM clearly understands the user's choice
        const optionLabel = options[index] || `option${index}`
        await onSubmitFeedback(event.data.request_id, optionLabel)
        if (event.data.request_id) markFeedbackSubmitted(event.data.request_id)
        setHasSubmitted(true)
        triggerScrollCallback()
      } catch (error) {
        console.error(`Failed to select option ${index}:`, error)
        setSubmitError('The response could not be delivered. The request may have expired; please retry if it is still active.')
      } finally {
        setIsSubmittingFeedback(false)
      }
    }
  }

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      if (!isSubmittingFeedback && !hasSubmitted && feedback.trim()) {
        handleSubmitFeedback()
      }
    }
  }

  // Submitted state — show question + answer
  if (hasSubmitted) {
    return (
      <div className="bg-green-50 dark:bg-green-950/30 border border-green-200 dark:border-green-800 rounded-md px-3 py-2 my-2">
        {/* Question */}
        <div className="text-xs text-green-700 dark:text-green-300 mb-1.5">
          <MarkdownRenderer content={question} className="text-xs" />
        </div>
        {/* Answer */}
        <div className="flex items-center gap-2">
          <svg className="w-4 h-4 text-green-600 dark:text-green-400 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
          </svg>
          <span className="text-xs font-medium text-green-800 dark:text-green-200">Response submitted</span>
        </div>
        {context && (
          <details className="mt-2 group">
            <summary className="text-[10px] text-green-600 dark:text-green-400 cursor-pointer font-medium flex items-center gap-1">
              <span className="group-open:hidden">+ Show details</span>
              <span className="hidden group-open:inline">− Hide details</span>
            </summary>
            <div className="mt-1.5 p-3 bg-white dark:bg-gray-800/60 border border-gray-200 dark:border-gray-700 rounded max-h-[500px] overflow-y-auto">
              <MarkdownRenderer content={context} className="text-xs" />
            </div>
          </details>
        )}
      </div>
    )
  }

  // Waiting state
  return (
    <div className="bg-indigo-50 dark:bg-indigo-950/30 border border-indigo-200 dark:border-indigo-800/60 rounded-md px-3 py-2.5 my-2">
      {/* Question text */}
      <div className="text-xs text-indigo-700 dark:text-indigo-300">
        <MarkdownRenderer content={question} className="text-xs" />
      </div>

      {/* Action Buttons */}
      <div className="flex flex-wrap items-center gap-2 mt-2">
          {hasMultipleOptions ? (
            // Multiple-choice mode
            options.map((optionLabel, index) => {
              const colorClasses = [
                'bg-blue-600 hover:bg-blue-700 dark:bg-blue-700 dark:hover:bg-blue-600 disabled:opacity-50',
                'bg-indigo-600 hover:bg-indigo-700 dark:bg-indigo-700 dark:hover:bg-indigo-600 disabled:opacity-50',
                'bg-green-600 hover:bg-green-700 dark:bg-green-700 dark:hover:bg-green-600 disabled:opacity-50',
                'bg-purple-600 hover:bg-purple-700 dark:bg-purple-700 dark:hover:bg-purple-600 disabled:opacity-50',
              ]
              const colorClass = colorClasses[index % colorClasses.length]

              return (
                <button
                  key={index}
                  onClick={() => handleOption(index)}
                  disabled={isApproving || isSubmittingFeedback}
                  className={`px-3 py-1.5 ${colorClass} text-white text-xs font-medium rounded transition-colors`}
                >
                  {isSubmittingFeedback ? 'Processing...' : optionLabel}
                </button>
              )
            })
          ) : yesNoOnly ? (
            // Yes/No mode — hide reject button when noLabel is empty
            <>
              {noLabel && (
                <button
                  onClick={handleReject}
                  disabled={isApproving || isSubmittingFeedback}
                  className="px-3 py-1.5 bg-gray-200 hover:bg-gray-300 dark:bg-gray-700 dark:hover:bg-gray-600 text-gray-700 dark:text-gray-200 text-xs font-medium rounded transition-colors disabled:opacity-50"
                >
                  {isSubmittingFeedback ? 'Processing...' : noLabel}
                </button>
              )}
              <button
                onClick={handleApprove}
                disabled={isApproving || isSubmittingFeedback}
                className="px-3 py-1.5 bg-green-600 hover:bg-green-700 dark:bg-green-700 dark:hover:bg-green-600 text-white text-xs font-medium rounded transition-colors disabled:opacity-50"
              >
                {isApproving || isSubmittingFeedback ? 'Processing...' : yesLabel}
              </button>
            </>
          ) : null}
      </div>

      {/* Feedback textarea + buttons — below the main row, only in normal mode */}
      {!yesNoOnly && !hasMultipleOptions && (
        <div className="mt-2">
          <textarea
            id="feedback-input"
            value={feedback}
            onChange={(e) => setFeedback(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder="Type feedback here if changes are needed... (Enter to submit)"
            className="w-full px-2.5 py-1.5 text-xs border border-gray-200 dark:border-gray-700 rounded bg-white dark:bg-gray-800/80 text-gray-900 dark:text-gray-100 placeholder-gray-400 dark:placeholder-gray-500 focus:ring-1 focus:ring-indigo-500 focus:border-indigo-500 resize-none"
            rows={5}
            disabled={isApproving || isSubmittingFeedback}
          />
          <div className="flex justify-end gap-2 mt-1.5">
            {!feedback.trim() && (
              <button
                onClick={handleApprove}
                disabled={isApproving || isSubmittingFeedback}
                className="px-3 py-1.5 bg-green-600 hover:bg-green-700 dark:bg-green-700 dark:hover:bg-green-600 text-white text-xs font-medium rounded transition-colors disabled:opacity-50"
              >
                {isApproving ? 'Processing...' : yesLabel || 'Approve & Continue'}
              </button>
            )}
            {feedback.trim() && (
              <button
                onClick={handleSubmitFeedback}
                disabled={isSubmittingFeedback || isApproving || !feedback.trim()}
                className="px-3 py-1.5 bg-indigo-600 hover:bg-indigo-700 dark:bg-indigo-700 dark:hover:bg-indigo-600 text-white text-xs font-medium rounded transition-colors disabled:opacity-50"
              >
                {isSubmittingFeedback ? 'Submitting...' : 'Submit Feedback'}
              </button>
            )}
          </div>
        </div>
      )}

      {/* Context / Plan content — collapsible toggle */}
      {context && (
        <details className="mt-2 group">
          <summary className="text-[10px] text-indigo-600 dark:text-indigo-400 cursor-pointer font-medium flex items-center gap-1">
            <span className="group-open:hidden">+ Show details</span>
            <span className="hidden group-open:inline">− Hide details</span>
          </summary>
          <div className="mt-1.5 p-3 bg-white dark:bg-gray-800/60 border border-gray-200 dark:border-gray-700 rounded max-h-[500px] overflow-y-auto">
            <MarkdownRenderer content={context} className="text-xs" />
          </div>
        </details>
      )}
      {submitError && (
        <p className="mt-2 rounded border border-red-200 bg-red-50 px-2 py-1.5 text-xs text-red-700 dark:border-red-900 dark:bg-red-950/40 dark:text-red-300" role="alert">
          {submitError}
        </p>
      )}
    </div>
  )
}
