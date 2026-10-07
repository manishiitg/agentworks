import { useRareWorkingTip } from './chat/useRareWorkingTip'
import { CodingAgentQuestionCard } from './CodingAgentQuestionCard'
import { codingAgentQuestionCards, withClosedQuestions, type CodingAgentQuestionAnswerHandler } from '../utils/codingAgentQuestions'
import { AgentRuntimeActivityIndicator } from './AgentRuntimeActivityIndicator'
import type { ChatRuntimeActivity } from '../utils/chatRuntimeActivity'
import React, { memo, createContext, useContext, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Virtuoso, type VirtuosoHandle } from 'react-virtuoso'
import { prependedIndex, transcriptReadingState, useTranscriptScroll, type TranscriptReadingState } from './useTranscriptScroll'
import { Bot, CheckCircle2, ChevronDown, ChevronRight, CircleDashed, XCircle } from 'lucide-react'
import { EventDispatcher } from './events/EventDispatcher'
import { ConversationMarkdownRenderer } from './ui/MarkdownRenderer'
import { normalizeProductChatFailure } from '../platform/chat/productChatFailure'
import {
  buildTranscriptItems,
  collapseTurnFailures,
  turnFailureText,
  internalTranscriptMessageTitle,
  isExecutionPromptTranscriptMessage,
  isInternalTranscriptMessage,
  pairToolCalls,
  runActivity,
  shouldCollapseTranscriptUserMessage,
  type PairedToolCall,
  selectTerminalEvents,
  type TranscriptItem,
} from '../utils/terminalEventTranscript'
import { withToolCallVisibility } from '../utils/toolCallVisibility'
import { formatDurationCompact } from '../utils/duration'
import { liveUsageSummary, type LiveUsageSummary } from './terminalUsage'
import { formatToolCallArguments, formatToolCallResult } from '../utils/toolCallFormatting'
import type { PollingEvent, TerminalSnapshot } from '../services/api-types'
import { parseProductInteraction, type ProductInteraction } from '../../shared/session/interactions'
import { ConversationContinuityNotice, isConversationContinuityNotice } from './ConversationContinuityNotice'
import { DeliveryFailedResend, DeliveryTick } from './events/system/DeliveryTick'
import { deliveryTickState } from './events/system/deliveryTickState'
import { QueuedProviderLabel } from './chat/ProviderChangeNotice'
import { askAIDisplayText, hasAskAIMessage } from '../utils/askAIMessage'
import { isChatDeliveryTelemetryEvent, recordChatDeliveryTelemetry } from '../utils/chatDeliveryTelemetry'
import { ShowFullContentButton, chatArtifactRef, useChatArtifactText } from './ChatArtifactExpander'
import { WebSearchToolCallDisplay } from './events/tools/ToolCallSpecialRender/WebSearchToolCallDisplay'
import { isWebSearchToolCall } from '../utils/webSearchToolCall'

// Message text sizes multiply --chat-scale (default 1), so a product can offer
// a bigger reading size (SparkQuill's Child Mode "T" button sets it on the
// chat section) without the transcript knowing about the control.
type TranscriptRenderItem = TranscriptItem | {
  kind: 'live'
  key: string
  text: string
  status: string
}

type TranscriptHistoryContext = {
  hasOlder: boolean
  loadingOlder: boolean
  onLoadOlder: () => void
  activity?: ChatRuntimeActivity
  usage?: LiveUsageSummary
}

// Keep history navigation inside the virtual scroller. A fixed sibling header
// can flash during tab hydration and change the viewport while rows settle.
const TranscriptHistoryHeader = ({ context }: { context?: TranscriptHistoryContext }) => {
  if (!context?.hasOlder && !context?.loadingOlder) return null
  return <div className="flex justify-center px-3 py-1.5 text-[11px]" data-testid="transcript-history-header">
    <button type="button" onClick={context.onLoadOlder} disabled={context.loadingOlder}
      className="rounded px-2 py-0.5 text-muted-foreground hover:bg-muted hover:text-foreground disabled:cursor-wait disabled:opacity-60">
      {context.loadingOlder ? 'Loading earlier messages…' : 'Load earlier messages'}
    </button>
  </div>
}

// The agent's working state sits under the last message, inside the scroller, so it is visible while
// reading the end of a very long reply (at the top of the turn it scrolled out of sight). The row keeps
// the same height whether or not the agent is working, so it appearing never moves the list.
const ACTIVITY_TEXT: Record<string, string> = {
  running: 'Working…',
  'background running': 'Background agent running…',
  'waiting for input': 'Waiting for your input',
}
// The right side of the same row carries the coding CLI's live context fill and
// a plan-limit warning (PLAT-554). Both are single truncating lines inside the
// fixed h-7 row, so updates never change the list height.
const TranscriptActivityFooter = ({ context }: { context?: TranscriptHistoryContext }) => {
  const activity = context?.activity
  const working = activity !== undefined && activity.state !== 'ready'
  const tip = useRareWorkingTip(working && activity?.label === 'running')
  const usage = context?.usage
  return <div data-testid="transcript-activity-footer" className="flex h-7 min-w-0 items-center gap-2 px-3 text-xs text-muted-foreground">
    {working && <>
      <AgentRuntimeActivityIndicator state={activity.state} label={activity.label} />
      <span className="truncate">{ACTIVITY_TEXT[activity.label] ?? activity.label}</span>
      {tip && <span data-testid="working-tip" className="truncate text-[11px] text-muted-foreground/70 animate-in fade-in duration-700">· Tip: {tip}</span>}
    </>}
    {(usage?.contextText || usage?.warning) && <div className="ml-auto flex min-w-0 shrink items-center gap-2">
      {usage.warning && <span data-testid="transcript-usage-warning" className="truncate font-medium text-amber-500" title="Plan usage is high">
        {usage.warning.text}
      </span>}
      {usage.contextText && <span data-testid="transcript-context-meter" className="flex shrink-0 items-center gap-1.5" title={usage.contextTitle}>
        {usage.contextPercent !== undefined && <span className="h-1.5 w-12 overflow-hidden rounded-full bg-muted" aria-hidden="true">
          <span className={`block h-full rounded-full ${usage.contextPercent >= 85 ? 'bg-amber-500' : 'bg-cyan-500/70'}`} style={{ width: `${Math.max(2, usage.contextPercent)}%` }} />
        </span>}
        <span className="tabular-nums">{usage.contextText}</span>
      </span>}
    </div>}
  </div>
}
const transcriptComponents = { Header: TranscriptHistoryHeader, Footer: TranscriptActivityFooter }

// Clean view = the SAME rich event components the tree used, laid out as one
// flat chronological conversation for a single terminal.
//
// The rail is the hierarchy now: every agent and sub-agent owns its own
// terminal entry, so parent/child nesting inside a transcript is redundant.
// What lived in EventHierarchy (tree layout, parent resolution, owned log
// panels) is deliberately NOT reproduced — only its two load-bearing
// behaviours are: virtualization, and collapsing consecutive tool calls.
//
// This replaces a renderer that parsed terminal TEXT into synthesized rows.
// That approach could not reuse the event components, so user messages
// rendered raw and every tool call degraded to an anonymous line.
//
// Selection/grouping logic lives in utils/terminalEventTranscript.ts so it can
// be unit-tested without pulling React in.

// ONE card per tool call — not one per event.
//
// A single call arrives as two events and the transcript used to draw a card
// for each. That was worse than verbose, it was misleading: the start event
// never carries arguments, so its "Arguments: (no arguments)" section was
// permanently empty, while the end event held both the arguments and the
// result behind a disclosure. The reader saw two boxes, the useful one closed.
//
// This renders the pair as one thing — identity from the start, args + result
// from the end — and deliberately does NOT nest the old per-event cards, which
// is what produced triplicated server names, boxes inside boxes, and a scroll
// container fighting itself.
const PREVIEW_LIMIT = 600
const AGENT_RESPONSE_EVENT_TYPES = new Set([
  'agent_end',
  'background_agent_completed',
  'llm_generation_end',
  'orchestrator_agent_end',
  'unified_completion',
])

function transcriptEventPayload(event: PollingEvent): Record<string, unknown> {
  const outer = event.data
  if (!outer || typeof outer !== 'object') return {}
  const nested = (outer as { data?: unknown }).data
  return nested && typeof nested === 'object'
    ? nested as Record<string, unknown>
    : outer as Record<string, unknown>
}

function assistantResponseText(event: PollingEvent): string {
  if (!AGENT_RESPONSE_EVENT_TYPES.has(event.type || '')) return ''
  const payload = transcriptEventPayload(event)
  const content = typeof payload.content === 'string' ? payload.content.trim() : ''
  const finalResult = typeof payload.final_result === 'string' ? payload.final_result.trim() : ''
  const result = typeof payload.result === 'string' ? payload.result.trim() : ''
  return content || finalResult || result
}

// The streamed text stays on screen for a moment after the turn ends, and the finished reply is
// added as a normal row meanwhile. Showing both doubled a long answer, then collapsed it: a big
// jump. When the finished reply already says what the live text says, the live row is dropped, so
// the swap happens inside one frame.
export function liveTextAlreadyCommitted(items: TranscriptRenderItem[], liveText: string): boolean {
  const live = liveText.replace(/\s+/g, ' ').trim().toLowerCase()
  if (!live) return false
  for (let index = items.length - 1; index >= 0; index--) {
    const item = items[index]
    if (item.kind !== 'event') continue
    if (item.event.type === 'user_message') return false
    const answer = assistantResponseText(item.event).replace(/\s+/g, ' ').trim().toLowerCase()
    if (!answer) continue
    return answer === live || answer.startsWith(live) || live.startsWith(answer)
  }
  return false
}

function presentationActivity(event: PollingEvent): { label: string; title: string; destination: string; detail: string } | null {
	if (event.type !== 'presentation_updated') return null
	const payload = transcriptEventPayload(event)
	const title = typeof payload.title === 'string' && payload.title.trim()
		? payload.title.trim()
		: 'Production item'
	const activity = payload.activity && typeof payload.activity === 'object'
		? payload.activity as Record<string, unknown>
		: {}
	// New events always provide these values from product.yaml. The neutral
	// fallback keeps old persisted events readable without recreating a
	// kind-to-panel map in the frontend.
	const label = typeof activity.label === 'string' && activity.label.trim() ? activity.label.trim() : 'Production update'
	const destination = typeof activity.destination === 'string' && activity.destination.trim() ? activity.destination.trim() : 'Production panel'
	const detail = typeof activity.detail === 'string' && activity.detail.trim() ? activity.detail.trim() : 'Updated'
	return { label, title, destination, detail }
}

// A final response can be represented by two different protocol events during
// a restore (for example `llm_generation_end` plus `unified_completion`). The
// event selector normally removes that pair, but a mixed live/history tail can
// still carry both. The conversation should never make the reader see the
// identical reply twice, so retain just the first adjacent response card.
function removeAdjacentDuplicateAssistantResponses(items: TranscriptItem[]): TranscriptItem[] {
  let previousAnswer = ''
  return items.filter((item) => {
    if (item.kind !== 'event') return true
    if (item.event.type === 'user_message') {
      previousAnswer = ''
      return true
    }
    const answer = assistantResponseText(item.event)
    if (!answer) return true
    const comparable = answer.replace(/\s+/g, ' ').trim().toLowerCase()
    if (comparable && comparable === previousAnswer) return false
    previousAnswer = comparable
    return true
  })
}

function transcriptTimestamp(event: PollingEvent): string {
  const payload = transcriptEventPayload(event)
  const rawTimestamp = event.timestamp || (typeof payload.timestamp === 'string' ? payload.timestamp : '')
  return rawTimestamp && Number.isFinite(Date.parse(rawTimestamp))
    ? new Date(rawTimestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
    : ''
}

// The event the transcript renders as the agent's reply for a turn.
function isAgentResponseEvent(event: PollingEvent): boolean {
  return AGENT_RESPONSE_EVENT_TYPES.has(event.type || '') && Boolean(assistantResponseText(event))
}

const TranscriptEvent: React.FC<{
  event: PollingEvent
  onSendMessage?: (msg: string) => void
  onRetryLastMessage?: () => void | Promise<void>
  /** Resends a user message whose delivery failed. */
  onResendMessage?: (msg: string) => void
  compactUserBottom?: boolean
  /** Rendered inside a turn block that already draws the border and header. */
  inTurn?: boolean
  renderInteraction?: (interaction: ProductInteraction, event: PollingEvent) => React.ReactNode
  assistantLabel?: string
  assistantIcon?: React.ReactNode
  /** The turn's clock is shown elsewhere or not at all; draw no time on this row. */
  hideTimestamp?: boolean
}> = ({ event, onSendMessage, onRetryLastMessage, onResendMessage, compactUserBottom = false, inTurn = false, renderInteraction, assistantLabel, assistantIcon, hideTimestamp = false }) => {
  if (event.type === 'product_interaction') {
    const interaction = parseProductInteraction(event)
    return interaction && renderInteraction ? <>{renderInteraction(interaction, event)}</> : null
  }
  const payload = transcriptEventPayload(event)
  const content = typeof payload.content === 'string' ? payload.content.trim() : ''
  const timestamp = hideTimestamp ? '' : transcriptTimestamp(event)

  const run = runActivity(event)
  if (run) return <RunActivityEvent {...run} timestamp={timestamp} />

  // A failed turn: one card, in plain words, with the raw error behind a
  // disclosure. collapseTurnFailures already reduced the server's three
  // failure events to the last one.
  const failureText = turnFailureText(event)
  if (failureText) {
    return <TurnFailureMessage failure={normalizeProductChatFailure(failureText, failureHints(payload))} timestamp={timestamp} onRetry={onRetryLastMessage} />
  }

  const presentation = presentationActivity(event)
  if (presentation) {
    return <PresentationActivityEvent {...presentation} timestamp={timestamp} />
  }

  if (isInternalTranscriptMessage(event)) {
    return <InternalActivityEvent title={internalTranscriptMessageTitle(event)} content={content} timestamp={timestamp} />
  }

  // Different runtime transports carry a completed agent answer in different
  // fields.  They are all agent responses, so render them with one component
  // and one type scale instead of falling through to several event cards.
  const finalResult = typeof payload.final_result === 'string' ? payload.final_result.trim() : ''
  const result = typeof payload.result === 'string' ? payload.result.trim() : ''
  const responseContent = content || finalResult || result
  if (AGENT_RESPONSE_EVENT_TYPES.has(event.type || '') && responseContent) {
    return <AssistantTranscriptMessage event={event} content={responseContent} timestamp={timestamp} framed={!inTurn} label={assistantLabel} icon={assistantIcon} />
  }

  if (isExecutionPromptTranscriptMessage(event)) {
    return <EventDispatcher event={event} onSendMessage={onSendMessage} compact hideOrchestratorContext />
  }

  if (event.type !== 'user_message') {
    return <EventDispatcher event={event} onSendMessage={onSendMessage} compact hideOrchestratorContext />
  }

  const metadata = payload.metadata && typeof payload.metadata === 'object'
    ? payload.metadata as Record<string, unknown>
    : undefined
  // The server keeps the typed text beside a wrapped prompt (continuity
  // notice), so the user's own message renders instead of the wrapper.
  const displayContent = typeof metadata?.display_content === 'string' ? metadata.display_content.trim() : ''
  if (isConversationContinuityNotice(content)) {
    const notice = <ConversationContinuityNotice content={content} timestamp={timestamp} />
    if (!displayContent) return notice
    return <>{notice}<UserTranscriptMessage content={displayContent} timestamp={timestamp} metadata={metadata} compactBottom={compactUserBottom} onResend={onResendMessage} /></>
  }
  return <UserTranscriptMessage content={displayContent || content || 'Message'} timestamp={timestamp} metadata={metadata} compactBottom={compactUserBottom} onResend={onResendMessage} />
}

const USER_MESSAGE_PREVIEW_LIMIT = 480

const UserTranscriptMessage: React.FC<{ content: string; timestamp: string; metadata?: Record<string, unknown>; compactBottom?: boolean; onResend?: (text: string) => void }> = ({ content, timestamp, metadata, compactBottom = false, onResend }) => {
  // Ask AI blocks collapse to their plain-words request; the builder-only
  // instructions stay one click away behind the usual expansion toggle.
  const askAI = hasAskAIMessage(content)
  const collapsible = askAI || shouldCollapseTranscriptUserMessage(content)
  const [expanded, setExpanded] = useState(false)
  const shown = expanded
    ? content
    : askAI
      ? askAIDisplayText(content)
      : collapsible
        ? `${content.slice(0, USER_MESSAGE_PREVIEW_LIMIT).trimEnd()}…`
        : content
  // The receipt row appears for a timestamped row or a row carrying a
  // delivery verdict; plain untimestamped rows render exactly as before.
  const showReceipt = Boolean(timestamp) || deliveryTickState(metadata) !== null

  if (!collapsible) {
    return (
      <div className={`ml-auto mt-2 max-w-[84%] text-right ${compactBottom ? 'mb-1' : 'mb-2'}`}>
        <div className="whitespace-pre-wrap break-words text-[length:calc(14px*var(--chat-scale,1))] leading-[calc(20px*var(--chat-scale,1))] text-foreground">{shown}</div>
        {showReceipt && (
          <div className="mt-0.5 flex flex-wrap items-center justify-end gap-x-2 text-[10px] leading-4 text-muted-foreground">
            <DeliveryFailedResend metadata={metadata} text={content} onResend={onResend} />
            {timestamp && <span className="tabular-nums">{timestamp}</span>}
            <span className="inline-flex items-center gap-1"><QueuedProviderLabel metadata={metadata} /><DeliveryTick metadata={metadata} /></span>
          </div>
        )}
      </div>
    )
  }

  return (
    <article className={`ml-auto mt-4 w-[min(92%,52rem)] rounded-lg border border-border bg-muted/30 px-4 py-3 text-left ${compactBottom ? 'mb-1' : 'mb-4'}`}>
      <div className="whitespace-pre-wrap break-words text-[length:calc(13px*var(--chat-scale,1))] leading-[calc(24px*var(--chat-scale,1))] text-foreground/90">{shown}</div>
      <div className="mt-1 flex items-center gap-2">
        <button
          type="button"
          onClick={() => setExpanded(value => !value)}
          className="text-[11px] font-medium text-muted-foreground transition-colors hover:text-foreground"
        >
          {expanded ? 'Show less' : 'Show full message'}
        </button>
        <div className="ml-auto flex flex-wrap items-center justify-end gap-x-2 text-[10px] leading-4 text-muted-foreground">
          <DeliveryFailedResend metadata={metadata} text={content} onResend={onResend} />
          {timestamp && <span className="tabular-nums">{timestamp}</span>}
          <span className="inline-flex items-center gap-1"><QueuedProviderLabel metadata={metadata} /><DeliveryTick metadata={metadata} /></span>
        </div>
      </div>
    </article>
  )
}

// The turn's header line: who spoke, turn, duration, time. It sits at the top
// of the agent's block, which starts at the turn's first tool call when there
// is one, so tool work reads as part of the reply rather than a stray chip.
const AssistantTurnHeader = memo(function AssistantTurnHeader({ event, timestamp, label = 'Agent', icon }: { event?: PollingEvent; timestamp: string; label?: string; icon?: React.ReactNode }) {
  const fields = event ? transcriptEventPayload(event) : {}
  // Only a reply event's duration describes the turn. When the header falls
  // back to the block's first tool call, that event's duration is one shell
  // command (e.g. "281ms" on a 92-second turn), so say nothing rather than
  // something misleading. The backend's zero-based `turn` counter is an
  // internal index that disagrees between event types for the same reply
  // ("Turn 0" on tool events, "Turn 1" on llm_generation_end); it never
  // meant anything to a reader and is not shown.
  const duration = event && AGENT_RESPONSE_EVENT_TYPES.has(event.type || '') && typeof fields.duration === 'number' && fields.duration > 0
    ? formatDurationCompact(fields.duration)
    : ''
  const metadata = [duration, timestamp].filter(Boolean).join(' · ')
  // The turn is identified by its mark, not an all-caps word: the default
  // agent gets the Bot glyph alone, while a product override (e.g. Quill's
  // logo + name) keeps its explicit label beside its mark.
  const mark = icon ?? <Bot className="h-4 w-4" aria-hidden="true" />
  return (
    <div data-testid="terminal-clear-assistant-header" aria-label={label} className="mb-2 flex items-center gap-2 text-[10px] font-semibold uppercase tracking-[0.12em] text-muted-foreground">
      <span className="inline-flex h-4 w-4 shrink-0 items-center justify-center text-muted-foreground [&>img]:h-4 [&>img]:w-4 [&>svg]:h-4 [&>svg]:w-4" aria-hidden="true">{mark}</span>
      {label !== 'Agent' && <span>{label}</span>}
      {metadata && <>
        <span className="h-1 w-1 rounded-full bg-muted-foreground/60" />
        <span className="normal-case font-medium tracking-normal text-muted-foreground">{metadata}</span>
      </>}
    </div>
  )
})

// Keep the shared AgentWorks/Work conversation rail close to the pane edge.
// The row already supplies horizontal padding, so a second full padding step
// made every assistant turn look unnecessarily inset.
const AGENT_BLOCK_CLASS = 'pl-1 pr-1'

const DisclosureContext = createContext<Map<string, boolean> | null>(null)
function useDisclosure(key: string, initial = false) {
  const cache = useContext(DisclosureContext)
  const [value, setValue] = useState(() => cache?.get(key) ?? initial)
  const toggle = useCallback(() => setValue(previous => {
    const next = !previous
    cache?.set(key, next)
    return next
  }), [cache, key])
  return [value, toggle] as const
}

// Where an item sits in its agent turn. A turn is everything between two user
// messages: tool batches, thoughts, replies, presentation and activity rows.
type TurnSlot = { agent: boolean; first: boolean; last: boolean; header?: PollingEvent; showTime: boolean }

// Agent turns share one clock: the first turn, and any turn more than this
// long after the previous labelled one. User rows always carry their own
// time next to the delivery receipt.
const TIME_LABEL_GAP_MS = 5 * 60 * 1000

function itemTime(item: TranscriptRenderItem | undefined): number {
  if (!item || item.kind === 'live') return NaN
  const event = item.kind === 'event' ? item.event : item.events[0]
  if (!event) return NaN
  const payload = transcriptEventPayload(event)
  return Date.parse(event.timestamp || (typeof payload.timestamp === 'string' ? payload.timestamp : ''))
}

function isUserItem(item: TranscriptRenderItem | undefined): boolean {
  return item?.kind === 'event' && item.event.type === 'user_message'
}

function buildTurnSlots(data: TranscriptRenderItem[]): TurnSlot[] {
  const slots: TurnSlot[] = []
  let inTurn = false
  let lastLabelled = NaN
  const decideTime = (item: TranscriptRenderItem): boolean => {
    const at = itemTime(item)
    if (!Number.isFinite(at)) return false
    if (Number.isFinite(lastLabelled) && at - lastLabelled < TIME_LABEL_GAP_MS) return false
    lastLabelled = at
    return true
  }
  data.forEach((item, index) => {
    if (isUserItem(item)) {
      inTurn = false
      // User rows always show their time; still advance the label clock so
      // agent-turn grouping behaves exactly as before.
      decideTime(item)
      slots.push({ agent: false, first: false, last: false, showTime: item.kind !== 'live' })
      return
    }
    const first = !inTurn
    inTurn = true
    const next = data[index + 1]
    slots.push({ agent: true, first, last: !next || isUserItem(next), showTime: first && decideTime(item) })
  })
  // The header carries the turn's reply metadata (turn, duration, time): the
  // turn's first reply, or its first event while there is no reply yet.
  slots.forEach((slot, index) => {
    if (!slot.first) return
    for (let j = index; j < data.length && slots[j]?.agent; j++) {
      const item = data[j]
      if (item.kind === 'event' && isAgentResponseEvent(item.event)) {
        slot.header = item.event
        return
      }
    }
    const item = data[index]
    slot.header = item.kind === 'event' ? item.event : item.kind === 'live' ? undefined : item.events[0]
  })
  return slots
}

const ASSISTANT_ARTIFACT_KEYS = ['content', 'final_result', 'result'] as const

const AssistantTranscriptMessage: React.FC<{ event: PollingEvent; content: string; timestamp: string; label?: string; icon?: React.ReactNode; framed?: boolean }> = ({ event, content, timestamp, label = 'Agent', icon, framed = true }) => {
  const artifact = useMemo(() => chatArtifactRef(event), [event])
  const full = useChatArtifactText(artifact, ASSISTANT_ARTIFACT_KEYS)
  return (
    <article data-testid="terminal-clear-assistant-message" className={framed ? `my-4 ${AGENT_BLOCK_CLASS}` : 'py-1'}>
      {framed && <AssistantTurnHeader event={event} timestamp={timestamp} label={label} icon={icon} />}
      <div className="[&_li]:!text-[length:calc(14px*var(--chat-scale,1))] [&_p]:!text-[length:calc(14px*var(--chat-scale,1))] [&_li]:!leading-[calc(24px*var(--chat-scale,1))] [&_p]:!leading-[calc(24px*var(--chat-scale,1))]">
        <ConversationMarkdownRenderer content={full.text || content} framed={false} maxHeight="none" />
      </div>
      {artifact && !full.text && (
        <ShowFullContentButton ref_={artifact} loading={full.loading} error={full.error} onLoad={() => { void full.load() }} label="Show full response" />
      )}
    </article>
  )
}

function failureHints(payload: Record<string, unknown>): { code?: unknown; provider?: unknown; retryAt?: unknown; technicalDetails?: unknown } {
  const metadata = payload.metadata && typeof payload.metadata === 'object' ? payload.metadata as Record<string, unknown> : {}
  const error = payload.error && typeof payload.error === 'object' ? payload.error as Record<string, unknown> : {}
  return {
    code: payload.code ?? payload.error_kind ?? payload.kind ?? error.code ?? metadata.code ?? metadata.error_kind,
    provider: payload.provider ?? error.provider ?? metadata.provider,
    retryAt: payload.retry_at ?? payload.retryAt ?? error.retry_at ?? error.retryAt ?? metadata.retry_at ?? metadata.retryAt,
    technicalDetails: payload.technical_details ?? payload.technicalDetails ?? error.technical_details ?? error.technicalDetails ?? metadata.technical_details ?? metadata.technicalDetails,
  }
}

const TurnFailureMessage: React.FC<{ failure: ReturnType<typeof normalizeProductChatFailure>; timestamp: string; onRetry?: () => void | Promise<void> }> = ({ failure, timestamp, onRetry }) => {
  const [open, setOpen] = useState(false)
  const [retrying, setRetrying] = useState(false)
  // A cancel is something the person did, not a fault: one quiet line, not a full error card.
  if (failure.code === 'cancelled') {
    return (
      <div data-testid="terminal-clear-turn-failure" className="my-1 flex items-center gap-2 text-[11px] text-muted-foreground">
        <XCircle className="h-3 w-3 shrink-0" />
        <span>{failure.title}</span>
        {timestamp && <span className="tabular-nums">{timestamp}</span>}
        {failure.retryable && onRetry && (
          <button
            type="button"
            disabled={retrying}
            className="underline-offset-2 hover:text-foreground hover:underline disabled:opacity-50"
            onClick={async () => {
              setRetrying(true)
              try { await onRetry() } finally { setRetrying(false) }
            }}
          >
            {retrying ? 'Retrying…' : 'Retry'}
          </button>
        )}
      </div>
    )
  }
  return (
    <article data-testid="terminal-clear-turn-failure" className="my-3 rounded-xl border border-destructive/30 bg-destructive/5 px-4 py-3">
      <div className="flex items-start gap-2">
        <XCircle className="mt-0.5 h-4 w-4 shrink-0 text-destructive" />
        <div className="min-w-0 flex-1">
          <div className="flex items-baseline gap-2">
            <span className="text-[length:calc(13px*var(--chat-scale,1))] font-semibold text-foreground">{failure.title}</span>
            {timestamp && <span className="ml-auto shrink-0 text-[11px] tabular-nums text-muted-foreground">{timestamp}</span>}
          </div>
          <p className="mt-1 text-[length:calc(13px*var(--chat-scale,1))] leading-relaxed text-muted-foreground">{failure.message}</p>
          {failure.actionUrl && (
            <a
              href={failure.actionUrl}
              target="_blank"
              rel="noreferrer"
              className="mt-2 inline-flex rounded-md border border-border px-2 py-1 text-xs font-medium text-foreground hover:bg-muted"
            >
              {failure.actionLabel || 'Open provider'}
            </a>
          )}
          {failure.retryable && onRetry && (
            <button
              type="button"
              disabled={retrying}
              className="mt-2 rounded-md border border-border px-2 py-1 text-xs hover:bg-muted disabled:opacity-50"
              onClick={async () => {
                setRetrying(true)
                try { await onRetry() } finally { setRetrying(false) }
              }}
            >
              {retrying ? 'Retrying…' : 'Retry message'}
            </button>
          )}
          {failure.technicalDetails && (
            <>
              <button
                type="button"
                onClick={() => setOpen(value => !value)}
                aria-expanded={open}
                className="mt-2 flex items-center gap-1 text-[11px] text-muted-foreground transition-colors hover:text-foreground"
              >
                {open ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
                Technical details
              </button>
              {open && (
                <pre className="mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-muted/60 p-3 text-[11px] leading-snug text-muted-foreground">{failure.technicalDetails}</pre>
              )}
            </>
          )}
        </div>
      </div>
    </article>
  )
}

const InternalActivityEvent: React.FC<{ title: string; content: string; timestamp: string }> = ({ title, content, timestamp }) => {
  const [open, setOpen] = useState(false)
  return (
    <div data-testid="terminal-clear-system-activity" className="my-3 border-y border-border/60 py-2">
      <button
        type="button"
        onClick={() => setOpen(value => !value)}
        aria-expanded={open}
        className="flex w-full items-center gap-2 text-left text-[11px] text-muted-foreground transition-colors hover:text-foreground"
      >
        <CircleDashed className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
        <span className="truncate">Automation update · {title}</span>
        {timestamp && <span className="ml-auto shrink-0 tabular-nums text-muted-foreground">{timestamp}</span>}
        {open ? <ChevronDown className="h-3.5 w-3.5 shrink-0" /> : <ChevronRight className="h-3.5 w-3.5 shrink-0" />}
      </button>
      {open && (
        // These are agent-written notes — headings, bullets, bold, inline code.
        // A <pre> showed the raw markup: "### Close-out", "**step-x**", stray
        // backticks. Render it the way the same text renders in a reply.
        <div className="mt-2 max-h-56 overflow-auto rounded-lg bg-muted/60 p-3 [&_li]:!text-[12px] [&_p]:!text-[12px]">
          <ConversationMarkdownRenderer content={content} framed={false} maxHeight="none" />
        </div>
      )}
    </div>
  )
}

const PresentationActivityEvent: React.FC<{ label: string; title: string; destination: string; detail: string; timestamp: string }> = ({ label, title, destination, detail, timestamp }) => (
  // Token-driven so it reads on light and dark products alike: the earlier
  // violet-on-violet palette hid the title on a light surface.
  <div data-testid="terminal-clear-presentation-activity" className="my-3 rounded-lg border border-border/60 bg-muted/40 px-3 py-2">
    <div className="flex items-center gap-2 text-[11px] text-muted-foreground">
      <CircleDashed className="h-3.5 w-3.5 shrink-0 text-primary/70" />
      <span className="truncate"><span className="font-medium text-foreground/85">{label}</span> · <span className="text-foreground">{title}</span></span>
      <span className="hidden shrink-0 sm:inline">{detail} in {destination}</span>
      {timestamp && <span className="ml-auto shrink-0 tabular-nums">{timestamp}</span>}
    </div>
  </div>
)

// Running a step, the workflow, or an evaluation is the headline action of a
// workflow turn, so it gets the same compact activity row a presentation gets
// rather than disappearing into a collapsed "N tool calls" chip.
const RunActivityEvent: React.FC<{ label: string; target: string; state: 'started' | 'failed'; timestamp: string }> = ({ label, target, state, timestamp }) => (
  <div data-testid="terminal-clear-run-activity" className="my-3 rounded-lg border border-border/60 bg-muted/40 px-3 py-2">
    <div className="flex items-center gap-2 text-[11px] text-muted-foreground">
      {state === 'failed'
        ? <CircleDashed className="h-3.5 w-3.5 shrink-0 text-red-500" aria-hidden="true" />
        : <CircleDashed className="h-3.5 w-3.5 shrink-0 text-primary/70" aria-hidden="true" />}
      <span className="truncate">
        <span className="font-medium text-foreground/85">{label}</span>
        {target && <> · <span className="text-foreground">{target}</span></>}
      </span>
      <span className="hidden shrink-0 sm:inline">
        {state === 'failed' ? 'could not start' : 'started'}
      </span>
      {timestamp && <span className="ml-auto shrink-0 tabular-nums">{timestamp}</span>}
    </div>
  </div>
)

const TOOL_ARTIFACT_KEYS = ['result', 'output', 'error'] as const

const ToolCallCard: React.FC<{ pair: PairedToolCall }> = ({ pair }) => {
  const [open, toggleOpen] = useDisclosure(`tool:${pair.key}`)
  const artifact = useMemo(() => {
    for (const event of pair.events) {
      const ref = chatArtifactRef(event)
      if (ref) return ref
    }
    return null
  }, [pair.events])
  const fullOutput = useChatArtifactText(artifact, TOOL_ARTIFACT_KEYS)
  const hasDetail = Boolean(pair.args || pair.result)
  const resultFormatting = useMemo(
    () => pair.result ? formatToolCallResult(pair.result) : null,
    [pair.result],
  )
  const displayStatus = resultFormatting?.isError ? 'error' : pair.status

  const mark = displayStatus === 'error' ? '✗' : displayStatus === 'ok' ? '✓' : '⋯'
  const markClass =
    displayStatus === 'error' ? 'text-red-400' : displayStatus === 'ok' ? 'text-emerald-400' : 'text-muted-foreground'
  // Shared formatter rather than a local one: the local copy assumed milliseconds
  // while the wire value is nanoseconds, and it had no minutes branch, so long
  // calls printed absurd second counts.
  const duration =
    pair.durationNs != null && pair.durationNs > 0 ? formatDurationCompact(pair.durationNs) : null

  return (
    <div
      data-testid="terminal-clear-tool-call"
      className={`rounded border ${
        displayStatus === 'error'
          ? 'border-red-300/70 bg-red-50/70 dark:border-red-900/80 dark:bg-red-950/20'
          : 'border-border/70 bg-card'
      }`}
    >
      <button
        type="button"
        onClick={toggleOpen}
        aria-expanded={open}
        className="flex w-full items-center gap-2 px-2 py-1.5 text-left text-xs hover:bg-muted/60"
      >
        <span className={`shrink-0 font-mono ${markClass}`}>{mark}</span>
        <span className="truncate font-medium text-foreground">{pair.name}</span>
        {pair.server && (
          <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">
            {pair.server}
          </span>
        )}
        {duration && <span className="shrink-0 tabular-nums text-[10px] text-muted-foreground">{duration}</span>}
        {displayStatus === 'error' && (
          <span className="shrink-0 rounded bg-red-100/80 px-1.5 py-0.5 text-[10px] text-red-700 dark:bg-red-950 dark:text-red-300">
            failed
          </span>
        )}
        {open
          ? <ChevronDown className="ml-auto h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-label="Hide tool details" />
          : <ChevronRight className="ml-auto h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-label="Show tool details" />}
      </button>

      {open && (
        <div className="space-y-2 border-t border-border/70 px-2 py-2">
          {pair.args && <ToolCallField label="Arguments" value={pair.args} />}
          {(fullOutput.text || pair.result) && <ToolCallField label="Output" value={fullOutput.text || pair.result || ''} />}
          {artifact && !fullOutput.text && (
            <ShowFullContentButton ref_={artifact} loading={fullOutput.loading} error={fullOutput.error} onLoad={() => { void fullOutput.load() }} label="Load full output" />
          )}
          {!hasDetail && (
            <p className="text-[11px] leading-5 text-muted-foreground">
              This coding-CLI call did not retain its arguments or output. New calls will include them once the bridge trace update is running.
            </p>
          )}
        </div>
      )}
    </div>
  )
}

// Long args/results must scroll INSIDE their own box. Letting them size the
// card is what broke scrolling once a tool was opened: a multi-KB result grew
// the row past the viewport and took the transcript's scroll with it.
const ToolCallField: React.FC<{ label: string; value: string }> = ({ label, value }) => {
  const [full, setFull] = useState(false)
  const formatted = useMemo(
    () => label === 'Arguments' ? formatToolCallArguments(value) : formatToolCallResult(value),
    [label, value],
  )
  const isLong = formatted.text.length > PREVIEW_LIMIT
  const shown = full || !isLong ? formatted.text : `${formatted.text.slice(0, PREVIEW_LIMIT)}…`
  return (
    <div className="min-w-0">
      <div className="mb-0.5 flex items-center gap-1.5 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
        <span>{label}</span>
        {formatted.format !== 'text' && (
          <span className="rounded bg-muted px-1 py-0.5 text-[9px] tracking-normal text-muted-foreground">
            {formatted.format}
          </span>
        )}
        {formatted.isError && (
          <span className="rounded bg-red-100 px-1 py-0.5 text-[9px] tracking-normal text-red-700 dark:bg-red-950 dark:text-red-300">
            error
          </span>
        )}
      </div>
      <pre className={`max-h-64 overflow-auto whitespace-pre-wrap break-words rounded border p-2 text-[11px] leading-5 ${
        formatted.isError
          ? 'border-red-200 bg-red-50 text-red-800 dark:border-red-900/70 dark:bg-red-950/30 dark:text-red-200'
          : 'border-border/60 bg-muted/40 text-foreground/90'
      }`}>
        {shown}
      </pre>
      {isLong && (
        <button
          type="button"
          onClick={() => setFull(prev => !prev)}
          className="mt-1 text-[10px] text-muted-foreground hover:text-foreground"
        >
          {full ? 'Show less' : `Show all (${formatted.text.length.toLocaleString()} chars)`}
        </button>
      )}
    </div>
  )
}

// Thinking is a collapsible block like a tool batch, open by default (user
// decision 2026-09-03: it used to minimise once the answer streamed, which
// hid commentary people were still reading). `live` only drives the pulse dot
// while the agent is still reasoning; the toggle is the user's alone.
const ThinkingBatch: React.FC<{ item: Extract<TranscriptItem, { kind: 'thinking' }>; live: boolean }> = ({ item, live }) => {
  const [expanded, toggle] = useDisclosure(`thinking:${item.key}`, true)
  const hasText = Boolean(item.text.trim())

  if (item.assistantUpdate) {
    return (
      <div data-testid="terminal-assistant-update" className="my-2 text-foreground">
        <ConversationMarkdownRenderer content={item.text} framed={false} maxHeight="none" />
      </div>
    )
  }

  return (
    <div data-testid="terminal-clear-thinking-batch" className="my-1">
      <button
        type="button"
        onClick={hasText ? toggle : undefined}
        aria-expanded={hasText ? expanded : undefined}
        data-testid="terminal-clear-thinking-batch-toggle"
        className="flex items-center gap-1 py-1 text-left text-[11px] text-muted-foreground transition-colors hover:text-foreground"
      >
        <span>Thinking</span>
        {live && <span aria-hidden="true" className="inline-block h-1.5 w-1.5 animate-pulse rounded-full bg-muted-foreground/70" />}
        {hasText && (expanded
          ? <ChevronDown className="h-3 w-3 shrink-0" />
          : <ChevronRight className="h-3 w-3 shrink-0" />)}
      </button>
      {hasText && expanded && (
        <p
          data-testid="terminal-clear-thinking-batch-content"
          className="mt-1 whitespace-pre-wrap break-words border-l border-border pl-3 text-xs leading-5 text-muted-foreground"
        >
          {item.text}
        </p>
      )}
    </div>
  )
}

// The Builder chat and the workflow's Pulse talk in rounds on one topic
// (ask_pulse threads, PLAT-697). The whole exchange reads as one block.
const PulseThreadBlock: React.FC<{ item: Extract<TranscriptItem, { kind: 'pulse_thread' }> }> = ({ item }) => {
  const [expanded, toggle] = useDisclosure(`pulse-thread:${item.key}`, false)
  const rounds = item.rounds
  const last = rounds[rounds.length - 1]
  const running = last?.status === 'running'
  const summary = last?.decision
    ? `decision: ${last.decision}${last.ownerNeeded ? ` · owner needed: ${last.ownerNeeded}` : ''}`
    : running ? 'Pulse is answering' : ''
  return (
    <div data-testid="terminal-pulse-thread" className="my-1">
      <button
        type="button"
        onClick={toggle}
        aria-expanded={expanded}
        className="flex max-w-full items-center gap-1 py-1 text-left text-[11px] text-muted-foreground transition-colors hover:text-foreground"
      >
        <span className="font-medium text-foreground">Builder ↔ Pulse: {rounds.length} {rounds.length === 1 ? 'round' : 'rounds'}</span>
        {summary && <span className="truncate">· {summary}</span>}
        {running && <span aria-hidden="true" className="inline-block h-1.5 w-1.5 animate-pulse rounded-full bg-muted-foreground/70" />}
        {expanded ? <ChevronDown className="h-3 w-3 shrink-0" /> : <ChevronRight className="h-3 w-3 shrink-0" />}
      </button>
      {expanded && (
        <ol className="mt-1 space-y-2 border-l border-border pl-3 text-xs leading-5">
          {rounds.map((round, index) => (
            <li key={index}>
              <p className="whitespace-pre-wrap break-words text-muted-foreground"><span className="font-semibold text-foreground">Builder: </span>{round.message}</p>
              <p className="mt-0.5 whitespace-pre-wrap break-words text-foreground"><span className="font-semibold">Pulse: </span>{round.answer || (round.status === 'running' ? '…' : '')}</p>
            </li>
          ))}
        </ol>
      )}
    </div>
  )
}

// A web search is something the reader wants to see (what was looked up, what
// came back), so it gets its own collapsed card instead of hiding inside the
// "N tool calls" chip. The other calls keep their chip, split around the
// searches so the order of what happened is preserved.
type ToolBatchSegment =
  | { kind: 'search'; pair: PairedToolCall }
  | { kind: 'tools'; key: string; pairs: PairedToolCall[] }

function segmentToolBatch(batchKey: string, pairs: PairedToolCall[]): ToolBatchSegment[] {
  const segments: ToolBatchSegment[] = []
  for (const pair of pairs) {
    if (isWebSearchToolCall(pair.name, pair.args)) {
      segments.push({ kind: 'search', pair })
      continue
    }
    const last = segments[segments.length - 1]
    if (last?.kind === 'tools') last.pairs.push(pair)
    // The first chip keeps the batch's own key so its open/closed state
    // survives from before searches were split out.
    else segments.push({ kind: 'tools', key: segments.length === 0 ? batchKey : `${batchKey}:${pair.key}`, pairs: [pair] })
  }
  return segments
}

const WebSearchCard: React.FC<{ pair: PairedToolCall }> = ({ pair }) => {
  const [open, toggleOpen] = useDisclosure(`search:${pair.key}`)
  const failed = useMemo(() => pair.status === 'error' || (pair.result ? formatToolCallResult(pair.result).isError : false), [pair.result, pair.status])
  const duration = pair.durationNs != null && pair.durationNs > 0 ? formatDurationCompact(pair.durationNs) : null
  return (
    <div data-testid="terminal-clear-web-search" className="my-1">
      <WebSearchToolCallDisplay
        name={pair.name}
        args={pair.args}
        result={pair.result}
        status={failed ? 'error' : pair.status}
        duration={duration}
        open={open}
        onToggle={toggleOpen}
      />
    </div>
  )
}

const ToolBatch: React.FC<{ item: Extract<TranscriptItem, { kind: 'tools' }> }> = ({ item }) => {
  const pairs = useMemo(() => pairToolCalls(item.events), [item.events])
  const segments = useMemo(() => segmentToolBatch(item.key, pairs), [item.key, pairs])
  if (segments.length === 1 && segments[0].kind === 'tools') {
    return <ToolChip chipKey={item.key} pairs={pairs} toolCount={item.toolCount} />
  }
  return (
    <>
      {segments.map(segment => segment.kind === 'search'
        ? <WebSearchCard key={segment.pair.key} pair={segment.pair} />
        : <ToolChip key={segment.key} chipKey={segment.key} pairs={segment.pairs} toolCount={segment.pairs.length} />)}
    </>
  )
}

const ToolChip: React.FC<{ chipKey: string; pairs: PairedToolCall[]; toolCount: number }> = ({ chipKey, pairs, toolCount }) => {
  // A conversation should lead with what the agent said, not implementation
  // detail. Even failures stay closed initially: the visible failed count is
  // the signal, and the user chooses when to inspect arguments/results.
  const [expanded, toggle] = useDisclosure(`batch:${chipKey}`)

  return (
    <div data-testid="terminal-clear-tool-batch" className="my-1">
      <button
        type="button"
        onClick={toggle}
        aria-expanded={expanded}
        data-testid="terminal-clear-tool-batch-toggle"
        className="flex items-center gap-1 py-1 text-left text-[11px] text-muted-foreground transition-colors hover:text-foreground"
      >
        <span>{toolCount} tool {toolCount === 1 ? 'call' : 'calls'}</span>
        {expanded
          ? <ChevronDown className="h-3 w-3 shrink-0" />
          : <ChevronRight className="h-3 w-3 shrink-0" />}
      </button>
      {expanded && (
        <div data-testid="terminal-clear-tool-batch-content" className="mt-1 space-y-0.5 border-l border-border pl-3">
          {pairs.map(pair => (
            <ToolCallCard key={pair.key} pair={pair} />
          ))}
        </div>
      )}
    </div>
  )
}

interface TerminalEventTranscriptProps {
  /** Stable chat/terminal identity for restoring reading position. */
  scrollKey?: string
  events: PollingEvent[] | undefined
  terminal: TerminalSnapshot | null | undefined
  // Full terminal list for the session. Required for a correct main-agent
  // transcript (it needs to know which events sibling owned terminals already
  // claim) — see selectTerminalEvents. Optional here only because an owned
  // terminal's own scoping does not need it.
  siblingTerminals?: TerminalSnapshot[]
  onSendMessage?: (msg: string) => void
  onAnswerCodingAgentQuestion?: CodingAgentQuestionAnswerHandler
  closedCodingAgentQuestions?: ReadonlySet<string>
  onRetryLastMessage?: () => void | Promise<void>
  /** Resends a user message whose delivery failed. */
  onResendMessage?: (msg: string) => void
  loading?: boolean
  loadingOlder?: boolean
  hasOlder?: boolean
  error?: string
  onLoadOlder?: () => void
  onRetry?: () => void
  /** Transient response text from SSE. It is intentionally not persisted as
   * individual protocol events, but belongs in the readable live transcript. */
  streamingText?: string
  streamingStatus?: string
  /** Optional product skin for the transcript backdrop. AgentWorks keeps its
   * existing default; product surfaces can use their own visual identity. */
  surfaceClassName?: string
  /** Product interactions to show in place, inside the agent's turn, with the
   * product's own rendering (a celebration, an inline scene). Other
   * interaction kinds stay on the side channel. */
  productRows?: { kinds: string[]; render: (interaction: ProductInteraction, event: PollingEvent) => React.ReactNode }
  /** What the agent is called in turn headers ("Agent" by default; a product passes its own name, e.g. "Quill"). */
  assistantLabel?: string
  /** A small mark identifying the turn (a product's logo); the default agent Bot glyph when unset. */
  assistantIcon?: React.ReactNode
  /** Main chat lifecycle, scoped to this chat; never inferred from a retained terminal. */
  runtimeActivity?: ChatRuntimeActivity
}

const TerminalEventTranscriptInner: React.FC<TerminalEventTranscriptProps & { readingState: TranscriptReadingState; readingKey?: string }> = ({
  events,
  terminal,
  siblingTerminals,
  onSendMessage,
  onAnswerCodingAgentQuestion,
  closedCodingAgentQuestions,
  onRetryLastMessage,
  onResendMessage,
  loading = false,
  loadingOlder = false,
  hasOlder = false,
  error,
  onLoadOlder,
  onRetry,
  streamingText = '',
  streamingStatus = '',
  surfaceClassName,
  productRows,
  assistantLabel = 'Agent',
  assistantIcon,
  runtimeActivity,
  readingState,
  readingKey,
}) => {
  const virtuosoRef = useRef<VirtuosoHandle | null>(null)
  const keptKinds = productRows?.kinds.join('\u0000') ?? ''
  const keepInteractionKinds = useMemo(() => new Set(keptKinds ? keptKinds.split('\u0000') : []), [keptKinds])
  const renderInteraction = productRows?.render
  const scoped = useMemo(
    () => selectTerminalEvents(events, terminal, siblingTerminals, keepInteractionKinds),
    [events, terminal, siblingTerminals, keepInteractionKinds],
  )
  const latestTelemetryEvent = useMemo(
    () => [...scoped].reverse().find(isChatDeliveryTelemetryEvent),
    [scoped],
  )
  useEffect(() => {
    if (!latestTelemetryEvent) return
    const frame = window.requestAnimationFrame(() => {
      const sessionId = latestTelemetryEvent.session_id || terminal?.session_id || ''
      recordChatDeliveryTelemetry('painted', sessionId, [latestTelemetryEvent], 'react', terminal?.terminal_id)
    })
    return () => window.cancelAnimationFrame(frame)
  }, [latestTelemetryEvent, terminal?.session_id, terminal?.terminal_id])
  const questions = useMemo(() => codingAgentQuestionCards(scoped), [scoped])
  const items = useMemo<TranscriptRenderItem[]>(
    () => withToolCallVisibility(removeAdjacentDuplicateAssistantResponses(collapseTurnFailures(buildTranscriptItems(scoped.filter(event => !questions.hiddenEvents.has(event.id)))))),
    [scoped, questions],
  )
  // Retry belongs to the latest human turn, never an older failed message
  // after the user has continued the conversation.
  const retryItem = useMemo(() => [...items].reverse().find(item =>
    item.kind === 'event' && (item.event.type === 'user_message' || isAgentResponseEvent(item.event) || turnFailureText(item.event)),
  ), [items])
  const [isAtTranscriptEnd, setIsAtTranscriptEnd] = useState(true)
  const listData = useMemo<TranscriptRenderItem[]>(
    () => ((streamingText || streamingStatus) && !liveTextAlreadyCommitted(items, streamingText))
      || (runtimeActivity?.state !== undefined && runtimeActivity.state !== 'ready' && (!items.length || isUserItem(items.at(-1))))
      ? [...items, { kind: 'live' as const, key: '__live-stream__', text: streamingText, status: streamingStatus }]
      : items,
    [items, streamingStatus, streamingText, runtimeActivity?.state],
  )
  const turnSlots = useMemo(() => buildTurnSlots(listData), [listData])

  const keys = useMemo(() => listData.map(item => item.key), [listData])
  const [pagination, setPagination] = useState(() => ({ keys, first: 1_000_000 }))
  // Adjust data and the inverse index in the same render so Virtuoso can
  // preserve the visible row when older history is prepended.
  let firstItemIndex = pagination.first
  if (pagination.keys.length !== keys.length || pagination.keys.some((key, index) => key !== keys[index])) {
    firstItemIndex = prependedIndex(pagination.keys, keys, pagination.first)
    setPagination({ keys, first: firstItemIndex })
  }
  const scroll = useTranscriptScroll(keys, readingState, virtuosoRef, readingKey)
  const latestUserMessageKey = useMemo(() => {
    for (let index = items.length - 1; index >= 0; index--) {
      const item = items[index]
      if (item.kind === 'event' && item.event.type === 'user_message'
        && !isInternalTranscriptMessage(item.event) && !isExecutionPromptTranscriptMessage(item.event)) return item.key
    }
    return undefined
  }, [items])
  const previousUserMessageKey = useRef(latestUserMessageKey)
  useEffect(() => {
    const previous = previousUserMessageKey.current
    previousUserMessageKey.current = latestUserMessageKey
    // Sending another message starts a new turn at the bottom. Hydrating or
    // prepending history must not reset a restored reading position.
    if (previous && latestUserMessageKey !== previous && keys.includes(previous)) scroll.jumpToLatest()
  }, [keys, latestUserMessageKey, scroll.jumpToLatest])
  const [initialPosition, setInitialPosition] = useState<{ index: number; align: 'start' | 'end'; offset?: number } | null>(null)
  if (!initialPosition && keys.length > 0) {
    const anchorIndex = readingState.anchor ? keys.indexOf(readingState.anchor.key) : -1
    setInitialPosition(readingState.following || !readingState.deliberate || anchorIndex < 0
      ? { index: keys.length - 1, align: 'end' }
      : { index: anchorIndex, align: 'start', offset: readingState.anchor!.offset })
  }

  const handleEarlierMessages = useCallback(() => {
    if (!hasOlder) return
    scroll.preserveReadingPosition()
    onLoadOlder?.()
  }, [hasOlder, onLoadOlder, scroll])

  const statusMeta = terminal?.status?.status_meta as Record<string, unknown> | undefined
  const usageKey = statusMeta ? JSON.stringify([statusMeta.context_used_tokens, statusMeta.context_window_tokens, statusMeta.rate_limit_windows, statusMeta.status_extras]) : ''
  // Keyed on the values, not the snapshot object: every terminal poll creates
  // a new object and would otherwise re-render the footer for nothing.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const usage = useMemo(() => liveUsageSummary(statusMeta), [usageKey])
  const historyContext = useMemo<TranscriptHistoryContext>(() => ({
    hasOlder: Boolean(hasOlder && onLoadOlder),
    loadingOlder,
    onLoadOlder: handleEarlierMessages,
    activity: runtimeActivity,
    usage,
  }), [hasOlder, loadingOlder, onLoadOlder, handleEarlierMessages, runtimeActivity, usage])

  if (listData.length === 0) {
    const state = (terminal?.state || '').trim().toLowerCase()
    const failed = state === 'failed' || state === 'error' || state === 'stale'
    const completed = state === 'completed' || state === 'closing'
    const Icon = error ? XCircle : failed ? XCircle : completed ? CheckCircle2 : CircleDashed
    const title = loading
      ? 'Loading conversation…'
      : error
        ? 'Conversation could not be loaded.'
        : failed
      ? 'This agent did not finish.'
      : completed
        ? 'This agent completed.'
        : 'Waiting for this agent to begin.'
    const detail = error || (completed || failed
      ? 'Conversation details are not available for this retained run.'
      : 'Its conversation will appear here when the first event arrives.')
    return (
      <div
        data-testid="terminal-clear-view-empty"
        className={`flex min-w-0 flex-1 items-center justify-center overflow-y-auto px-5 py-8 ${surfaceClassName ?? 'bg-[#0b0d0c]'}`}
      >
        <div className="flex max-w-md items-start gap-3 text-left">
          <Icon
            className={`mt-0.5 h-5 w-5 shrink-0 ${
              error || failed
                ? 'text-red-400'
                : completed
                  ? 'text-emerald-400'
                  : loading
                    ? 'animate-spin text-cyan-400'
                    : 'text-cyan-400'
            }`}
          />
          <div>
            <div className="text-sm font-medium text-foreground">{title}</div>
            <div className="mt-1 text-xs leading-5 text-muted-foreground">{detail}</div>
            {error && onRetry && (
              <button
                type="button"
                onClick={onRetry}
                className="mt-3 rounded border border-border px-2.5 py-1 text-xs text-foreground/90 hover:bg-muted"
              >
                Retry
              </button>
            )}
          </div>
        </div>
      </div>
    )
  }

  return (
    <div
      data-testid="terminal-clear-view"
      className={`relative flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden ${surfaceClassName ?? 'bg-[#0d100f]'}`}
      onClickCapture={scroll.preserveDisclosure}
    >
      {error && (
        <div className="flex shrink-0 items-center border-b border-red-900/60 bg-red-950/25 px-3 py-1.5 text-[11px] text-red-300">
          <span className="truncate">Refresh failed: {error}</span>
          {onRetry && (
            <button type="button" onClick={onRetry} className="ml-auto shrink-0 text-red-200 hover:text-white">
              Retry
            </button>
          )}
        </div>
      )}
      {/* Virtualized: the tree inherited this from EventHierarchy. A flat list
          that rendered every event would regress long sessions badly. */}
      <Virtuoso
        // Virtuoso reads initialTopMostItemIndex only when it mounts. A chat
        // opened while its history is still loading mounted at index 0 and then
        // jumped to the bottom once rows arrived (0 -> 40,328 -> 45,948 px,
        // 2026-10-07). Remount once, while the list is still empty, when the
        // starting position is known, so it opens at the bottom (or the saved
        // reading position) without a visible jump.
        key={initialPosition ? 'positioned' : 'loading'}
        ref={virtuosoRef}
        data={listData}
        components={transcriptComponents}
        context={historyContext}
        className="custom-scrollbar min-h-0 flex-1"
        scrollerRef={scroll.scrollerRef}
        atBottomStateChange={setIsAtTranscriptEnd}
        atBottomThreshold={24}
        firstItemIndex={firstItemIndex}
        followOutput={false}
        totalListHeightChanged={scroll.layoutChanged}
        tabIndex={0}
        aria-label="Conversation messages"
        // Render well beyond the viewport so scrolling reveals rows that are
        // already there instead of rows popping in as they mount.
        increaseViewportBy={{ top: 1200, bottom: 600 }}
        initialTopMostItemIndex={initialPosition ?? 0}
        computeItemKey={(_, item) => item.key}
        itemContent={(absoluteIndex, item) => {
          const index = absoluteIndex - firstItemIndex
          // Contain message margins inside the measured row. Collapsed margins
          // otherwise leave unmeasured space at the end of the virtual list.
          const slot = turnSlots[index]
          const testId = item.kind === 'event' ? `terminal-clear-event-${item.event.id || item.key}` : undefined
          const question = item.kind === 'event' ? questions.cards.get(item.event.id) : undefined
          const questionPrompt = question ? withClosedQuestions(question, closedCodingAgentQuestions) : question
          const body = questionPrompt
            ? <CodingAgentQuestionCard prompt={questionPrompt} onAnswer={onAnswerCodingAgentQuestion} />
            : item.kind === 'live'
            ? <LiveAssistantTranscript text={item.text} status={item.status} showWriting={!runtimeActivity} />
            : item.kind === 'tools'
            ? <ToolBatch item={item} />
            : item.kind === 'pulse_thread'
            ? <PulseThreadBlock item={item} />
            : item.kind === 'thinking'
              ? <ThinkingBatch item={item} live={index === items.length - 1 && !streamingText.trim()} />
              : (
                <TranscriptEvent
                  event={item.event}
                  onSendMessage={onSendMessage}
                  onResendMessage={onResendMessage}
                  onRetryLastMessage={item === retryItem ? onRetryLastMessage : undefined}
                  compactUserBottom={listData[index + 1]?.kind === 'tools'}
                  inTurn={Boolean(slot?.agent)}
                  renderInteraction={renderInteraction}
                  assistantLabel={assistantLabel}
                  assistantIcon={assistantIcon}
                  hideTimestamp={!(slot?.showTime ?? true)}
                />
              )
          if (!slot?.agent) {
            return <div data-transcript-key={item.key} data-testid={testId} className="flow-root px-3 py-0.5">{body}</div>
          }
          // One block per agent turn: the header once at the top, then every
          // tool batch, thought and reply of that turn on the same rail.
          return (
            <div data-transcript-key={item.key} data-testid={testId} className="flow-root px-2">
              <div className={`${AGENT_BLOCK_CLASS} ${slot.first ? 'mt-4' : ''} ${slot.last ? 'mb-2' : ''}`}>
                {slot.first && <AssistantTurnHeader event={slot.header} timestamp={slot.showTime && slot.header ? transcriptTimestamp(slot.header) : ''} label={assistantLabel} icon={assistantIcon} />}
                {body}
              </div>
            </div>
          )
        }}
      />
      {!scroll.following && !isAtTranscriptEnd && (
        <div className="absolute inset-x-0 bottom-3 z-10 flex justify-center pointer-events-none">
          <button type="button" onClick={scroll.jumpToLatest} className="pointer-events-auto flex items-center gap-1 rounded-full border border-border bg-background px-3 py-1.5 text-xs text-foreground shadow-md hover:bg-muted">
            <ChevronDown className="h-3.5 w-3.5" /> Jump to latest
          </button>
        </div>
      )}
    </div>
  )
}

// Custom product renderers can keep their existing live-text cue until they
// opt into the shared lifecycle indicator.
const LiveAssistantTranscript: React.FC<{ text: string; status: string; showWriting: boolean }> = ({ text, status, showWriting }) => (
  text ? (
    <article data-testid="terminal-clear-live-assistant-message" className="py-1">
      <div className="[&_li]:!text-[length:calc(14px*var(--chat-scale,1))] [&_p]:!text-[length:calc(14px*var(--chat-scale,1))] [&_li]:!leading-[calc(24px*var(--chat-scale,1))] [&_p]:!leading-[calc(24px*var(--chat-scale,1))]">
        <ConversationMarkdownRenderer content={text} framed={false} maxHeight="none" />
      </div>
      {showWriting && <span aria-label="Writing" className="mt-1 inline-block h-1.5 w-1.5 animate-pulse rounded-full bg-cyan-400" />}
    </article>
  ) : status ? (
    // Virtuoso receives this row whenever the backend has tool/status progress
    // but no assistant prose yet. Returning null made it a zero-height item,
    // which breaks its measurements and leaves new user messages off-screen.
    // Keep it visually quiet while giving the virtual list a real anchor for
    // the one-time tool/status scroll.
    <div aria-hidden="true" className="h-px" data-testid="terminal-live-status-anchor" />
  ) : null
)

// Memoized: the parent re-renders on every terminal poll, and re-rendering the
// whole transcript each time would defeat EventDispatcher's own memoization.
export const TerminalEventTranscript = memo(function TerminalEventTranscript(props: TerminalEventTranscriptProps) {
  const key = props.scrollKey ?? props.terminal?.terminal_id ?? props.events?.find(event => event.session_id)?.session_id
  const readingState = useMemo(() => key ? transcriptReadingState(key) : { following: true, disclosures: new Map<string, boolean>() }, [key])
  return <DisclosureContext.Provider value={readingState.disclosures}>
    <TerminalEventTranscriptInner key={key ?? 'unscoped'} {...props} readingState={readingState} readingKey={key} />
  </DisclosureContext.Provider>
})
