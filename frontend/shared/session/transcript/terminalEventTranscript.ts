import { normalizeTranscriptChunkEvents } from '../transcriptChunkUpdates'
import { getOwnedTerminalOwnerKeys, getTerminalOwnerPayload } from './eventOwnership'
import { parseProductInteraction } from '../interactions'
import { isMainAgentTerminal } from './terminalIdentity'
import type { PollingEvent, TerminalSnapshot } from '../types'
import { compareTerminalEvents } from './terminalEventPage'

// FORMATTED VIEW VISIBILITY CONTRACT
//
// This file is the single source of truth for what the normal Chat/Schedule
// Formatted view shows. Keep changes to event visibility documented here and
// covered by terminalEventTranscript.test.ts.
//
// Always visible:
//   - human messages and the main agent's answers;
//   - main-agent errors and failed tool calls;
//   - automatic notifications, rendered as compact collapsed updates;
//   - main-agent tool calls, grouped into an expandable “N tool calls” row;
//   - long task/user messages, collapsed behind an explicit disclosure.
//
// Hidden from the main conversation:
//   - raw streaming packets and provider/system lifecycle telemetry;
//   - conversation token totals unless the caller explicitly enables usage;
//   - large-output bookkeeping markers (the owning tool call remains visible);
//   - workflow-step/background-child payloads, including their internal tools,
//     validation data, and errors. Their product-facing boundary is the compact
//     main-session auto-notification plus the main agent's resulting summary.
//     Runtime diagnostics may expose the raw child transcript when explicitly
//     enabled, but normal product correctness must not depend on that surface.
//
// Deduplicated rather than semantically hidden:
//   - repeated final-answer carriers;
//   - duplicate lifecycle start/completion events;
//   - wrapper/container lifecycle records;
//   - an execution prompt already present on its richer start event;
//   - successful empty completions that only settle transport state.
//
// If a child failure cannot be understood from its main-session notification,
// fix that notification boundary or add a compact product failure projection;
// do not silently depend on the disabled child-terminal rail.
//
// Pure selection/grouping logic for the terminal clean view.
//
// Deliberately free of React and component imports so it can be tested on its
// own: these two functions decide WHICH conversation a terminal shows and how
// tool runs collapse, and both fail silently if wrong (you get a plausible but
// incorrect transcript rather than an error).

// The transcript is meant to read as the conversation actually happened:
//
//   user → tools → assistant → pre-validation → auto-notification
//
// Cost, context usage, and provider status snapshots live in the terminal
// footer. They are not part of the conversation, so keep them out of Clear
// View. A long reviewer can emit dozens of status_line updates; rendering each
// one as a card pushes the actual finding off screen.
// A product_interaction the surface asked the transcript to show in place
// (a celebration, an inline scene) instead of leaving to the side channel.
function isKeptInteraction(event: PollingEvent, keep?: ReadonlySet<string>): boolean {
  if (!keep || keep.size === 0) return false
  const it = parseProductInteraction(event)
  return Boolean(it && keep.has(it.kind))
}

const NON_TRANSCRIPT_TYPES = new Set([
  'token_usage',
  'status_line',
  // Provider/setup diagnostics belong in Terminal, not in the readable
  // conversation. Their useful human content is already represented by the
  // user_message and final-answer events beside them.
  'system_prompt',
  'conversation_start',
  'conversation_end',
  'conversation_turn',
  'llm_generation_start',
  'llm_generation_with_retry',
  // Live delivery is assembled by ChatArea into the transient streaming text
  // buffer. Persisted chunks are protocol packets, not messages, and must not
  // fall through EventDispatcher as noisy "Unknown Event Type" JSON cards.
  'streaming_start',
  'streaming_chunk',
  // Product side-channel events (suggestion pills, family/pin updates) are
  // read by the product's own surface, not rendered as transcript cards.
  'product_interaction',
  // Compatibility for pre-profile Work state events. New product tools emit
  // typed product_interaction events declared in product.yaml.
  'work_identity_updated',
  'work_workflow_references_updated',
  'streaming_end',
  // Cache diagnostics are still emitted on the wire (one cache_event per
  // cached MCP connection per tool call) for observability tracers, but they
  // are not part of the readable conversation and must not fall through
  // EventDispatcher as noisy "Unknown Event Type" JSON cards.
  'cache_event',
  // MCP connection lifecycle likewise exists only as Langfuse/LangSmith
  // tracer spans; the bridge already skips it, this keeps stored history
  // from rendering it as an unknown card.
  'mcp_server_connection_start',
  'mcp_server_connection_end',
  // Durable answer/expiry marker for a blocking human-input request; read by
  // the pending-prompt logic, never rendered as its own card.
  'human_feedback_resolved',
])

// The developer diagnostics rail shows the conversation, not the trace:
// user/assistant/tool rows plus content-bearing gates, errors, and results.
// Pure lifecycle/status banners are hidden here (they never showed in product
// chats either). Rule: hide status banners, keep anything carrying message,
// tool, result, or error content. Deliberately a denylist (fail-open) so a
// future content-bearing type stays visible until someone classifies it.
const TERMINAL_RAIL_HIDDEN_TYPES = new Set([
  // Sub-agent lifecycle (hidden from the rail only; product-chat branches
  // stay intact and the events stay live for stores, retention, bots, and
  // backend consumers).
  'delegation_start',
  'delegation_end',
  'orchestrator_agent_start',
  'orchestrator_agent_end',
  // orchestrator_agent_error stays visible: it is the failure that explains
  // why a routing, todo, message-sequence, human-input, or Crew child stopped.
  'background_agent_started',
  'background_agent_completed',
  'background_agent_terminated',
  // Workflow/orchestrator status (completion is still visible via
  // unified_completion / agent_end; orchestrator_end keeps its product-chat
  // renderer and is hidden from the rail only).
  'orchestrator_end',
  'todo_task_route_selected',
  'todo_task_step_completed',
  // Context/usage diagnostics.
  'step_token_usage',
  'routing_evaluated',
  'variables_extracted',
  // Transient resilience status.
  'retry_attempt',
  'broken_pipe',
  'max_turns_reached',
  // Scripted-mode execution output (store still consumes it for purge/dedup;
  // just not painted in the rail).
  'learn_code_script_execution',
  // Selection + delivery mechanics.
  'mcp_server_selection',
  'synthetic_turn_ready',
  'auto_notification_steered',
  'conversation_resumed',
])

// A full run is a CONTAINER, not an agent: it has no conversation of its own,
// only the steps beneath it. The backend already declares this
// (ExecutionKindFullRun) and keeps it out of the terminal rail for exactly that
// reason, but its lifecycle card still rendered at the top of the transcript --
// a "Full Run [Group / Iteration 0]" row restating the panel header above it
// before the first thing that actually happened.
const CONTAINER_EXECUTION_KINDS = new Set(['full_run'])
const MAIN_AGENT_EXECUTION_KINDS = new Set(['main_agent', 'main', 'chat'])
const TOOL_CALL_TYPES = new Set([
  'tool_call_start',
  'tool_call_end',
  'tool_call_error',
])

// Absorbed into an adjacent tool batch so they don't split one in half, but
// never worth a row of their own. delegation_* interleave between a
// tool_call_start/end pair in workflow and multi-agent runs.
//
// llm_generation_end is only a bridge when it is EMPTY. It is also the event
// that carries the assistant's final answer, and nothing else in the terminal
// transcript renders that text: EventDispatcher has no streaming_chunk case,
// so chunks draw nothing here. Treating it as an unconditional bridge meant a
// tool-heavy turn -- a CDP browser step ends on a tool call, then answers --
// swept the answer into the tool batch, where pairToolCalls emits only
// tool rows and drops it. The turn rendered with no response at all.
const BATCH_BRIDGE_TYPES = new Set(['delegation_start', 'delegation_end'])

// An llm_generation_end with text must never be absorbed; an empty one still
// behaves as a bridge so it doesn't split a tool batch in half.
function isEmptyGenerationEnd(event: PollingEvent): boolean {
  return textField(eventFields(event).content).trim().length === 0
}

// A lifecycle start is useful while an agent is running. Once the matching
// completion/failure arrives, rendering both is just duplicate information:
// the retained "Running" card makes a finished task look active next to its
// actual result. Keep the terminal transcript to one lifecycle card per agent.
const LIFECYCLE_EVENT_FAMILIES: Record<string, { start?: boolean; terminal?: boolean; family: string }> = {
  agent_start: { start: true, family: 'agent' },
  agent_end: { terminal: true, family: 'agent' },
  agent_error: { terminal: true, family: 'agent' },
  orchestrator_agent_start: { start: true, family: 'orchestrator-agent' },
  orchestrator_agent_end: { terminal: true, family: 'orchestrator-agent' },
  orchestrator_agent_error: { terminal: true, family: 'orchestrator-agent' },
  background_agent_started: { start: true, family: 'background-agent' },
  background_agent_completed: { terminal: true, family: 'background-agent' },
  background_agent_terminated: { terminal: true, family: 'background-agent' },
}

function isFullRunContainerEvent(event: PollingEvent): boolean {
  const fields = eventFields(event)
  const kind = textField(event.execution_kind) || textField(fields.execution_kind)
  if (CONTAINER_EXECUTION_KINDS.has(kind)) return true

  // Compatibility for events recorded before completion events preserved the
  // creator-declared full_run kind. Their stable workflow-full-* execution id
  // still identifies the orchestration container unambiguously.
  const identity = textField(event.execution_id) ||
    textField(fields.execution_id) ||
    textField(fields.agent_id)
  return identity.startsWith('workflow-full-')
}

function isContainerTranscriptNoise(event: PollingEvent): boolean {
  if (!isFullRunContainerEvent(event)) return false
  return LIFECYCLE_EVENT_FAMILIES[event.type || ''] !== undefined ||
    event.type === 'auto_notification_steered'
}

// Message-sequence items are announced twice: the generic background runner
// emits a blue/green lifecycle banner, then the delegated step emits the real
// task/result card. The generic rows add no content ("Running" / "completed")
// and remain visually loud after the useful card has appeared. Keep failures
// and cancellations visible, but remove the redundant happy-path wrapper.
const MESSAGE_SEQUENCE_WRAPPER_TYPES = new Set([
  'background_agent_started',
  'background_agent_completed',
])

function isMessageSequenceWrapperEvent(event: PollingEvent): boolean {
  if (!MESSAGE_SEQUENCE_WRAPPER_TYPES.has(event.type || '')) return false
  const fields = eventFields(event)
  const metadata = fields.metadata && typeof fields.metadata === 'object'
    ? fields.metadata as Record<string, unknown>
    : undefined
  const name = textField(fields.name).toLowerCase()
  const agentID = textField(fields.agent_id) || textField(metadata?.agent_id)
  return name.startsWith('message sequence item') ||
    agentID.toLowerCase().startsWith('msgseq-') ||
    metadata?.message_sequence_item === true ||
    metadata?.message_sequence_item === 'true'
}

function eventFields(event: PollingEvent): Record<string, unknown> {
  const outer = event.data
  if (!outer || typeof outer !== 'object') return {}
  const nested = (outer as { data?: unknown }).data
  return nested && typeof nested === 'object'
    ? nested as Record<string, unknown>
    : outer as Record<string, unknown>
}

function textField(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

/** Long, multi-line inputs are task briefs, even when authored by the user.
 * They need a compact readable card rather than a narrow right-aligned chat
 * line that makes every wrapped line appear to run backwards.
 */
export function shouldCollapseTranscriptUserMessage(content: string): boolean {
  const normalized = content.trim()
  if (!normalized) return false
  return normalized.length > 480 || normalized.split(/\r?\n/).length > 6
}

/**
 * A few orchestrator messages travel over the same transport as a person's
 * chat input.  They are useful audit trail, but they are not authored by the
 * person looking at the conversation.  Rendering them as a "You" bubble made
 * the formatted view lie about who said what and then repeat the completion
 * in the following assistant response.
 */
export function isInternalTranscriptMessage(event: PollingEvent): boolean {
  if (event.type !== 'user_message') return false
  const content = textField(eventFields(event).content)
  return /^\[AUTO-NOTIFICATION\]/.test(content) ||
    /^PULSE RUN CONTEXT\./.test(content) ||
    /^PULSE GATE\s*\/\s*WORKLIST\./.test(content)
}

/**
 * Workflow execution instructions share the user_message event type because
 * they are input to the coding agent, but they are not messages authored by
 * the person in chat. The transcript must render them as a left-aligned Task,
 * not as a right-aligned human message.
 */
export function isExecutionPromptTranscriptMessage(event: PollingEvent): boolean {
  if (event.type !== 'user_message') return false
  const fields = eventFields(event)
  const metadata = fields.metadata && typeof fields.metadata === 'object'
    ? fields.metadata as Record<string, unknown>
    : undefined
  if (textField(metadata?.source) === 'execution_prompt') return true

  const hasStepScope = Boolean(textField(metadata?.current_step_id) || textField(metadata?.step_name))
  return fields.turn === 0 && hasStepScope
}

export function internalTranscriptMessageTitle(event: PollingEvent): string {
  const content = textField(eventFields(event).content)
  if (/^\[AUTO-NOTIFICATION\]/.test(content)) {
    const agent = content.match(/^\[AUTO-NOTIFICATION\]\s*Agent\s+'([^']+)'/i)?.[1]
    const status = content.match(/completed\s+—\s+status=([a-z_]+)/i)?.[1]
    return agent
      ? `${agent}${status ? ` · ${status.replace(/_/g, ' ')}` : ''}`
      : 'Automation update'
  }
  if (/^PULSE RUN CONTEXT\./.test(content)) return 'Pulse run context'
  if (/^PULSE GATE\s*\/\s*WORKLIST\./.test(content)) return 'Pulse review plan'
  return 'Automation update'
}

function normalizedLifecycleName(fields: Record<string, unknown>): string {
  return (textField(fields.agent_name) || textField(fields.name))
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
}

function isMainAgentLifecycle(event: PollingEvent): boolean {
  const kind = textField(event.execution_kind) || textField(eventFields(event).execution_kind)
  const executionID = textField(event.execution_id) || textField(eventFields(event).execution_id)
  return kind === 'main_agent' || executionID.startsWith('main:')
}

function lifecycleFamily(event: PollingEvent): string {
  return LIFECYCLE_EVENT_FAMILIES[event.type || '']?.family || ''
}

function lifecycleExecutionID(event: PollingEvent): string {
  const fields = eventFields(event)
  const metadata = fields.metadata && typeof fields.metadata === 'object'
    ? fields.metadata as Record<string, unknown>
    : undefined

  // Lifecycle events for one message-sequence item do not always agree on
  // the outer execution_id. The generic background event carries the item's
  // agent_id directly, while the richer orchestrator event can retain the
  // parent workflow-step execution_id and put that same agent_id in metadata.
  // The agent id is the identity of the lifecycle being announced; the outer
  // execution id is only its container and remains the legacy fallback.
  return textField(fields.agent_id) ||
    textField(metadata?.agent_id) ||
    textField(event.execution_id) ||
    textField(fields.execution_id)
}

// The server and the delegated agent both announce the SAME execution, and they
// decorate the name differently at each end -- "Pulse reviewer: pulse 2026 07 27
// bug review" against "Background: Pulse reviewer - pulse-2026-07-27-bug-review".
// Including the name in the dedupe key therefore made one reviewer render as two
// agents. Normalising those strings is a losing game (they differ by word order,
// separators and prefix), so identity comes from the execution id instead.
//
// The name still has a job: sibling agents can legitimately share one execution
// id -- an eval step emits "...route-val-2" and "...route-val-3" under the same
// id -- and those must stay apart. The distinguishing signal is whether a single
// family emits more than one start for that execution: aliases arrive one per
// family, real siblings repeat within a family. Measured across 1101 executions
// in stored history, exactly 2 fall in the latter group.
function aliasCollapsibleExecutions(events: PollingEvent[]): Set<string> {
  const perExecutionFamilyCounts = new Map<string, Map<string, number>>()
  for (const event of events) {
    const descriptor = LIFECYCLE_EVENT_FAMILIES[event.type || '']
    if (!descriptor?.start) continue
    const executionID = lifecycleExecutionID(event)
    if (!executionID) continue
    const families = perExecutionFamilyCounts.get(executionID) || new Map<string, number>()
    const family = lifecycleFamily(event)
    families.set(family, (families.get(family) || 0) + 1)
    perExecutionFamilyCounts.set(executionID, families)
  }

  const collapsible = new Set<string>()
  for (const [executionID, families] of perExecutionFamilyCounts) {
    let repeats = false
    for (const count of families.values()) {
      if (count > 1) repeats = true
    }
    if (!repeats) collapsible.add(executionID)
  }
  return collapsible
}

function lifecycleKey(event: PollingEvent, aliasExecutions?: Set<string>): string | null {
  const descriptor = LIFECYCLE_EVENT_FAMILIES[event.type || '']
  if (!descriptor) return null
  const fields = eventFields(event)
  const name = normalizedLifecycleName(fields)

  // The server emits a generic background lifecycle event and the delegated
  // LLM emits an orchestrator lifecycle event for the same execution. Treat
  // those as aliases, not two agents. The execution id is shared across both
  // event families; the name protects sequence items that share a parent
  // execution from collapsing into one another.
  const executionID = lifecycleExecutionID(event)
  if (executionID) {
    // Alias case: one start per family for this execution, so the differing
    // names are two labels for the same agent.
    if (aliasExecutions?.has(executionID)) return `execution:${executionID}`
    return `execution:${executionID}:${name}`
  }

  // Correlation is shared by a start/end pair. Include the agent identity so
  // sibling agents that happen to share the same workflow correlation cannot
  // hide one another's live card.
  const correlation = textField(event.correlation_id) || textField(fields.correlation_id)
  if (correlation && name) return `correlation:${correlation}:${name}`
  return null
}

function lifecycleStartRichness(event: PollingEvent): number {
  const fields = eventFields(event)
  let score = event.type === 'orchestrator_agent_start' ? 10 : 0
  if (textField(fields.provider)) score += 4
  if (textField(fields.model_id)) score += 4
  if (textField(fields.agent_type)) score += 2
  if (fields.input_data && typeof fields.input_data === 'object') score += 1
  if (textField(fields.objective) || textField(fields.instruction)) score += 1
  return score
}

export function collapseCompletedLifecycleStarts(events: PollingEvent[]): PollingEvent[] {
  const openStarts = new Map<string, PollingEvent>()
  const hiddenStarts = new Set<PollingEvent>()
  const aliasExecutions = aliasCollapsibleExecutions(events)

  for (const event of events) {
    const descriptor = LIFECYCLE_EVENT_FAMILIES[event.type || '']
    const key = lifecycleKey(event, aliasExecutions)
    if (!descriptor || !key) continue
    // The main agent's own "Agent Started" card says nothing the user's
    // message above it does not already say, and it outlived every turn in
    // every product: the start arrives over SSE only, the store never keeps
    // it, so no later agent_end ever matched it and the card stayed under
    // finished answers ("Agent Started: simple | Model: … | Provider: …").
    // Sub-agent and background lifecycles keep their cards.
    if (descriptor.start && isMainAgentLifecycle(event)) {
      hiddenStarts.add(event)
      continue
    }
    if (descriptor.start) {
      const current = openStarts.get(key)
      if (!current) {
        openStarts.set(key, event)
        continue
      }

      // Prefer the delegated agent's enriched start payload over the generic
      // background notification. On equal detail, the latest event wins.
      if (lifecycleStartRichness(event) >= lifecycleStartRichness(current)) {
        hiddenStarts.add(current)
        openStarts.set(key, event)
      } else {
        hiddenStarts.add(event)
      }
      continue
    }
    if (descriptor.terminal) {
      const start = openStarts.get(key)
      if (start) {
        hiddenStarts.add(start)
        openStarts.delete(key)
      }
    }
  }

  // A restored trace can carry the main agent's start without its agent_end
  // (the persisted UI trace is bounded), while the turn's unified_completion
  // is present. A start whose turn has completed is not running; keep it out
  // rather than rendering a "Started" card under a finished answer.
  const completedExecutions = new Set<string>()
  for (const event of events) {
    if (event.type !== 'unified_completion') continue
    const executionID = lifecycleExecutionID(event)
    if (executionID) completedExecutions.add(executionID)
  }
  for (const start of openStarts.values()) {
    if (lifecycleFamily(start) !== 'agent') continue
    const executionID = lifecycleExecutionID(start)
    if (executionID && completedExecutions.has(executionID)) hiddenStarts.add(start)
  }

  // Only ONE completion card per execution. Starts were already collapsed
  // above, but terminal events never were, and they arrive in bulk: the server
  // reports the background execution finishing, the delegated agent reports its
  // own agent_end, and a retried step can emit several orchestrator_agent_end
  // events for the same work. Measured over stored history, 585 of 1562
  // executions emit more than one -- which is why a finished reviewer showed
  // "Agent Completed" and "Pulse Reviewer: ... completed (2m46s)" stacked.
  //
  // The last one wins rather than the first: a failure reported after an end
  // is the outcome that actually held, and the newest card carries the final
  // duration.
  const lastTerminalByKey = new Map<string, PollingEvent>()
  for (const event of events) {
    const descriptor = LIFECYCLE_EVENT_FAMILIES[event.type || '']
    if (!descriptor?.terminal) continue
    const key = lifecycleKey(event, aliasExecutions)
    if (!key) continue
    lastTerminalByKey.set(key, event)
  }
  const supersededTerminals = new Set<PollingEvent>()
  for (const event of events) {
    const descriptor = LIFECYCLE_EVENT_FAMILIES[event.type || '']
    if (!descriptor?.terminal) continue
    const key = lifecycleKey(event, aliasExecutions)
    if (!key) continue
    if (lastTerminalByKey.get(key) !== event) supersededTerminals.add(event)
  }

  return events.filter(event => !hiddenStarts.has(event) && !supersededTerminals.has(event))
}

// The lifecycle-start event type that can carry the SAME content a following
// "execution_prompt" user_message duplicates, keyed by which field holds it —
// orchestrator_agent_start uses user_message (base_orchestrator_agent.go),
// background_agent_started uses instruction (BackgroundAgentStartedEvent).
const KICKOFF_CONTENT_FIELD: Record<string, string> = {
  orchestrator_agent_start: 'user_message',
  background_agent_started: 'instruction',
}

function metadataField(fields: Record<string, unknown>, key: string): string {
  const metadata = fields.metadata
  if (!metadata || typeof metadata !== 'object') return ''
  return textField((metadata as Record<string, unknown>)[key])
}

/**
 * A step-scoped, turn-zero user_message tagged "execution_prompt" by the
 * backend's context-aware bridge (context_aware_bridge.go) is the prompt that
 * kicked an execution off. OrchestratorAgentStartEventDisplay now renders
 * that same content inline, always visible, as its Task section — so once a
 * lifecycle-start event with non-empty content has been seen in THIS
 * terminal's own scope, a following execution_prompt message is provably a
 * duplicate and is dropped.
 *
 * Deliberately conservative: the execution_prompt tag is applied broadly to
 * any step-scoped turn-zero message (context_aware_bridge.go), including
 * execution kinds whose own start event does not carry the content — for
 * those, no preceding start event will have non-empty content, so this never
 * drops anything. Never guess from the tag alone.
 */
function dropDuplicateExecutionPromptMessages(events: PollingEvent[]): PollingEvent[] {
  let precedingStartHasContent = false
  return events.filter(event => {
    const contentField = KICKOFF_CONTENT_FIELD[event.type || '']
    if (contentField) {
      precedingStartHasContent = Boolean(textField(eventFields(event)[contentField]))
      return true
    }
    if (event.type === 'user_message' && precedingStartHasContent) {
      const fields = eventFields(event)
      if (metadataField(fields, 'source') === 'execution_prompt') return false
    }
    return true
  })
}

function normalizedUserMessageText(event: PollingEvent): string {
  if (event.type !== 'user_message') return ''
  return textField(eventFields(event).content).replace(/\s+/g, ' ').toLowerCase()
}

function isFrontendUserMessage(event: PollingEvent): boolean {
  return event.type === 'user_message' && Boolean(event.id?.startsWith('user-message-'))
}

/**
 * A submitted chat message exists locally before the server acknowledgement
 * and can then return through durable history with a different event id. Keep
 * one row when those two carriers are adjacent and have identical text.
 *
 * Adjacency is the important safety boundary. Two identical messages entered
 * intentionally remain visible when both are local or both are durable, and
 * an older identical turn separated by an agent response is never collapsed.
 */
function dropAdjacentFrontendUserEchoes(events: PollingEvent[]): PollingEvent[] {
  const visible: PollingEvent[] = []
  for (const event of events) {
    const previous = visible.at(-1)
    const content = normalizedUserMessageText(event)
    const sameAdjacentUserMessage = Boolean(
      content &&
      previous?.type === 'user_message' &&
      normalizedUserMessageText(previous) === content,
    )
    if (!sameAdjacentUserMessage || !previous) {
      visible.push(event)
      continue
    }

    const previousIsFrontend = isFrontendUserMessage(previous)
    const currentIsFrontend = isFrontendUserMessage(event)
    if (previousIsFrontend === currentIsFrontend) {
      visible.push(event)
      continue
    }

    // Prefer the frontend carrier: it owns the immediate accepted timestamp
    // and, for live input, the server message receipt used by reconciliation.
    if (currentIsFrontend) visible[visible.length - 1] = event
  }
  return visible
}

function isTranscriptEvent(event: PollingEvent): boolean {
  if (NON_TRANSCRIPT_TYPES.has(event.type || '')) return false
  if (isRunToolEnd(event)) return false
  if (isContainerTranscriptNoise(event)) return false
  if (isMessageSequenceWrapperEvent(event)) return false
  if (event.type === 'agent_end' || event.type === 'unified_completion') {
    const fields = eventFields(event)
    const failed = fields.success === false || Boolean(textField(fields.error))
    // The panel header already owns the terminal's state. A successful empty
    // lifecycle card only repeats “Completed” after the real reply and makes
    // the conversation look like a trace log. Retained coding-agent turns in
    // particular emit an empty unified_completion solely to settle the live
    // terminal lifecycle; it is transport state, not a chat message.
    if (!failed && !answerText(event)) return false
  }
  return true
}

// Apply the child-execution boundary from the Formatted View Visibility
// Contract above. The main conversation must not flatten child payloads as if
// the main agent said them, and product correctness must not depend on the
// diagnostics-only child-terminal rail.
function isProductMainConversationEvent(event: PollingEvent): boolean {
  if (!isTranscriptEvent(event)) return false
  const fields = eventFields(event)
  const metadata = fields.metadata && typeof fields.metadata === 'object'
    ? fields.metadata as Record<string, unknown>
    : undefined
  const kind = (textField(event.execution_kind) || textField(fields.execution_kind) || textField(metadata?.execution_kind))
    .toLowerCase()
  if (kind && !MAIN_AGENT_EXECUTION_KINDS.has(kind)) return false

  const executionID = (
    textField(event.execution_id) ||
    textField(fields.execution_id) ||
    textField(metadata?.execution_id)
  ).toLowerCase()
  if (executionID &&
    !executionID.startsWith('main:') &&
    !executionID.startsWith('session:') &&
    (executionID.startsWith('workflow-step:') ||
      executionID.startsWith('delegation-') ||
      executionID.startsWith('background-') ||
      executionID.startsWith('workflow-full-'))) {
    return false
  }

  const stepID = textField(metadata?.current_step_id) || textField(metadata?.workflow_step_id) || textField(metadata?.step_id)
  return !stepID || stepID.startsWith('main_agent:')
}

function isToolBatchEvent(event: PollingEvent): boolean {
  if (runActivity(event) || isRunToolEnd(event)) return false
  const type = event.type || ''
  if (type === 'llm_generation_end') return isEmptyGenerationEnd(event)
  return TOOL_CALL_TYPES.has(type) || BATCH_BRIDGE_TYPES.has(type)
}

// A batch is only worth collapsing if it contains real tool calls — a stray
// token_usage between two messages should stay inline, not become a control
// that hides nothing.
// Counting only starts left an end whose start was not retained (a session
// restored from a trimmed trace, a bridge call whose start landed before the
// turn boundary) outside any batch, so it rendered as a full standalone
// "Command Completed" card instead of folding into the "N tool calls" chip.
// pairToolCalls already builds a card from an end alone; count what it builds.
function countRealToolCalls(events: PollingEvent[]): number {
  const startIDs = new Set<string>()
  let starts = 0
  let idlessStartsPending = 0
  for (const event of events) {
    if (event.type !== 'tool_call_start') continue
    starts += 1
    const id = textField(toolCallField(event, 'tool_call_id'))
    if (id) startIDs.add(id)
    else idlessStartsPending += 1
  }
  let orphans = 0
  for (const event of events) {
    if (event.type !== 'tool_call_end' && event.type !== 'tool_call_error') continue
    const id = textField(toolCallField(event, 'tool_call_id'))
    if (id) {
      if (!startIDs.has(id)) orphans += 1
    } else if (idlessStartsPending > 0) {
      idlessStartsPending -= 1 // pairs with an id-less start, in order
    } else {
      orphans += 1
    }
  }
  return starts + orphans
}

export function toolBatchLabel(events: PollingEvent[]): string {
  const names = new Set<string>()
  for (const event of events) {
    if (event.type !== 'tool_call_start') continue
    const data = event.data as Record<string, unknown> | undefined
    const payload = (data?.data as Record<string, unknown>) || data
    const name = payload?.tool_name ?? payload?.name
    if (typeof name === 'string' && name.trim()) names.add(name.trim())
  }
  const list = Array.from(names)
  if (list.length === 0) return ''
  if (list.length <= 2) return list.join(', ')
  return `${list.slice(0, 2).join(', ')} +${list.length - 2} more`
}

export type TranscriptItem =
  | { kind: 'event'; key: string; event: PollingEvent }
  | { kind: 'tools'; key: string; events: PollingEvent[]; toolCount: number }
  /** Consecutive conversation_thinking events: one collapsible block, like tools. */
  | { kind: 'thinking'; key: string; events: PollingEvent[]; text: string; assistantUpdate?: boolean }

const TURN_FAILURE_EVENT_TYPES = new Set(['llm_generation_error', 'conversation_error', 'agent_error', 'context_cancelled'])

/**
 * turnFailureText returns the error a transcript event carries for the turn,
 * or '' when it is not a failure. A failed turn is reported by up to three
 * protocol events in a row (llm_generation_error, conversation_error, then a
 * unified_completion whose status is error) that all carry the same text.
 */
export function turnFailureText(event: PollingEvent): string {
  const envelope = event.data as Record<string, unknown> | undefined
  const inner = envelope?.data as Record<string, unknown> | undefined
  const payload = inner && typeof inner === 'object' ? inner : (envelope ?? {})
  const str = (v: unknown) => (typeof v === 'string' ? v.trim() : '')
  const type = event.type || ''
  if (TURN_FAILURE_EVENT_TYPES.has(type)) {
    return str(payload.error) || str(payload.message) || str(payload.content)
  }
  if (type === 'unified_completion') {
    const error = str(payload.error)
    if (error) return error
    if (str(payload.status).toLowerCase() === 'error') return str(payload.final_result) || str(payload.content)
  }
  return ''
}

/**
 * collapseTurnFailures keeps one failure per turn — the last one the server
 * sent — so the reader sees a single explanation instead of the same error
 * three times. A turn is the stretch between user messages.
 */
export function collapseTurnFailures(items: TranscriptItem[]): TranscriptItem[] {
  const out: TranscriptItem[] = []
  let lastFailureIndex = -1
  for (const item of items) {
    if (item.kind === 'event' && item.event.type === 'user_message') {
      lastFailureIndex = -1
      out.push(item)
      continue
    }
    if (item.kind === 'event' && turnFailureText(item.event)) {
      if (lastFailureIndex >= 0) out.splice(lastFailureIndex, 1)
      lastFailureIndex = out.length
    }
    out.push(item)
  }
  return out
}

export function isAssistantUpdate(event: PollingEvent): boolean {
  const envelope = event.data as Record<string, unknown> | undefined
  const inner = envelope?.data as Record<string, unknown> | undefined
  const fields = inner || envelope || {}
  if (metadataField(fields, 'presentation') === 'assistant_update') return true

  // Pi historically sent its narrated progress through the reasoning stream.
  // Treat those events as assistant updates so restored runs use the same
  // presentation as newly emitted Pi events.
  const provider = metadataField(fields, 'provider').trim().toLowerCase().replaceAll('_', '-')
  return provider === 'pi-cli'
}

function transcriptThinkingText(event: PollingEvent): string {
  const envelope = event.data as Record<string, unknown> | undefined
  const inner = envelope?.data as Record<string, unknown> | undefined
  const raw = inner?.thinking ?? envelope?.thinking
  return typeof raw === 'string' ? raw.trim() : ''
}

function isEmptyThinkingActivity(event: PollingEvent): boolean {
  const envelope = event.data as Record<string, unknown> | undefined
  const inner = envelope?.data as Record<string, unknown> | undefined
  const fields = inner || envelope || {}
  const metadata = fields.metadata
  return Boolean(metadata && typeof metadata === 'object' && (metadata as Record<string, unknown>).thinking_active === true)
}

function dropStaleEmptyThinkingActivity(events: PollingEvent[]): PollingEvent[] {
  const newestIndex = events.length - 1
  return events.filter((event, index) => {
    if (event.type !== 'conversation_thinking') return true
    if (transcriptThinkingText(event)) return true
    // An empty Claude thinking block is only a transient live-state marker.
    // Remove it before grouping once anything follows; otherwise invisible
    // markers split one consecutive tool run into many "1 tool call" batches.
    return index === newestIndex && isEmptyThinkingActivity(event)
  })
}

/**
 * selectTerminalEvents scopes the session's event stream to ONE terminal.
 *
 * The rail is the hierarchy now: every agent and sub-agent owns its own
 * terminal entry, so this is the ONLY place that decides which conversation a
 * terminal shows. Deliberately reuses getOwnedTerminalOwnerKeys /
 * getTerminalOwnerPayload — the SAME functions the (now-removed) tree used to
 * decide which owned terminal's internal log panel an event streamed into —
 * rather than re-deriving a narrower subset of that matching. An event's
 * owner id can live in any of ~20 different fields depending on event type
 * (execution_id, delegation_id, background_agent_id, agent_id, several
 * metadata variants, workflow-step composite ids, ...); that enumeration
 * already exists once and callers must not re-narrow it.
 *
 * Two cases, because "main" is not an owned id (getOwnedTerminalOwnerKeys
 * deliberately excludes anything starting with "main:"):
 *
 *   - Owned terminal (a workflow step, message-sequence item, delegation,
 *     background agent, ...): an event belongs to it iff its owner keys
 *     include this terminal's terminal_id (== `${session_id}:${owner_id}`,
 *     confirmed against the backend's own construction of that id).
 *   - Main-agent terminal: an event belongs to it iff it is in the same
 *     session and NOT claimed by any sibling owned terminal. `siblingTerminals`
 *     should be the full terminal list for the session — omit it only when
 *     that list is genuinely unavailable; the fallback below is deliberately
 *     permissive (shows too much rather than silently hiding a live turn).
 */
// A terminal's own opening lifecycle card duplicates its panel header: the
// header already names this agent (title, "Sub-agent", state), so a
// background_agent_started/orchestrator_agent_start card whose execution_id
// IS this terminal's own execution_id adds nothing above what the reader
// already knows just from having opened this terminal -- it is the same
// bug as the earlier "Full Run" container row, just for a plain sub-agent
// instead of a full_run: "Review artifact drift review" (header) followed
// immediately by "Review Artifact Drift Review" (its own start card).
//
// Only the START half is dropped. The matching completion card is kept: it
// carries genuinely new information the header does not show (status,
// duration), where the start card only repeats the name.
//
// A CHILD's start card is never touched -- a child has its own distinct
// execution_id, so this only ever matches the terminal's own root event.
function isOwnTerminalLifecycleStart(event: PollingEvent, terminal: TerminalSnapshot): boolean {
  const descriptor = LIFECYCLE_EVENT_FAMILIES[event.type || '']
  if (!descriptor?.start) return false
  const terminalExecutionId = (terminal.execution_id || '').trim()
  if (!terminalExecutionId || lifecycleExecutionID(event) !== terminalExecutionId) return false

  // A name-only opening card merely repeats the terminal header and stays
  // hidden. A start carrying the actual task is different: removing it made a
  // finished step open on "Completed", so the transcript looked backwards
  // and the user could not see what the agent had been asked to do.
  const kickoffField = KICKOFF_CONTENT_FIELD[event.type || '']
  if (kickoffField && textField(eventFields(event)[kickoffField])) return false
  return true
}

function compareConversationEvents(a: PollingEvent, b: PollingEvent): number {
  // The main chat survives server restarts; EventStore's per-session sequence
  // counter does not. Comparing those counters across restored and live events
  // placed yesterday's replies below today's work. Prefer the event timestamp
  // here, retaining sequence as a tie-breaker for same-time messages.
  const aTime = Date.parse(a.timestamp || '')
  const bTime = Date.parse(b.timestamp || '')
  if (Number.isFinite(aTime) && Number.isFinite(bTime) && aTime !== bTime) return aTime - bTime
  return compareTerminalEvents(a, b)
}

export function selectTerminalEvents(
  events: PollingEvent[] | undefined,
  terminal: TerminalSnapshot | null | undefined,
  siblingTerminals?: TerminalSnapshot[],
  keepInteractionKinds?: ReadonlySet<string>,
): PollingEvent[] {
  if (!events || events.length === 0) return []
  events = normalizeTranscriptChunkEvents(events)

  // The normal Chat/Schedule product surface is the main conversation, not a
  // terminal inspector. It intentionally excludes workflow-step and child
  // execution payloads while keeping user messages and main-agent outcomes.
  // TerminalCenter still passes a terminal and keeps the owner scoping below
  // for developer diagnostics.
  if (!terminal) {
    return events
      .filter(event => isProductMainConversationEvent(event) || isKeptInteraction(event, keepInteractionKinds))
      .map((event, index) => ({ event, index }))
      .sort((a, b) => {
        const compared = compareConversationEvents(a.event, b.event)
        return compared !== 0 ? compared : a.index - b.index
      })
      .map(entry => entry.event)
  }
  const sessionId = terminal.session_id?.trim()
  if (!sessionId) return []

  const eventOwnerKeys = (event: PollingEvent): string[] =>
    getOwnedTerminalOwnerKeys(event, getTerminalOwnerPayload(event))

  let matched: PollingEvent[]

  if (isMainAgentTerminal(terminal)) {
    const ownedTerminalIds = new Set(
      (siblingTerminals ?? [])
        .filter(t => t.session_id === sessionId && t.terminal_id !== terminal.terminal_id && !isMainAgentTerminal(t))
        .map(t => t.terminal_id),
    )
    matched = events.filter(event => {
      if (event.session_id?.trim() !== sessionId) return false
      if (ownedTerminalIds.size === 0) return true // no sibling list: don't hide anything
      return !eventOwnerKeys(event).some(key => ownedTerminalIds.has(key))
    })
  } else {
    const terminalKey = terminal.terminal_id?.trim()
    if (!terminalKey) return []
    // A retained workflow execution root and its concrete evaluation/step
    // transcripts share an execution_id. The root is an orchestrator summary,
    // not a second copy of every child conversation. Exclude events claimed by
    // those concrete child terminals so each rail entry owns one transcript.
    const childTerminalIds = new Set(
      (siblingTerminals ?? [])
        .filter(candidate => (
          candidate.session_id === sessionId &&
          candidate.terminal_id !== terminalKey &&
          Boolean(terminal.execution_id) &&
          candidate.execution_id === terminal.execution_id &&
          Boolean(candidate.step_id) &&
          candidate.terminal_id.includes(':workflow-step:')
        ))
        .map(candidate => candidate.terminal_id),
    )
    matched = events.filter(event => {
      const ownerKeys = eventOwnerKeys(event)
      return ownerKeys.includes(terminalKey) &&
        !ownerKeys.some(key => childTerminalIds.has(key))
    })
  }

  // Within an owned execution, use the retained terminal-event loader's order.
  // Lifecycle events can be flushed in a batch with timestamps that do not
  // reflect their persisted sequence; sorting those timestamp-first made a
  // completion appear above the task work it completed.
  return matched
    .filter(event => (isTranscriptEvent(event) && !TERMINAL_RAIL_HIDDEN_TYPES.has(event.type || '')) || isKeptInteraction(event, keepInteractionKinds))
    .filter(event => !isOwnTerminalLifecycleStart(event, terminal))
    .map((event, index) => ({ event, index }))
    .sort((a, b) => {
      const compared = isMainAgentTerminal(terminal)
        ? compareConversationEvents(a.event, b.event)
        : compareTerminalEvents(a.event, b.event)
      if (compared !== 0) return compared
      return a.index - b.index // stable for equal/unparseable timestamps
    })
    .map(entry => entry.event)
}

/**
 * buildTranscriptItems flattens events into render items, folding consecutive
 * tool-call runs into one collapsible group.
 *
 * Collapsing is not cosmetic: the tree relied on auto-collapse to keep rendered
 * node counts down, and a flat list has no equivalent. This replaces it.
 */
// Events that render a completion card carrying the turn's answer.
const COMPLETION_ANSWER_TYPES = new Set([
  'unified_completion',
  'agent_end',
  'orchestrator_agent_end',
  'background_agent_completed',
])

function answerText(event: PollingEvent): string {
  const fields = eventFields(event)
  return textField(fields.content) || textField(fields.final_result) || textField(fields.result)
}

function comparableAnswer(text: string): string {
  return text.replace(/\s+/g, ' ').trim().toLowerCase()
}

// llm_generation_end has to render its answer when nothing else will -- that is
// what stopped tool-heavy turns (a CDP browser step) from showing no response
// at all. But a completion card usually repeats the very same text, and then
// the reader sees the answer twice in a row. Keep the completion card, which
// also carries the outcome, and drop the generation-end copy.
//
// Compared on normalised text with containment rather than equality: the two
// carriers legitimately differ at the edges (a completion card may prepend a
// status line, or hold prose from before a trailing tool call), and the point
// is only to detect that the reader is being shown the same answer twice.
//
// The same duplication happens between two completion-card types themselves:
// a turn can fire both, say, agent_end and unified_completion carrying the
// identical final_result, and neither was covered by the llm_generation_end
// check above -- the reader saw the same "Agent · Response" block twice, back
// to back, with matching duration and timestamp. Collapse those too, but only
// against the MOST RECENT completion card and only when they share the same
// lifecycleExecutionID: two different sub-agents/executions can legitimately
// report the identical short answer (e.g. "done"), and merging across
// executions would silently hide one agent's real result -- exact scoping,
// exact text equality (not containment) keeps this to the narrow case it is
// meant for.
function dropAnswersRepeatedByCompletionCard(events: PollingEvent[]): PollingEvent[] {
  const completionAnswers: string[] = []
  for (const event of events) {
    if (!COMPLETION_ANSWER_TYPES.has(event.type || '')) continue
    const text = comparableAnswer(answerText(event))
    if (text) completionAnswers.push(text)
  }

  let lastCompletion: { executionID: string; text: string } | null = null
  return events.filter(event => {
    const type = event.type || ''
    if (COMPLETION_ANSWER_TYPES.has(type)) {
      const text = comparableAnswer(answerText(event))
      if (!text) return true
      const executionID = lifecycleExecutionID(event)
      if (lastCompletion && lastCompletion.executionID === executionID && lastCompletion.text === text) {
        return false
      }
      lastCompletion = { executionID, text }
      return true
    }

    if (type !== 'llm_generation_end' || completionAnswers.length === 0) return true
    const text = comparableAnswer(answerText(event))
    // Exact equality is definitive even for a one-word reply. The backend
    // intentionally carries the final answer on both llm_generation_end and
    // unified_completion; keeping short exact matches made replies such as
    // "Hi! Ready when you are." appear twice. Only the looser containment
    // comparison needs a minimum length to avoid hiding coincidentally similar
    // short messages.
    if (completionAnswers.some(done => done === text)) return false
    if (text.length < 24) return true
    return !completionAnswers.some(done => done.includes(text) || text.includes(done))
  })
}

export function buildTranscriptItems(events: PollingEvent[]): TranscriptItem[] {
  // Filter wrapper noise before lifecycle deduplication. Otherwise a generic
  // completion can supersede the richer delegated completion and then be
  // removed itself, accidentally hiding both records.
  const transcriptEvents = normalizeTranscriptChunkEvents(events).filter(isTranscriptEvent)
  const visibleEvents = dropStaleEmptyThinkingActivity(dropAdjacentFrontendUserEchoes(dropAnswersRepeatedByCompletionCard(
    dropDuplicateExecutionPromptMessages(collapseCompletedLifecycleStarts(transcriptEvents)),
  )))
  const items: TranscriptItem[] = []
  let cursor = 0

  while (cursor < visibleEvents.length) {
    const event = visibleEvents[cursor]
    if (!isTranscriptEvent(event)) {
      cursor += 1
      continue
    }
    if (event.type === 'conversation_thinking') {
      // The agent's running commentary reads as one block per stretch, opened
      // while it is the newest thing on screen and minimised once the answer
      // (or a tool batch) follows -- the same shape tool calls already have.
      const batch: PollingEvent[] = []
      const assistantUpdate = isAssistantUpdate(event)
      while (cursor < visibleEvents.length && visibleEvents[cursor].type === 'conversation_thinking' && isAssistantUpdate(visibleEvents[cursor]) === assistantUpdate) {
        batch.push(visibleEvents[cursor])
        cursor += 1
      }
      const text = batch.map(transcriptThinkingText).filter(Boolean).join('\n\n')
      // Claude can expose that it started thinking while intentionally
      // redacting the reasoning text. Keep that marker only while it is the
      // newest event; once a tool, reply, or failure follows it becomes stale
      // history and should disappear rather than leave an empty card behind.
      const activeEmptyThinking = !text && cursor === visibleEvents.length && batch.some(isEmptyThinkingActivity)
      if (text || activeEmptyThinking) {
        items.push({ kind: 'thinking', key: batch[0].id || `thinking-${items.length}`, events: batch, text, assistantUpdate })
      }
      continue
    }
    if (!isToolBatchEvent(event)) {
      items.push({ kind: 'event', key: event.id || `evt-${cursor}`, event })
      cursor += 1
      continue
    }

    const batch: PollingEvent[] = []
    while (cursor < visibleEvents.length && isToolBatchEvent(visibleEvents[cursor])) {
      batch.push(visibleEvents[cursor])
      cursor += 1
    }

    const toolCount = countRealToolCalls(batch)
    if (toolCount === 0) {
      for (const item of batch) {
        items.push({ kind: 'event', key: item.id || `evt-${items.length}`, event: item })
      }
      continue
    }

    items.push({
      kind: 'tools',
      key: batch[0].id || `tools-${items.length}`,
      events: batch,
      toolCount,
    })
  }

  return items
}

// A step run or a full-workflow run is the workflow's own headline action, not
// plumbing. The transcript gives it a compact activity row of its own instead
// of folding it into an "N tool calls" chip, the same way a presentation gets
// one — so "it is running your workflow now" is visible at a glance.
const RUN_TOOL_LABELS: Record<string, string> = {
  execute_step: 'Running step',
  run_full_workflow: 'Running workflow',
  run_full_evaluation: 'Running evaluation',
}

function runToolLabel(event: PollingEvent): string {
  return RUN_TOOL_LABELS[mcpToolDisplayName(textField(toolCallField(event, 'tool_name'))).name] ?? ''
}

/**
 * The row says a run STARTED, and nothing about how it ended.
 *
 * `execute_step` returns "Step … started in background" the moment the step is
 * launched, so its tool_call_end means "launched", not "finished" — an earlier
 * version of this row drew a green tick there and claimed a run had completed
 * seconds after it began. Completion arrives separately, as the
 * [AUTO-NOTIFICATION] the backend sends when the step really finishes, and that
 * already has its own row. So: one row per run, and only a failed launch is
 * reported as an outcome.
 */
export function runActivity(event: PollingEvent): { label: string; target: string; state: 'started' | 'failed' } | null {
  const type = event.type || ''
  if (type !== 'tool_call_start' && type !== 'tool_call_error') return null
  const label = runToolLabel(event)
  if (!label) return null
  const args = toolCallArgs(event)
  let target = ''
  if (args) {
    try {
      const parsed = JSON.parse(args) as Record<string, unknown>
      target = [parsed.step_id, parsed.group_name].filter(v => typeof v === 'string' && v).join(' · ')
    } catch {
      /* a partially streamed argument blob is not worth reporting on */
    }
  }
  return { label, target, state: type === 'tool_call_error' ? 'failed' : 'started' }
}

/** The end of a run tool's launch call: its row was already drawn by the start. */
function isRunToolEnd(event: PollingEvent): boolean {
  return event.type === 'tool_call_end' && runToolLabel(event) !== ''
}

export interface PairedToolCall {
  key: string
  /** Friendly tool name — MCP's `mcp__<server>__<tool>` reduced to `<tool>`. */
  name: string
  /** MCP server that owns the tool, when the name encoded one. */
  server?: string
  /** start/end/error events for this call, in arrival order, for detail rendering. */
  events: PollingEvent[]
  status: 'running' | 'ok' | 'error'
  /**
   * Tool-call duration in NANOSECONDS, as Go's time.Duration serializes it.
   * Named for its real unit: this was previously `durationMs`, so the card divided
   * nanoseconds by 1000 and printed the microseconds with an `s` suffix — a 120ms
   * query rendered as "120193.5s".
   */
  durationNs?: number
  /** Arguments the model passed. Carried on the START event (tool_params). */
  args?: string
  /** What the tool returned. Carried on the END event. */
  result?: string
}

export interface ToolErrorContext {
  name?: string
  server?: string
  args?: string
}

/**
 * mcpToolDisplayName splits MCP's wire name into its parts.
 *
 * A tool arrives as `mcp__api-bridge__agent_browser`. Rendered raw it is the
 * least readable thing on screen, and the double underscores read as damage.
 * The server is worth keeping — just not welded into the name.
 */
export function mcpToolDisplayName(raw: string): { name: string; server?: string } {
  const match = /^mcp__(.+?)__(.+)$/.exec(raw.trim())
  if (!match) return { name: raw.trim() }
  return { name: match[2], server: match[1] }
}

/**
 * Tool payloads are normally strings, but coding-CLI and restored events can
 * preserve arguments/results as decoded JSON. Keep that structured detail
 * visible instead of treating it as absent merely because it is not a string.
 */
function toolCallDisplayText(value: unknown): string {
  if (typeof value === 'string') return value.trim()
  if (value == null) return ''
  try {
    const encoded = JSON.stringify(value, null, 2)
    return typeof encoded === 'string' ? encoded.trim() : ''
  } catch {
    return ''
  }
}

/** Arguments normally live under tool_params.arguments on the start event. */
function toolCallArgs(event: PollingEvent): string {
  const params = toolCallField(event, 'tool_params')
  if (params && typeof params === 'object') {
    const record = params as Record<string, unknown>
    const nested = toolCallDisplayText(record.arguments)
    if (nested) return nested
  }
  // CLI/replayed tool events have historically used these direct fields.
  return toolCallDisplayText(toolCallField(event, 'arguments')) ||
    toolCallDisplayText(toolCallField(event, 'tool_args')) ||
    toolCallDisplayText(toolCallField(event, 'input'))
}

function toolCallResult(event: PollingEvent): string {
  return toolCallDisplayText(toolCallField(event, 'result')) ||
    toolCallDisplayText(toolCallField(event, 'output')) ||
    // ToolCallErrorEvent carries its useful response as `error`, rather than
    // `result`. Without this fallback, an errored tool looks expandable only
    // when its start happened to retain arguments.
    toolCallDisplayText(toolCallField(event, 'error'))
}

function stringifyToolCallValue(value: unknown): string {
  const text = textField(value)
  if (text) return text
  if (!value || typeof value !== 'object') return ''
  try {
    return JSON.stringify(value)
  } catch {
    return ''
  }
}

function isCursorMCPWrapperName(rawName: string): boolean {
  // Cursor has emitted CallMcpTool with several spellings across its terminal
  // and structured transports. They all describe the same wrapper, not a
  // user-facing tool.
  const normalizedWrapperName = rawName.trim().toLowerCase().replace(/[^a-z0-9]/g, '')
  return normalizedWrapperName === 'callmcptool'
}

// Cursor represents every MCP invocation as its CallMcpTool wrapper. Preserve
// compatibility with events already recorded in that form: reveal the actual
// registered tool and its nested arguments instead of exposing the wrapper.
function normalizeCursorMCPToolCall(rawName: string, rawArgs: string): { name: string; args: string } {
  if (!isCursorMCPWrapperName(rawName)) return { name: rawName, args: rawArgs }

  try {
    const envelope = JSON.parse(rawArgs) as Record<string, unknown>
    const toolName = textField(envelope.toolName) || textField(envelope.tool_name)
    if (!toolName) return { name: rawName, args: rawArgs }
    const server = textField(envelope.server)
      || textField(envelope.serverName)
      || textField(envelope.serverIdentifier)
      || textField(envelope.providerIdentifier)
    const name = server ? `mcp__${server}__${toolName}` : toolName
    const nestedArgs = envelope.arguments ?? envelope.args ?? envelope.input
    return { name, args: stringifyToolCallValue(nestedArgs) || rawArgs }
  } catch {
    return { name: rawName, args: rawArgs }
  }
}

function toolCallField(event: PollingEvent, key: string): unknown {
  const fields = eventFields(event)
  return fields[key]
}

// Coding CLIs sometimes send a tool-start event before they have populated its
// arguments. Their completed structured transcript, included on
// llm_generation_end, contains the authoritative call id + arguments. Recover
// those values so a restored developer view remains useful after a reload.
function recoveredCodingToolArgs(events: PollingEvent[]): Map<string, string> {
  const recovered = new Map<string, string>()
  for (const event of events) {
    const fields = eventFields(event)
    // Live events carry generation_info directly. Persisted events preserve it
    // within metadata, so accept both shapes when restoring a conversation.
    const metadata = fields.metadata
    const metadataRecord = metadata && typeof metadata === 'object'
      ? metadata as Record<string, unknown>
      : undefined
    const generation = fields.generation_info ?? metadataRecord?.generation_info
    const generationRecord = generation && typeof generation === 'object'
      ? generation as Record<string, unknown>
      : undefined
    const intermediate = generationRecord?.coding_provider_intermediate_messages
    const intermediateRecord = intermediate && typeof intermediate === 'object'
      ? intermediate as Record<string, unknown>
      : undefined
    const messages = intermediateRecord?.messages
    if (!Array.isArray(messages)) continue

    for (const message of messages) {
      if (!message || typeof message !== 'object') continue
      const messageRecord = message as Record<string, unknown>
      const parts = messageRecord.Parts ?? messageRecord.parts
      if (!Array.isArray(parts)) continue
      for (const part of parts) {
        if (!part || typeof part !== 'object') continue
        const record = part as Record<string, unknown>
        const call = record.FunctionCall ?? record.functionCall ?? record.function_call
        if (!call || typeof call !== 'object') continue
        const callID = textField(record.ID) || textField(record.id) || textField(record.tool_call_id)
        const callRecord = call as Record<string, unknown>
        const args = stringifyToolCallValue(callRecord.Arguments ?? callRecord.arguments ?? callRecord.args)
        if (callID && args) {
          // Cursor has emitted a compound id containing its orchestration id
          // and provider fc_* id on separate lines. Index both aliases so a
          // retained lifecycle event can recover its authoritative arguments.
          recovered.set(callID, args)
          for (const alias of callID.split(/\s+/).filter(Boolean)) recovered.set(alias, args)
        }
      }
    }
  }
  return recovered
}

/**
 * pairToolCalls collapses the start/end (or start/error) events of one tool
 * call into a single item.
 *
 * The transcript previously rendered a "Tool Call Start" row and a "Tool Call
 * End" row for every call — two near-identical rows, neither showing what the
 * tool did, and the pair split apart whenever calls interleaved. A reader wants
 * one line per call: what ran, whether it worked, how long it took.
 *
 * Pairs on tool_call_id. Events without one (or an end with no matching start)
 * still get their own item, so nothing is ever dropped.
 */
export function pairToolCalls(events: PollingEvent[]): PairedToolCall[] {
  const out: PairedToolCall[] = []
  const byCallID = new Map<string, PairedToolCall>()

  for (const event of events) {
    const type = event.type || ''
    if (!TOOL_CALL_TYPES.has(type)) continue

    const callID = textField(toolCallField(event, 'tool_call_id'))
    const rawName = textField(toolCallField(event, 'tool_name'))
    const normalizedCall = normalizeCursorMCPToolCall(rawName, toolCallArgs(event))
    const existing = callID ? byCallID.get(callID) : undefined

    if (!existing) {
      // A few Cursor stream versions emit an orphan CallMcpTool *end* event
      // after the useful concrete tool event. With neither nested arguments
      // nor a resolved capability it is pure transport noise, so never show a
      // misleading "CallMcpTool" card to the user.
      if (isCursorMCPWrapperName(rawName) && normalizedCall.name === rawName && !normalizedCall.args) continue
      const { name, server } = mcpToolDisplayName(normalizedCall.name)
      const item: PairedToolCall = {
        key: event.id || `${type}-${out.length}`,
        name: name || 'tool',
        server: server || textField(toolCallField(event, 'server_name')) || undefined,
        events: [event],
        status: type === 'tool_call_error' ? 'error' : type === 'tool_call_end' ? 'ok' : 'running',
      }
      const duration = toolCallField(event, 'duration')
      if (typeof duration === 'number') item.durationNs = duration
      const args = normalizedCall.args
      if (args) item.args = args
      const result = toolCallResult(event)
      if (result) item.result = result
      out.push(item)
      if (callID) byCallID.set(callID, item)
      continue
    }

    existing.events.push(event)
    if (type === 'tool_call_error') existing.status = 'error'
    else if (type === 'tool_call_end' && existing.status !== 'error') existing.status = 'ok'
    const duration = toolCallField(event, 'duration')
    if (typeof duration === 'number') existing.durationNs = duration
    const args = normalizedCall.args
    if (args) existing.args = args
    const result = toolCallResult(event)
    if (result) existing.result = result
    // A start may lack the name that the end carries (and vice versa).
    if ((existing.name === 'tool' || existing.name === 'CallMcpTool') && normalizedCall.name) {
      const { name, server } = mcpToolDisplayName(normalizedCall.name)
      existing.name = name
      existing.server = existing.server || server
    }
  }

  const recoveredArgs = recoveredCodingToolArgs(events)
  for (const item of out) {
    if (item.args) continue
    for (const event of item.events) {
      const callID = textField(toolCallField(event, 'tool_call_id'))
      const args = recoveredArgs.get(callID)
      if (args) {
        item.args = args
        break
      }
    }

    const normalizedCall = normalizeCursorMCPToolCall(item.name, item.args || '')
    if (normalizedCall.name !== item.name || normalizedCall.args !== item.args) {
      const { name, server } = mcpToolDisplayName(normalizedCall.name)
      item.name = name
      item.server = item.server || server
      item.args = normalizedCall.args || item.args
    }
  }

  return out
}

/**
 * Error banners are rendered outside the chronological transcript, but tool
 * arguments normally exist only on the preceding start event. Join them by
 * tool_call_id and index the result by the error event's stable ID so a banner
 * can show the exact input without guessing across interleaved calls.
 */
export function toolErrorContextByEventID(events: PollingEvent[]): Map<string, ToolErrorContext> {
  const contexts = new Map<string, ToolErrorContext>()
  for (const pair of pairToolCalls(events)) {
    if (pair.status !== 'error') continue
    for (const event of pair.events) {
      if (event.type !== 'tool_call_error' || !event.id) continue
      contexts.set(event.id, {
        name: pair.name === 'tool' ? undefined : pair.name,
        server: pair.server,
        args: pair.args,
      })
    }
  }
  return contexts
}
