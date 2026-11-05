import React from 'react'
import type { ToolCallStartEvent } from '../../../../generated/event-types'
import { MarkdownRenderer } from '../../../ui/MarkdownRenderer'

interface HumanFeedbackToolCallDisplayProps {
  event: ToolCallStartEvent
}

export const HumanFeedbackToolCallDisplay: React.FC<HumanFeedbackToolCallDisplayProps> = React.memo(({ event }) => {
  const [response, setResponse] = React.useState('')
  const [isSubmitting, setIsSubmitting] = React.useState(false)
  const [error, setError] = React.useState<string | null>(null)
  const [isSubmitted, setIsSubmitted] = React.useState(false)
  const [submittedFeedback, setSubmittedFeedback] = React.useState('')

  // Extract parameters from tool arguments (matches backend human_feedback tool schema)
  // Memoize to avoid re-parsing on every render (prevents cascading re-renders during streaming)
  const toolParams = React.useMemo(() => {
    try {
      if (event.tool_params?.arguments) {
        const args = JSON.parse(event.tool_params.arguments)
        const optionsRaw = args.options
        const options: string[] = []
        if (Array.isArray(optionsRaw)) {
          for (const opt of optionsRaw) {
            if (typeof opt === 'string' && opt) options.push(opt)
          }
        }
        return {
          unique_id: args.unique_id || '',
          message_for_user: args.message_for_user || '',
          session_id: args.session_id,
          options
        }
      }
    } catch (err) {
      console.error('Failed to parse tool arguments:', err)
    }
    return {
      unique_id: '',
      message_for_user: 'Please provide your feedback',
      session_id: undefined,
      options: [] as string[]
    }
  }, [event.tool_params?.arguments])

  const hasMultipleOptions = toolParams.options.length > 0

  const submitFeedback = async (value: string, displayValue?: string) => {
    if (!value.trim()) return
    setIsSubmitting(true)
    setError(null)
    try {
      const { agentApi } = await import('../../../../services/api')
      await agentApi.submitHumanFeedback(toolParams.unique_id, value.trim())
      setSubmittedFeedback(displayValue ?? value.trim())
      setIsSubmitted(true)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to submit feedback')
    } finally {
      setIsSubmitting(false)
    }
  }

  const handleSubmit = async () => {
    if (!response.trim()) {
      setError('Please provide a response')
      return
    }
    await submitFeedback(response.trim())
  }

  const handleApprove = async () => {
    await submitFeedback('Approve', 'Approved')
  }

  const handleOption = async (index: number) => {
    // Send the actual option label text (not "optionN") so the LLM
    // clearly understands which choice the user made.
    const optionLabel = toolParams.options[index] || `option${index}`
    await submitFeedback(optionLabel)
  }

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      if (!isSubmitting && !isSubmitted && response.trim()) {
        handleSubmit()
      }
    }
  }

  // Submitted state — show question + answer
  if (isSubmitted) {
    return (
      <div className="bg-green-50 dark:bg-green-950/30 border border-green-200 dark:border-green-800 rounded-md px-3 py-2 my-2">
        {/* Question */}
        <div className="text-xs text-green-700 dark:text-green-300 mb-1.5">
          <MarkdownRenderer content={toolParams.message_for_user} className="text-xs" />
        </div>
        {/* Answer */}
        <div className="flex items-center gap-2">
          <svg className="w-4 h-4 text-green-600 dark:text-green-400 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
          </svg>
          <span className="text-xs font-medium text-green-800 dark:text-green-200">
            {submittedFeedback}
          </span>
        </div>
      </div>
    )
  }

  // Waiting state
  return (
    <div className="bg-indigo-50 dark:bg-indigo-950/30 border border-indigo-200 dark:border-indigo-800/60 rounded-md px-3 py-2.5 my-2">
      {/* Question text */}
      <div className="text-xs text-indigo-700 dark:text-indigo-300">
        <MarkdownRenderer content={toolParams.message_for_user} className="text-xs" />
      </div>

      {/* Action buttons: option buttons when options provided, else notification status */}
      <div className="flex flex-wrap items-center gap-2 mt-2">
        {hasMultipleOptions ? (
          toolParams.options.map((optionLabel, index) => {
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
                disabled={isSubmitting}
                className={`px-3 py-1.5 ${colorClass} text-white text-xs font-medium rounded transition-colors`}
              >
                {isSubmitting ? 'Processing...' : optionLabel}
              </button>
            )
          })
        ) : (
          null
        )}
      </div>

      {/* Free-text mode: textarea + Approve button (optional input) or Submit Feedback */}
      {!hasMultipleOptions && (
        <div className="mt-2">
          <textarea
            id="feedback-response"
            value={response}
            onChange={(e) => setResponse(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder="Type feedback here if needed... (Enter to submit)"
            className="w-full px-2.5 py-1.5 text-xs border border-gray-200 dark:border-gray-700 rounded bg-white dark:bg-gray-800/80 text-gray-900 dark:text-gray-100 placeholder-gray-400 dark:placeholder-gray-500 focus:ring-1 focus:ring-indigo-500 focus:border-indigo-500 resize-none"
            rows={5}
            disabled={isSubmitting}
          />
          <div className="flex justify-end gap-2 mt-1.5">
            {!response.trim() && (
              <button
                onClick={handleApprove}
                disabled={isSubmitting}
                className="px-3 py-1.5 bg-green-600 hover:bg-green-700 dark:bg-green-700 dark:hover:bg-green-600 text-white text-xs font-medium rounded transition-colors disabled:opacity-50"
              >
                {isSubmitting ? 'Processing...' : 'Submit'}
              </button>
            )}
            {response.trim() && (
              <button
                onClick={handleSubmit}
                disabled={isSubmitting || !response.trim()}
                className="px-3 py-1.5 bg-indigo-600 hover:bg-indigo-700 dark:bg-indigo-700 dark:hover:bg-indigo-600 text-white text-xs font-medium rounded transition-colors disabled:opacity-50"
              >
                {isSubmitting ? 'Submitting...' : 'Submit Feedback'}
              </button>
            )}
          </div>
        </div>
      )}

      {/* Error display */}
      {error && (
        <div className="mt-2 p-2 bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded">
          <p className="text-red-700 dark:text-red-300 text-xs">{error}</p>
        </div>
      )}
    </div>
  )
})
HumanFeedbackToolCallDisplay.displayName = 'HumanFeedbackToolCallDisplay'
