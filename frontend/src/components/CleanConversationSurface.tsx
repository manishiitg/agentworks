import { Fragment, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { Activity, AlertCircle, Bell, Check, Loader2, Sparkles, Wrench } from 'lucide-react'
import type { PollingEvent } from '../services/api-types'
import { buildCleanConversationItems, buildProductionActivityTurns } from '../utils/cleanConversation'
import type { ProductionActivityItem, ProductionActivityTurn } from '../utils/cleanConversation'
import { ConversationMarkdownRenderer } from './ui/MarkdownRenderer'
import { ConversationContinuityNotice } from './ConversationContinuityNotice'
import { CodingAgentQuestionCard } from './CodingAgentQuestionCard'
import { withClosedQuestions } from '../utils/codingAgentQuestions'

// The agent's foreground and background status travel in separate event
// updates. During a hand-off both can briefly read false even though work is
// still underway. Keep the product-facing working row mounted through that
// hand-off so it does not jump in and out of the conversation.
const WORKING_INDICATOR_HIDE_DELAY_MS = 900

export interface CleanConversationSurfaceProps {
  events: PollingEvent[]
  isStreaming: boolean
  isRestoring: boolean
  streamingText: string
  landingContent?: ReactNode
  onRetryLastMessage?: () => void | Promise<void>
  onAnswerCodingAgentQuestion?: (provider: string, promptId: string, answers: Array<{ id: string; selectedLabels: string[]; otherText?: string }>, auto?: boolean) => Promise<void>
  // Prompts the server no longer holds (after a restart): shown closed instead of answerable.
  closedCodingAgentQuestions?: ReadonlySet<string>
}

function messageTime(timestamp?: string): string {
  if (!timestamp) return ''
  const parsed = new Date(timestamp)
  if (!Number.isFinite(parsed.getTime())) return ''
  return parsed.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

function formatTokenCount(tokens: number): string {
  if (tokens >= 1_000_000) return `${(tokens / 1_000_000).toFixed(2).replace(/\.00$/, '')}M`
  if (tokens >= 1_000) return `${(tokens / 1_000).toFixed(1).replace(/\.0$/, '')}k`
  return String(tokens)
}

// These notices are overwhelmingly step completions, so they are labelled as
// such — but a failed background step is delivered through the same channel,
// and labelling that "Step complete" would state the opposite of what happened.
// Keyed off the two markers the backend writes for a failure: `status=failed`
// in the header line, and the "Error: " prefix it puts on the result body
// (buildAutoNotificationMessage, agent_go/cmd/server/background_agents.go).
function isFailedStepNotification(content: string): boolean {
  return /\bstatus=failed\b/i.test(content) || /(^|\n)\s*(- )?Result: Error:/i.test(content)
}

function ProgressUpdate({ content, isStreaming }: { content: string; isStreaming: boolean }) {
  const [open, setOpen] = useState(isStreaming)

  useEffect(() => {
    setOpen(isStreaming)
  }, [isStreaming])

  return (
    <details
      className="w-full rounded-xl border border-dashed border-slate-300 bg-slate-50/70 px-4 py-3 text-slate-600 dark:border-slate-700 dark:bg-slate-900/45 dark:text-slate-300"
      open={open}
      onToggle={(event) => setOpen(event.currentTarget.open)}
      data-testid="clean-progress-message"
    >
      <summary className="flex cursor-pointer list-none select-none items-center gap-2 text-[11px] font-semibold uppercase tracking-wide text-slate-500 dark:text-slate-400 [&::-webkit-details-marker]:hidden">
        {isStreaming ? <Loader2 className="h-3.5 w-3.5 animate-spin text-violet-400" /> : <Activity className="h-3.5 w-3.5" />}
        <span>Progress update</span>
      </summary>
      <div className="mt-2 border-l-2 border-slate-200 pl-3 text-sm leading-6 text-slate-600 dark:border-slate-700 dark:text-slate-300">
        <ConversationMarkdownRenderer content={content} maxHeight="none" framed={false} />
      </div>
    </details>
  )
}

// One turn's tool activity. Rendered per turn rather than once for the whole
// conversation: the summary used to describe only the newest turn, so sending
// a message erased the record of what had just been done on the user's behalf.
function ProductionActivityDetails({ items }: { items: ProductionActivityItem[] }) {
  if (items.length === 0) return null
  return (
          // Plain text, no card: a bordered box per turn read as a UI element
          // competing with the conversation, for information nobody reads unless
          // they explicitly ask. Closed by default even while streaming — with a
          // workflow in flight this list can run to dozens of calls.
          <details className="w-full" aria-label="Production activity" data-testid="clean-production-activity">
            <summary className="flex cursor-pointer list-none items-center gap-1 select-none text-[11px] text-slate-500 hover:text-slate-300 [&::-webkit-details-marker]:hidden">
              <Wrench className="h-3 w-3" />
              <span>+{items.length} {items.length === 1 ? 'tool' : 'tools'}</span>
            </summary>
            <div className="mt-1.5 space-y-1 pl-4 text-[11px] text-slate-500">
              {items.map((activityItem) => (
                <div key={activityItem.id} className="flex items-start gap-1.5" data-status={activityItem.status}>
                  <span className="mt-0.5 shrink-0">
                    {activityItem.status === 'complete' ? <Check className="h-3 w-3 text-emerald-500" /> : activityItem.status === 'error' ? <AlertCircle className="h-3 w-3 text-red-500" /> : <Loader2 className="h-3 w-3 animate-spin text-violet-500" />}
                  </span>
                  <div className="min-w-0 flex-1">
                    <span className="block truncate">{activityItem.title}</span>
                    {activityItem.arguments || activityItem.result ? (
                      <details className="mt-1 text-slate-500">
                        <summary className="cursor-pointer select-none text-violet-400">Developer details</summary>
                        {activityItem.arguments ? <><p className="mt-1 text-slate-500">Arguments</p><pre className="mt-0.5 max-h-48 overflow-auto whitespace-pre-wrap break-words font-mono leading-4">{activityItem.arguments}</pre></> : null}
                        {activityItem.result ? <><p className="mt-1 text-slate-500">Result</p><pre className="mt-0.5 max-h-48 overflow-auto whitespace-pre-wrap break-words font-mono leading-4">{activityItem.result}</pre></> : null}
                      </details>
                    ) : null}
                  </div>
                </div>
              ))}
            </div>
          </details>
  )
}

export function CleanConversationSurface({
  events,
  isStreaming,
  isRestoring,
  streamingText,
  landingContent,
  onRetryLastMessage,
  onAnswerCodingAgentQuestion,
  closedCodingAgentQuestions,
}: CleanConversationSurfaceProps) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const [retryingFailureId, setRetryingFailureId] = useState<string | null>(null)
  const [showWorkingIndicator, setShowWorkingIndicator] = useState(isStreaming)
  const items = useMemo(() => buildCleanConversationItems(events), [events])
  const activityTurns = useMemo(() => buildProductionActivityTurns(events), [events])

  useEffect(() => {
    if (isStreaming) {
      setShowWorkingIndicator(true)
      return
    }

    const hideTimer = window.setTimeout(
      () => setShowWorkingIndicator(false),
      WORKING_INDICATOR_HIDE_DELAY_MS,
    )
    return () => window.clearTimeout(hideTimer)
  }, [isStreaming])

  // Each turn's activity renders after that turn's messages and before the
  // next thing the user typed, so a new message no longer takes the previous
  // turn's work off screen. Work can also precede the first human message on
  // a restored conversation, which is the turn with no anchor.
  const { rows, trailingActivity } = useMemo(() => {
    const byAnchor = new Map(activityTurns.filter((turn) => turn.anchorId).map((turn) => [turn.anchorId, turn]))
    let pending = activityTurns.find((turn) => !turn.anchorId) ?? null
    const built = items.map((item) => {
      let activityBefore: ProductionActivityTurn | null = null
      if (item.role === 'user') {
        activityBefore = pending
        pending = byAnchor.get(item.id) ?? null
      }
      return { item, activityBefore }
    })
    return { rows: built, trailingActivity: pending }
  }, [items, activityTurns])

  useEffect(() => {
    const element = scrollRef.current
    if (!element) return
    const closeToBottom = element.scrollHeight - element.scrollTop - element.clientHeight < 240
    if (closeToBottom || isStreaming) element.scrollTop = element.scrollHeight
  }, [items.length, isStreaming, streamingText])

  if (isRestoring) {
    return (
      <div className="grid h-full place-items-center text-sm text-slate-500" data-testid="clean-conversation-restoring">
        <span className="flex items-center gap-2"><Loader2 className="h-4 w-4 animate-spin text-violet-500" /> Restoring conversation…</span>
      </div>
    )
  }

  if (items.length === 0 && !isStreaming) return <>{landingContent}</>

  return (
    <div ref={scrollRef} className="h-full overflow-y-auto overscroll-contain px-4 py-6 sm:px-6" aria-live="polite" data-testid="clean-conversation-surface">
      <div className="flex w-full flex-col gap-6">
        {rows.map(({ item, activityBefore }) => (
          <Fragment key={`row:${item.id}`}>
            {activityBefore ? <ProductionActivityDetails items={activityBefore.items} /> : null}
            {item.role === 'continuity' ? (
          <ConversationContinuityNotice content={item.content} timestamp={messageTime(item.timestamp)} />
        ) : item.role === 'user' ? (
          <article key={item.id} className="ml-auto max-w-full rounded-2xl rounded-br-md bg-violet-600 px-4 py-3 text-sm leading-6 text-white shadow-sm" data-testid="clean-user-message">
            <p className="whitespace-pre-wrap break-words">{item.content}</p>
          </article>
        ) : item.role === 'reasoning' ? (
          <details key={item.id} className="w-full rounded-2xl border border-violet-900/70 bg-slate-900/80 px-4 py-3 text-slate-200 shadow-sm" open={isStreaming} data-testid="clean-reasoning-message">
            <summary className="cursor-pointer select-none text-xs font-semibold text-violet-300">Thinking</summary>
            <p className="mt-2 whitespace-pre-wrap break-words text-sm leading-6 text-slate-300">{item.content}</p>
          </details>
        ) : item.role === 'progress' ? (
          <ProgressUpdate key={item.id} content={item.content} isStreaming={isStreaming} />
        ) : item.role === 'background' ? (
          <div key={item.id} className="flex items-start gap-2 text-xs text-cyan-700 dark:text-cyan-300" data-testid="clean-background-task-update">
            <Activity className="mt-0.5 h-3.5 w-3.5 shrink-0" />
            <span className="whitespace-pre-wrap break-words">{item.content}</span>
          </div>
        ) : item.role === 'question' && item.codingAgentQuestion ? (
          <CodingAgentQuestionCard prompt={withClosedQuestions(item.codingAgentQuestion, closedCodingAgentQuestions)} onAnswer={onAnswerCodingAgentQuestion} />
        ) : item.role === 'notification' ? (
          // An automatic update the runtime delivered to the agent (a background
          // step finishing), not something the user typed or the agent said. It
          // is shown rather than hidden so a reply that arrives with no prompt
          // has a visible cause. Full width with its own label, because the
          // payload is a multi-line status report — the centered pill this used
          // to be was built for a one-line notice and wrapped badly.
          // Collapsed by default: the body is orchestration bookkeeping written
          // for the agent (raw `iter=`/`step=`/`item=` context plus the step's
          // STATUS contract line), not prose for a video creator. The header
          // alone answers the question this card exists to answer — why the
          // agent just spoke — and the detail stays one click away.
          <details key={item.id} className="w-full rounded-xl border border-cyan-200/70 bg-cyan-50/60 px-4 py-3 dark:border-cyan-900/60 dark:bg-cyan-950/20" data-testid="clean-notification-message">
            <summary className="flex cursor-pointer select-none items-center gap-1.5 text-[10px] font-semibold uppercase tracking-wide text-cyan-700 dark:text-cyan-300">
              <Bell className="h-3 w-3 shrink-0" />
              {isFailedStepNotification(item.content) ? 'Step failed' : 'Step complete'}
            </summary>
            {/* The payload is authored as markdown (bold step names, `code`
                paths, bullet lists when several steps land together), so render
                it rather than printing the syntax literally. */}
            <div className="mt-2 text-xs leading-5 text-slate-600 dark:text-slate-300">
              <ConversationMarkdownRenderer content={item.content} maxHeight="none" framed={false} />
            </div>
          </details>
        ) : (
          <article key={item.id} className="flex w-full items-start gap-3" role={item.role === 'error' ? 'alert' : undefined} data-testid={item.role === 'error' ? 'clean-error-message' : 'clean-assistant-message'}>
            <span className={`mt-0.5 grid h-8 w-8 shrink-0 place-items-center rounded-xl ${item.role === 'error' ? 'bg-red-50 text-red-600 dark:bg-red-950/50 dark:text-red-300' : 'bg-violet-100 text-violet-700 dark:bg-violet-950 dark:text-violet-300'}`}>
              {item.role === 'error' ? <AlertCircle className="h-4 w-4" /> : <Sparkles className="h-4 w-4" />}
            </span>
            <div className={`min-w-0 flex-1 rounded-2xl rounded-tl-md border px-4 py-3 shadow-sm ${item.role === 'error' ? 'border-red-200 bg-red-50 text-red-800 dark:border-red-900 dark:bg-red-950/30 dark:text-red-200' : 'border-slate-200 bg-white text-slate-700 dark:border-slate-800 dark:bg-slate-900 dark:text-slate-200'}`}>
              {item.role === 'assistant' ? (
                <ConversationMarkdownRenderer content={item.content} maxHeight="none" framed={false} />
              ) : item.failure ? (
                <div className="space-y-2">
                  <p className="text-sm font-semibold leading-5">{item.failure.title}</p>
                  <p className="whitespace-pre-wrap text-sm leading-6">{item.content}</p>
                  <div className="flex flex-wrap items-center gap-2 pt-1">
                    {item.failure.actionUrl ? (
                      <a
                        href={item.failure.actionUrl}
                        target="_blank"
                        rel="noreferrer"
                        className="rounded-lg border border-red-300 bg-white px-3 py-1.5 text-xs font-semibold text-red-700 transition hover:bg-red-100 dark:border-red-800 dark:bg-red-950/30 dark:text-red-200 dark:hover:bg-red-950/60"
                      >
                        {item.failure.actionLabel || 'Open provider'}
                      </a>
                    ) : null}
                    {item.failure.retryable && onRetryLastMessage ? (
                      <button
                        type="button"
                        className="rounded-lg border border-red-300 bg-white px-3 py-1.5 text-xs font-semibold text-red-700 transition hover:bg-red-100 disabled:cursor-not-allowed disabled:opacity-60 dark:border-red-800 dark:bg-red-950/30 dark:text-red-200 dark:hover:bg-red-950/60"
                        disabled={isStreaming || retryingFailureId === item.id}
                        onClick={() => {
                          setRetryingFailureId(item.id)
                          void Promise.resolve(onRetryLastMessage())
                            .catch(() => undefined)
                            .finally(() => setRetryingFailureId(null))
                        }}
                      >
                        {retryingFailureId === item.id ? 'Retrying…' : 'Retry'}
                      </button>
                    ) : null}
                    {item.failure.technicalDetails ? (
                      <details className="text-xs text-red-700/80 dark:text-red-300/80">
                        <summary className="cursor-pointer select-none">Technical details</summary>
                        <pre className="mt-2 max-h-40 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-red-100/70 p-2 font-mono text-[11px] leading-4 dark:bg-black/20">{item.failure.technicalDetails}</pre>
                      </details>
                    ) : null}
                  </div>
                </div>
              ) : (
                <p className="whitespace-pre-wrap text-sm leading-6">{item.content}</p>
              )}
              {(item.role === 'assistant' && item.usage) || messageTime(item.timestamp) ? (
                <div className={`mt-3 flex flex-wrap items-center justify-between gap-x-3 gap-y-1 pt-2 text-[10px] font-medium text-slate-400 ${item.role === 'assistant' && item.usage ? 'border-t border-slate-100 dark:border-slate-800' : ''}`}>
                  {item.role === 'assistant' && item.usage ? (
                    <p className="flex flex-wrap gap-x-3 gap-y-1" data-testid="clean-assistant-usage">
                      <span>Input {formatTokenCount(item.usage.inputTokens)}</span>
                      <span>Output {formatTokenCount(item.usage.outputTokens)}</span>
                      {item.usage.cacheTokens > 0 ? <span>Cache {formatTokenCount(item.usage.cacheTokens)}</span> : null}
                      {item.usage.isEstimated ? <span>Estimated</span> : null}
                    </p>
                  ) : <span />}
                  {messageTime(item.timestamp) ? <time>{messageTime(item.timestamp)}</time> : null}
                </div>
              ) : null}
            </div>
          </article>
            )}
          </Fragment>
        ))}

        {trailingActivity ? <ProductionActivityDetails items={trailingActivity.items} /> : null}

        {showWorkingIndicator && !streamingText.trim() ? (
          <div className="flex items-center gap-2 text-xs text-slate-400" role="status" aria-live="polite" data-testid="clean-working-indicator">
            <Loader2 className="h-3.5 w-3.5 animate-spin text-violet-400" />
            <span>Working…</span>
          </div>
        ) : null}

        {showWorkingIndicator && streamingText.trim() ? (
          <article className="flex w-full items-start gap-3" data-testid="clean-working-message">
            <span className="mt-0.5 grid h-8 w-8 shrink-0 place-items-center rounded-xl bg-violet-100 text-violet-700 dark:bg-violet-950 dark:text-violet-300"><Sparkles className="h-4 w-4" /></span>
            <div className="min-w-0 flex-1 rounded-2xl rounded-tl-md border border-slate-800 bg-slate-900 px-4 py-3 shadow-sm">
              <div className="flex items-center gap-2 text-xs font-semibold text-slate-200">
                <Loader2 className="h-3.5 w-3.5 animate-spin text-violet-400" />
                Preparing response
              </div>
              <div className="mt-2 text-sm leading-6 text-slate-200" data-testid="clean-streaming-text">
                <ConversationMarkdownRenderer content={streamingText} maxHeight="none" framed={false} />
              </div>
            </div>
          </article>
        ) : null}
      </div>
    </div>
  )
}
