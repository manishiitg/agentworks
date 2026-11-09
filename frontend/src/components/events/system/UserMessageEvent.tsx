import React, { useState } from 'react'
import { ChevronDown, ListTodo } from 'lucide-react'
import type { UserMessageEvent } from '../../../generated/events'
import { askAIDisplayText, hasAskAIMessage } from '../../../utils/askAIMessage'
import { PlainMarkdown } from '../../ui/PlainMarkdown'
import { DeliveryTick } from './DeliveryTick'
import { AgentInboxMessage, agentInboxMessage } from '../../AgentInboxMessage'

interface UserMessageEventDisplayProps {
  event: UserMessageEvent
  mode?: 'compact' | 'detailed'
  /** True when the surrounding terminal header already names this step. */
  hideContext?: boolean
}

export const UserMessageEventDisplay: React.FC<UserMessageEventDisplayProps> = ({
  event,
  mode = 'detailed',
  hideContext = false,
}) => {
  const [isExpanded, setIsExpanded] = useState(false)
  // Auto-notifications are short and are the reason the turn resumed, so they
  // open by default rather than hiding behind a click.
  const [isAutoExpanded, setIsAutoExpanded] = useState(true)
  const CHAR_LIMIT = 300
  // Above this, the prompt is a document rather than a sentence.
  const EXECUTION_PROMPT_AUTO_OPEN_CHARS = 600
  const messageSource = event.metadata?.source
  const isLiveCodingAgentInput = messageSource === 'coding_agent_live_input'
  const hasStepScope = typeof event.metadata?.current_step_id === 'string' ||
    typeof event.metadata?.step_name === 'string'
  const isExecutionPrompt = messageSource === 'execution_prompt' ||
    (!messageSource && event.turn === 0 && hasStepScope)
  const content = event.content || ''
  const isAutoNotification = content.trim().startsWith('[AUTO-NOTIFICATION]')
  const stepName = typeof event.metadata?.step_name === 'string'
    ? event.metadata.step_name.trim()
    : ''

  // Use the same server-attributed card in standard chat and terminal/native
  // transcripts. Never infer an agent identity from someone typing an envelope.
  const incomingAgentMessage = agentInboxMessage(event.metadata as Record<string, unknown> | undefined)
  if (incomingAgentMessage) return <AgentInboxMessage message={incomingAgentMessage} timestamp={typeof event.timestamp === 'string' ? event.timestamp : ''} />

  // Check if content is long enough to need expansion
  const shouldShowExpand = content.length > CHAR_LIMIT

  if (isAutoNotification) {
    const firstLine = content
      .trim()
      .split('\n')
      .find(line => line.trim() && !line.includes('Do NOT call tools')) || content.trim()
    const summary = firstLine
      .replace(/^\[AUTO-NOTIFICATION\]\s*/i, '')
      .replace(/\s*Ack briefly;.*$/i, '')
      .trim()

    // Truncation must be judged on what is actually RENDERED. This compared
    // the full raw content against the limit while rendering the (much
    // shorter) summary, so a one-line notification got an ellipsis it had not
    // earned -- "SUB-AGENT COMPLETION BATCH..." -- plus a Show details link
    // that revealed nothing new.
    const summaryText = summary || 'Notification sent'
    const isSummaryTruncated = summaryText.length > CHAR_LIMIT
    const shownSummary = isAutoExpanded || !isSummaryTruncated
      ? summaryText
      : `${summaryText.substring(0, CHAR_LIMIT)}…`
    // Detail is only worth offering when it says more than the summary.
    const detail = content.trim()
    const hasDetail = detail !== summaryText && detail.replace(/^\[AUTO-NOTIFICATION\]\s*/i, '').trim() !== summaryText

    return (
      <div className="ml-6 border-l-2 border-cyan-300/70 dark:border-cyan-700/70 pl-3 py-1.5">
        <div className="flex items-start gap-2 text-sm">
          <span className="mt-0.5 shrink-0 rounded-sm border border-cyan-200 bg-cyan-50 px-1.5 py-0.5 font-medium text-cyan-700 dark:border-cyan-800 dark:bg-cyan-950/40 dark:text-cyan-300">
            Auto
          </span>
          <div className="min-w-0 flex-1">
            <div className="whitespace-pre-wrap break-words text-slate-700 dark:text-slate-300">
              {shownSummary}
            </div>
            {(hasDetail || isSummaryTruncated) && (
              <button
                onClick={() => setIsAutoExpanded(!isAutoExpanded)}
                className="mt-1 text-xs text-cyan-700 hover:text-cyan-800 dark:text-cyan-400 dark:hover:text-cyan-200"
              >
                {isAutoExpanded ? 'Hide detail' : 'Show detail'}
              </button>
            )}
            {isAutoExpanded && hasDetail && (
              <pre className="mt-2 max-h-96 overflow-auto whitespace-pre-wrap break-words rounded bg-muted/40 px-3 py-2 font-mono text-[12.5px] leading-5 text-muted-foreground">
                {detail}
              </pre>
            )}
          </div>
        </div>
      </div>
    )
  }

  if (hasAskAIMessage(content)) {
    const shown = askAIDisplayText(content)
    return (
      <div className="ml-6 border-l-2 border-violet-300/70 dark:border-violet-700/70 pl-3 py-1.5">
        <div className="flex items-start gap-2 text-sm">
          <span className="mt-0.5 shrink-0 rounded-sm border border-violet-200 bg-violet-50 px-1.5 py-0.5 font-medium text-violet-700 dark:border-violet-800 dark:bg-violet-950/40 dark:text-violet-300">
            Ask AI
          </span>
          <div className="min-w-0 flex-1">
            <div className="whitespace-pre-wrap break-words text-slate-700 dark:text-slate-300">
              {shown}
            </div>
            <button
              onClick={() => setIsExpanded(!isExpanded)}
              className="mt-1 text-xs text-violet-700 hover:text-violet-800 dark:text-violet-400 dark:hover:text-violet-200"
            >
              {isExpanded ? 'Hide detail' : 'Show detail'}
            </button>
            {isExpanded && (
              <pre className="mt-2 max-h-96 overflow-auto whitespace-pre-wrap break-words rounded bg-muted/40 px-3 py-2 font-mono text-[12.5px] leading-5 text-muted-foreground">
                {content.trim()}
              </pre>
            )}
          </div>
        </div>
      </div>
    )
  }

  if (isLiveCodingAgentInput) {
    return (
      <div className="ml-6 flex items-baseline gap-2 py-1 text-xs text-slate-500 dark:text-slate-400">
        <span className="text-slate-400 dark:text-slate-500">↳</span>
        <span className="max-w-full whitespace-pre-wrap break-words text-slate-700 dark:text-slate-200">
          {event.content || 'No message content'}
        </span>
        <DeliveryTick metadata={event.metadata as Record<string, unknown> | undefined} />
      </div>
    )
  }

  if (isExecutionPrompt) {
    return (
      <div
        data-testid="terminal-execution-prompt"
        className="rounded border border-neutral-800 bg-neutral-900/45 px-3 py-2"
      >
        <div className="flex items-start gap-2.5">
          <ListTodo className="mt-0.5 h-4 w-4 shrink-0 text-cyan-400" aria-hidden="true" />
          <div className="min-w-0 flex-1">
            <div className="text-[11px] font-medium uppercase tracking-wide text-neutral-500">Task</div>
            {/* The terminal header already carries the step name with its
                context ("... · Orchestrator · updated 54s ago"). Repeating it
                verbatim one line below made a 55-character title appear twice
                in the same viewport. */}
            {!hideContext && (
              <div className="mt-0.5 break-words text-sm font-medium text-neutral-100">
                {stepName || 'Execute this workflow step'}
              </div>
            )}
          </div>
        </div>
        {/* Open by default: the instructions ARE the task, and hiding the only
            substantive content behind a click made the card a header with
            nothing under it. The "Instructions sent to the agent" caption went
            with it -- it described what is now visible directly below. */}
        {/* Auto-expanded only when short. A step prompt here runs to several
            thousand characters of operational contract; opening that by default
            filled the whole pane and pushed every tool call and reply below the
            fold, so the view led with what we SENT instead of what happened.
            Short prompts still open, because a collapsed one-liner is a header
            with nothing under it. */}
        {content && (
          <details
            open={content.length <= EXECUTION_PROMPT_AUTO_OPEN_CHARS}
            className="group mt-2 border-t border-neutral-800 pt-2"
          >
            <summary className="flex cursor-pointer list-none items-center gap-1.5 text-xs text-neutral-500 hover:text-neutral-300">
              <ChevronDown
                className="h-3.5 w-3.5 transition-transform group-open:rotate-180"
                aria-hidden="true"
              />
              <span className="group-open:hidden">
                Show instructions{content.length > EXECUTION_PROMPT_AUTO_OPEN_CHARS
                  ? ` · ${content.length.toLocaleString()} chars`
                  : ''}
              </span>
              <span className="hidden group-open:inline">Instructions</span>
            </summary>
            {/* Rendered as markdown, not raw text: these prompts are written in
                markdown (## headings, lists, fenced code), and a <pre> showed
                the syntax literally -- "## Orchestrator Instructions" with the
                hashes visible -- which is the hardest possible way to read the
                one thing the card exists to show. */}
            <div className="mt-2 max-h-96 overflow-auto rounded border border-neutral-800/80 bg-black/40 px-3 py-2">
              <PlainMarkdown content={content} />
            </div>
          </details>
        )}
      </div>
    )
  }

  if (mode === 'compact') {
    return (
      <div className="bg-slate-50 dark:bg-slate-800/30 border border-slate-200 dark:border-slate-700 rounded p-2">
        <div className="flex items-start gap-2">
          <span className="text-xs font-bold text-slate-700 dark:text-slate-300">👤</span>
          <div className="flex-1 min-w-0">
            {event.content ? (
              <>
                <div className="text-xs text-slate-900 dark:text-slate-100 leading-tight">
                  {isExpanded || event.content.length <= CHAR_LIMIT
                    ? event.content
                    : `${event.content.substring(0, CHAR_LIMIT)}...`
                  }
                </div>
                {shouldShowExpand && (
                  <button
                    onClick={() => setIsExpanded(!isExpanded)}
                    className="text-xs text-slate-600 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-300 mt-1"
                  >
                    {isExpanded ? '↑ Collapse' : '↓ Expand'}
                  </button>
                )}
              </>
            ) : (
              <div className="text-xs text-red-600 dark:text-red-400 italic">
                No message content
              </div>
            )}
          </div>
        </div>
      </div>
    )
  }

  return (
    <div className="bg-slate-50 dark:bg-slate-800/30 border border-slate-200 dark:border-slate-700 rounded p-2">
      <div className="flex items-start gap-2">
        <span className="text-xs font-bold text-slate-700 dark:text-slate-300">👤</span>
        <div className="flex-1 min-w-0">
          {event.content ? (
            <>
              <div className="text-xs text-slate-900 dark:text-slate-100 leading-tight whitespace-pre-wrap bg-white dark:bg-slate-700/50 rounded p-2 border border-slate-100 dark:border-slate-600">
                {isExpanded || !shouldShowExpand ? event.content : `${event.content.substring(0, CHAR_LIMIT)}...`}
              </div>
              {shouldShowExpand && (
                <button
                  onClick={() => setIsExpanded(!isExpanded)}
                  className="text-xs text-slate-600 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-300 mt-1"
                >
                  {isExpanded ? '↑ Collapse' : '↓ Expand'}
                </button>
              )}
            </>
          ) : (
            <div className="text-xs text-red-600 dark:text-red-400 italic bg-red-50 dark:bg-red-900/30 rounded p-2 border border-red-200 dark:border-red-800">
              No message content
            </div>
          )}

          {event.timestamp && (
            <div className="mt-1 flex items-center gap-1.5 text-xs text-slate-600 dark:text-slate-400">
              {new Date(event.timestamp).toLocaleString()}
              <DeliveryTick metadata={event.metadata as Record<string, unknown> | undefined} />
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
