import { normalizeTranscriptChunkEvents } from './transcriptChunkUpdates'
import type { PollingEvent } from '../services/api-types'
import { askAIDisplayText } from './askAIMessage'
import { appendStreamingText } from './streamingStatus'
import { humanReadableAgentResult } from '../components/events/system/eventDisplayUtils'
import {
  looksLikeProductChatFailure,
  normalizeProductChatFailure,
  type ProductChatFailure,
} from '../platform/chat/productChatFailure'
import { pairToolCalls, isAssistantUpdate } from './terminalEventTranscript'
import { isConversationContinuityNotice } from '../components/ConversationContinuityNotice'

export type ConversationItem = {
  id: string
  role: 'user' | 'assistant' | 'progress' | 'reasoning' | 'error' | 'notification' | 'continuity' | 'background' | 'question'
  content: string
  timestamp?: string
  assistantUpdate?: boolean
  usage?: ConversationUsage
  failure?: ProductChatFailure
  codingAgentQuestion?: CodingAgentQuestionPrompt
}

export type CodingAgentQuestionPrompt = {
  provider: string
  promptId: string
  state: 'pending' | 'answered' | 'interrupted'
  questions: Array<{ id: string; header: string; question: string; multiSelect: boolean; minSelections?: number; maxSelections?: number; options: Array<{ label: string; description: string }> }>
  answers: Array<{ id: string; selectedLabels: string[] }>
}

export type ConversationUsage = {
  inputTokens: number
  outputTokens: number
  cacheTokens: number
  isEstimated: boolean
}

export type ProductionActivityItem = {
  id: string
  title: string
  detail: string
  status: 'running' | 'complete' | 'error'
  kind: 'tool' | 'workflow'
  arguments?: string
  result?: string
}

const RESTORED_CONVERSATION_CONTEXT_MARKER = '\n\nPrevious workflow-builder conversation file:'

function asRecord(value: unknown): Record<string, unknown> | undefined {
  return value && typeof value === 'object' ? value as Record<string, unknown> : undefined
}

function eventPayload(event: PollingEvent): Record<string, unknown> {
  const envelope = asRecord(event.data)
  return asRecord(envelope?.data) || envelope || {}
}

function firstText(...values: unknown[]): string {
  for (const value of values) {
    if (typeof value === 'string' && value.trim()) return value.trim()
  }
  return ''
}

function finiteNumber(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

function failureHints(payload: Record<string, unknown>) {
  const metadata = asRecord(payload.metadata)
  const error = asRecord(payload.error)
  return {
    code: firstText(payload.code, payload.error_code, error?.code, error?.kind, metadata?.code, metadata?.error_code),
    provider: firstText(payload.provider, payload.llm_provider, error?.provider, metadata?.provider),
    retryAt: payload.retry_at ?? payload.resume_after ?? error?.retry_at ?? error?.retryAt ?? metadata?.retry_at ?? metadata?.resume_after,
    technicalDetails: firstText(payload.technical_details, payload.technicalDetails, error?.technical_details, error?.technicalDetails, metadata?.technical_details, metadata?.technicalDetails),
  }
}

function productFailureItem(event: PollingEvent, payload: Record<string, unknown>, raw: string): ConversationItem {
  const failure = normalizeProductChatFailure(raw, failureHints(payload))
  return {
    id: event.id,
    role: 'error',
    content: failure.message,
    timestamp: event.timestamp,
    failure,
  }
}

// A completed native structured turn emits its conversation-total usage just
// after the final assistant result. Keep that usage with the answer that
// caused it, rather than displaying a project-wide total in the chat.
function conversationUsage(payload: Record<string, unknown>): ConversationUsage | undefined {
  if (firstText(payload.context, payload.operation) !== 'conversation_total') return undefined
  const generationInfo = asRecord(payload.generation_info)
  const additional = asRecord(generationInfo?.Additional) ?? asRecord(generationInfo?.additional)
  const inputTokens = finiteNumber(generationInfo?.cumulative_prompt_tokens) ?? finiteNumber(payload.prompt_tokens) ?? 0
  const outputTokens = finiteNumber(generationInfo?.cumulative_completion_tokens) ?? finiteNumber(payload.completion_tokens) ?? 0
  const cacheTokens = finiteNumber(generationInfo?.cumulative_cache_tokens)
    ?? finiteNumber(generationInfo?.cache_read_input_tokens)
    ?? finiteNumber(payload.cache_tokens)
    ?? 0
  if (inputTokens === 0 && outputTokens === 0 && cacheTokens === 0) return undefined
  return {
    inputTokens,
    outputTokens,
    cacheTokens,
    isEstimated: payload.token_usage_estimated === true
      || payload.usage_estimated === true
      || generationInfo?.token_usage_estimated === true
      || additional?.token_usage_estimated === true,
  }
}

function displaySafeUserMessage(content: string): string {
  const markerIndex = content.indexOf(RESTORED_CONVERSATION_CONTEXT_MARKER)
  const stripped = (markerIndex >= 0 ? content.slice(0, markerIndex) : content).trim()
  // Ask AI blocks collapse to their plain-words request; the builder-only
  // instructions (tool calls, skill paths) never render in chat.
  return askAIDisplayText(stripped)
}

function isChildExecution(event: PollingEvent, payload: Record<string, unknown>): boolean {
  const kind = firstText(event.execution_kind, payload.execution_kind).toLowerCase()
  return Boolean(kind && kind !== 'main_agent' && kind !== 'main-agent')
}

/**
 * Converts the orchestration event stream into the small, durable transcript a
 * product user expects: their messages, final answers, and actionable errors.
 * Tool calls and terminal details deliberately remain in the runtime. Providers
 * can additionally supply a structured reasoning event, which remains visually
 * separate from the final assistant answer.
 */
export function buildCleanConversationItems(events: PollingEvent[]): ConversationItem[] {
	const items: ConversationItem[] = []
	let lastAssistantContent = ''
	let completedAssistantAwaitingUsage: ConversationItem | undefined
	const pendingFrontendUserEchoes = new Map<string, number>()
	const pendingCodingAgentQuestions = new Map<string, ConversationItem>()
	const pushUnique = (item: ConversationItem) => {
		const previous = items.at(-1)
		if (previous?.role === item.role && previous.content === item.content) return
		items.push(item)
	}

  for (const event of normalizeTranscriptChunkEvents(events)) {
    const payload = eventPayload(event)

    if (event.type === 'user_message') {
      const content = displaySafeUserMessage(firstText(payload.content, asRecord(event.data)?.content))
      if (!content) continue
      const metadata = asRecord(payload.metadata)
      const displayContent = typeof metadata?.display_content === 'string' ? metadata.display_content.trim() : ''
      if (isConversationContinuityNotice(content)) {
        pushUnique({ id: event.id, role: 'continuity', content, timestamp: event.timestamp })
        if (displayContent) pushUnique({ id: `${event.id}:typed`, role: 'user', content: displayContent, timestamp: event.timestamp })
        continue
      }
      if (content.startsWith('[AUTO-NOTIFICATION]')) {
        // Surfaced directly for now — background execute_step / run_full_workflow
        // completions were previously only inferable from the assistant's next
        // reply, which read as the agent narrating unprompted.
        const notification = content.replace(/^\[AUTO-NOTIFICATION\]\s*/, '').trim()
        if (notification) pushUnique({ id: event.id, role: 'notification', content: notification, timestamp: event.timestamp })
        continue
      }
			if (event.id?.startsWith('user-message-')) {
				pendingFrontendUserEchoes.set(content, (pendingFrontendUserEchoes.get(content) || 0) + 1)
			} else {
				const pendingEchoCount = pendingFrontendUserEchoes.get(content) || 0
				if (pendingEchoCount > 0) {
					if (pendingEchoCount === 1) pendingFrontendUserEchoes.delete(content)
					else pendingFrontendUserEchoes.set(content, pendingEchoCount - 1)
					continue
				}
			}
			pushUnique({ id: event.id, role: 'user', content: displayContent || content, timestamp: event.timestamp })
      // The same answer can legitimately recur after another user message.
      // Only collapse duplicate final-answer carriers within the current turn.
      lastAssistantContent = ''
      completedAssistantAwaitingUsage = undefined
      continue
    }

    if (isChildExecution(event, payload)) continue

    if (event.type === 'coding_agent_question') {
      const provider = firstText(payload.provider)
      const promptId = firstText(payload.prompt_id)
      if (!promptId) continue
      const questionKey = `${provider}:${promptId}`
      if ((payload.kind === 'requested' || payload.kind === 'user_input_prompt_requested') && Array.isArray(payload.questions)) {
        const questions = payload.questions.map((value) => {
          const q = asRecord(value) || {}
          return {
            id: firstText(q.id), header: firstText(q.header), question: firstText(q.question), multiSelect: q.multi_select === true || q.multiSelect === true,
            minSelections: typeof q.min_selections === 'number' ? q.min_selections : undefined,
            maxSelections: typeof q.max_selections === 'number' ? q.max_selections : undefined,
            options: Array.isArray(q.options) ? q.options.map((option) => {
              const o = asRecord(option) || {}
              return { label: firstText(o.label), description: firstText(o.description) }
            }).filter((option) => option.label) : [],
          }
        }).filter((question) => question.id && question.question && question.options.length)
        if (!questions.length) continue
        const item: ConversationItem = {
          id: event.id, role: 'question', content: questions.map((q) => q.question).join(' · '), timestamp: event.timestamp,
          codingAgentQuestion: { provider, promptId, state: 'pending', questions, answers: [] },
        }
        items.push(item)
        pendingCodingAgentQuestions.set(questionKey, item)
      } else if (payload.kind === 'settled' || payload.kind === 'user_input_prompt_settled') {
        const item = pendingCodingAgentQuestions.get(questionKey)
        if (item?.codingAgentQuestion) {
          const answers = Array.isArray(payload.answers) ? payload.answers.map((value) => {
            const answer = asRecord(value) || {}
            const selectedLabels = Array.isArray(answer.selected_labels)
              ? answer.selected_labels.filter((label): label is string => typeof label === 'string')
              : [firstText(answer.selected_label)].filter(Boolean)
            return { id: firstText(answer.id), selectedLabels }
          }) : []
          item.codingAgentQuestion = { ...item.codingAgentQuestion, state: payload.outcome === 'answered' ? 'answered' : 'interrupted', answers }
          pendingCodingAgentQuestions.delete(questionKey)
        }
      }
      continue
    }

    if (event.type === 'coding_agent_background_task') {
      const kind = firstText(payload.kind)
      if (['task_backgrounded', 'status', 'output', 'completed', 'failed', 'cancelled', 'rejected'].includes(kind)) {
        const label = kind === 'task_backgrounded' ? 'started' : kind
        const message = firstText(payload.message)
        const task = firstText(payload.task_id).slice(0, 8)
        pushUnique({
          id: event.id,
          role: 'background',
          content: `Background task${task ? ` ${task}` : ''} ${label}${message ? `: ${message}` : ''}`,
          timestamp: event.timestamp,
        })
      }
      continue
    }

    if (event.type === 'conversation_thinking') {
      const content = firstText(payload.thinking)
      if (!content) continue
      // A token-streamed fragment (is_delta) continues the reasoning item
      // before it rather than opening another one.
      const previous = items.at(-1)
      const assistantUpdate = isAssistantUpdate(event)
      const role = assistantUpdate ? 'progress' : 'reasoning'
      if (payload.is_delta === true && previous?.role === role && !!previous.assistantUpdate === assistantUpdate) {
        items[items.length - 1] = { ...previous, content: appendStreamingText(previous.content, content, true) }
        continue
      }
      pushUnique({ id: event.id, role, content, timestamp: event.timestamp, ...(assistantUpdate ? { assistantUpdate: true } : {}) })
      continue
    }

    // Durable history is projected into llm_generation_end events by the
    // shared session restorer. Treat that same final-answer carrier exactly
    // like the live completion events so product surfaces retain assistant
    // replies after refresh, not just the user's prompts.
    if (event.type === 'llm_generation_end' || event.type === 'unified_completion' || event.type === 'conversation_end') {
      const content = humanReadableAgentResult(firstText(payload.content, payload.final_result, payload.result))
			const isIntermediateUpdate = payload.restored_intermediate_update === true
			const rawError = firstText(payload.error, asRecord(payload.error)?.message)
			const failureText = rawError || (looksLikeProductChatFailure(content) ? content : '')
			if (failureText) {
				pushUnique(productFailureItem(event, payload, failureText))
				completedAssistantAwaitingUsage = undefined
			} else if (content && (isIntermediateUpdate || content !== lastAssistantContent)) {
				if (!isIntermediateUpdate) {
          // Coding CLIs commonly emit the final prose once as a transcript
          // chunk and again as unified_completion. The chunk is progress only
          // until the completion proves it is final; replace that progress row
          // instead of showing the same answer twice in two visual styles.
          for (let index = items.length - 1; index >= 0; index -= 1) {
            if (items[index].role === 'user') break
            if (items[index].role === 'progress' && items[index].content === content) {
              items.splice(index, 1)
              break
            }
          }
        }
				const assistantItem: ConversationItem = {
          id: event.id,
          role: isIntermediateUpdate ? 'progress' : 'assistant',
          content,
          timestamp: event.timestamp,
          ...(isIntermediateUpdate ? { assistantUpdate: true } : {}),
        }
				pushUnique(assistantItem)
        if (!isIntermediateUpdate) {
          completedAssistantAwaitingUsage = assistantItem
          lastAssistantContent = content
        }
      }
      continue
    }

    if (event.type === 'token_usage' && completedAssistantAwaitingUsage) {
      const usage = conversationUsage(payload)
      if (usage) completedAssistantAwaitingUsage.usage = usage
      continue
    }

    if (event.type === 'conversation_error' || event.type === 'agent_error') {
      const content = firstText(payload.error, asRecord(payload.error)?.message, payload.context, 'The request could not be completed.')
			pushUnique(productFailureItem(event, payload, content))
      continue
    }

    if (event.type === 'context_cancelled') {
			pushUnique(productFailureItem(event, { ...payload, code: 'cancelled' }, 'The current response was cancelled.'))
    }
  }

  return items
}

function toolName(event: PollingEvent): string {
  const payload = eventPayload(event)
  return firstText(payload.tool_name, payload.name).toLowerCase()
}

function toolActivityCopy(name: string): { title: string; detail: string } {
  const normalized = name.toLowerCase()
  // `exec` is the bridge wrapper for the same workspace capability. Showing
  // the canonical tool preserves raw visibility without rendering duplicates.
  const displayName = normalized === 'exec' ? 'execute_shell_command' : name
  // Keep tool names literal during the implementation phase. This gives the
  // operator the actual capability the agent invoked without dumping raw
  // arguments or results into the creator-facing conversation.
  if (normalized === 'run_full_workflow') return { title: displayName, detail: 'Workflow: running the full production workflow' }
  if (normalized === 'execute_step') return { title: displayName, detail: 'Workflow: running the selected production step' }
  return { title: displayName || 'tool', detail: 'Tool call' }
}

const TOOL_DETAIL_LIMIT = 12_000

// Developer details are intentionally available in the product while it is
// being built, but credentials must never be echoed back from a tool event.
function developerToolDetail(value?: string): string | undefined {
  const trimmed = value?.trim()
  if (!trimmed) return undefined
  const redacted = trimmed
    .replace(/("(?:api[_-]?key|token|authorization|password|secret)"\s*:\s*")[^"]*(")/gi, '$1[redacted]$2')
    .replace(/\b(Bearer\s+)[^\s"']+/gi, '$1[redacted]')
    .replace(/\b(MCP_API_TOKEN|API[_-]?KEY|AUTHORIZATION|PASSWORD|SECRET)\s*[=:]\s*[^\s"']+/gi, '$1=[redacted]')
  return redacted.length > TOOL_DETAIL_LIMIT
    ? `${redacted.slice(0, TOOL_DETAIL_LIMIT)}\n… output truncated`
    : redacted
}

function lastHumanTurnEvents(events: PollingEvent[]): PollingEvent[] {
  let start = 0
  for (let index = events.length - 1; index >= 0; index -= 1) {
    const event = events[index]
    if (event.type !== 'user_message') continue
    const content = firstText(eventPayload(event).content, asRecord(event.data)?.content)
    if (!content.startsWith('[AUTO-NOTIFICATION]')) {
      start = index
      break
    }
  }
  return events.slice(start)
}

/** Index of every event that opens a human turn. Auto-notifications arrive as
 * user_message events but nobody typed them, so they continue the turn they
 * were triggered by rather than starting a new one. */
function humanTurnStarts(events: PollingEvent[]): number[] {
  const starts: number[] = []
  events.forEach((event, index) => {
    if (event.type !== 'user_message') return
    const content = firstText(eventPayload(event).content, asRecord(event.data)?.content)
    if (content.startsWith('[AUTO-NOTIFICATION]')) return
    starts.push(index)
  })
  return starts
}

export type ProductionActivityTurn = {
  /** Event id of the user message that opened this turn, so the surface can
   * render each turn's activity against its own message. */
  anchorId: string
  items: ProductionActivityItem[]
}

/** Activity grouped per turn, so sending a new message does not take the
 * previous turn's work off screen -- it happened, and the record of it is
 * often the only place a user can see what was done on their behalf. */
export function buildProductionActivityTurns(events: PollingEvent[]): ProductionActivityTurn[] {
  const starts = humanTurnStarts(events)
  if (starts.length === 0) {
    const items = productionActivityForTurn(events)
    return items.length > 0 ? [{ anchorId: '', items }] : []
  }
  const turns: ProductionActivityTurn[] = []
  // Work can precede the first human message on a restored conversation.
  const opening = productionActivityForTurn(events.slice(0, starts[0]))
  if (opening.length > 0) turns.push({ anchorId: '', items: opening })
  starts.forEach((start, position) => {
    const end = position + 1 < starts.length ? starts[position + 1] : events.length
    const items = productionActivityForTurn(events.slice(start, end))
    if (items.length > 0) turns.push({ anchorId: events[start].id, items })
  })
  return turns
}

/** Product-facing, observable work for the current turn. Raw terminal output,
 * private reasoning, tool arguments, and tool results remain internal. */
export function buildProductionActivityItems(events: PollingEvent[]): ProductionActivityItem[] {
  return productionActivityForTurn(lastHumanTurnEvents(events))
}

function productionActivityForTurn(turnEvents: PollingEvent[]): ProductionActivityItem[] {
  const toolItems: ProductionActivityItem[] = pairToolCalls(turnEvents).map((tool) => {
    const copy = toolActivityCopy(tool.name)
    return {
      id: tool.key,
      ...copy,
      kind: 'tool' as const,
      status: tool.status === 'ok' ? 'complete' as const : tool.status,
      arguments: developerToolDetail(tool.args),
      result: developerToolDetail(tool.result),
    }
  })
  const items: ProductionActivityItem[] = []
  for (const item of toolItems) {
    const existingIndex = items.findIndex((candidate) => candidate.title === item.title && candidate.detail === item.detail)
    if (existingIndex < 0) {
      items.push(item)
      continue
    }
    const existing = items[existingIndex]
    const existingHasDetail = Boolean(existing.arguments || existing.result)
    const itemHasDetail = Boolean(item.arguments || item.result)
    if (!existingHasDetail && !itemHasDetail) {
      // Cursor emits redundant empty wrapper/provider lifecycle fragments. A
      // single latest status is enough when neither row contains inspectable
      // data.
      items[existingIndex] = item
    } else if (existing.arguments === item.arguments && existing.result === item.result) {
      items[existingIndex] = item
    } else {
      // Never let an output-only fragment replace a row with its input (or a
      // second real invocation replace the first). The developer details must
      // stay attached to the exact correlated call.
      items.push(item)
    }
  }

  for (const event of turnEvents) {
    const payload = eventPayload(event)
    if (event.type === 'routing_evaluated' || event.type === 'todo_task_route_selected') {
      const response = asRecord(payload.routing_response)
      const routeID = firstText(response?.selected_route_id, payload.route_id, payload.selected_route_id)
      const routes = Array.isArray(payload.routes) ? payload.routes : []
      const route = routes.map(asRecord).find((candidate) => firstText(candidate?.route_id) === routeID)
      items.push({
        id: event.id,
        title: 'Choose production path',
        detail: firstText(route?.route_name, routeID, 'Production route selected'),
        status: 'complete',
        kind: 'workflow',
      })
    }
    if (event.type === 'workflow_step_started') {
      items.push({
        id: event.id,
        title: firstText(payload.step_title, payload.title, 'Production step'),
        detail: 'Workflow step started',
        status: 'running',
        kind: 'workflow',
      })
    }
    if (event.type === 'workflow_step_completed') {
      const stepID = firstText(payload.step_id, payload.id)
      const running = [...items].reverse().find((item) => item.kind === 'workflow' && item.status === 'running' && (!stepID || item.id.includes(stepID)))
      if (running) running.status = 'complete'
      else items.push({
        id: event.id,
        title: firstText(payload.step_title, payload.title, 'Production step'),
        detail: 'Workflow step completed',
        status: 'complete',
        kind: 'workflow',
      })
    }
  }

  return items.slice(-8)
}

export function cleanConversationActivity(events: PollingEvent[], fallback: string): string {
  for (let index = events.length - 1; index >= 0; index -= 1) {
    const event = events[index]
    const name = toolName(event)
    if (name === 'show_video') return 'Preparing the finished video'
    if (name === 'run_full_workflow' || name === 'execute_step') return 'Running the production workflow'
    if (name === 'read_skill') return 'Preparing the production checklist'
    if (name === 'read_image') return 'Reviewing the video frames'
    if (name === 'execute_shell_command') return 'Checking the video output'
    if (name === 'generate_video' || name === 'create_video' || name === 'render_video') return 'Rendering the video'
    if (name === 'generate_image' || name === 'image_generation') return 'Creating the visual assets'

    switch (event.type) {
      case 'routing_evaluated':
      case 'todo_task_route_selected':
        return 'Choosing the best production path'
      case 'background_agent_started':
        return 'Working through the production steps'
      case 'pre_validation_start':
      case 'pre_validation_completed':
        return 'Checking the work'
      case 'orchestrator_agent_start':
      case 'agent_start':
        return fallback
      default:
        break
    }
  }
  return fallback
}
