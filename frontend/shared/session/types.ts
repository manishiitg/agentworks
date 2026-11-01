// The session-event contract shared by every app that reads the agent
// server's event stream. Moved here from the
// AgentWorks api-types so a second app does not have to carry that whole
// file. The generated event schema stays where the generator writes it.
import type { PollingEventSchema } from '../../src/generated/event-types'

export type PollingEvent = PollingEventSchema & {
  id: string
  parent_id?: string
  hierarchy_level?: number
  span_id?: string
  trace_id?: string
  correlation_id?: string
  session_id?: string
  execution_id?: string
  parent_execution_id?: string
  execution_kind?: string
  terminal_owner_id?: string
  terminal_id?: string
  sequence?: number
  component?: string
  event_index?: number
}

export type RuntimePhase = 'starting' | 'running' | 'waiting' | 'idle' | 'completed' | 'failed' | 'canceled'

export interface RuntimeSnapshot {
  session_id: string
  generation: number
  revision: number
  phase: RuntimePhase
  reason?: string
  raw_session_status?: string
  foreground_turn: {
    busy: boolean
    has_cancel: boolean
    can_steer: boolean
    synthetic: boolean
  }
  child_executions?: Array<{
    execution_id: string
    kind?: string
    status: string
    started_at: string
    completed_at?: string
  }>
  background_agents?: Array<{
    agent_id: string
    status: string
    created_at: string
    completed_at?: string
  }>
  background_live: boolean
  terminals?: Array<{
    terminal_id: string
    execution_id?: string
    state: string
    active: boolean
    has_tmux: boolean
    updated_at: string
  }>
  terminal_busy: boolean
  waiting_for_user: boolean
  waiting_message?: string
  last_progress_at: string
  started_at: string
  completed_at?: string
  observed_at: string
}

export interface GetEventsResponse {
  events: PollingEvent[]
  has_more: boolean
  session_id: string
  session_status: string // Session status: "running", "completed", "error", "stopped", "inactive" (required - source of truth)
  display_status?: 'busy' | 'idle' | 'stopped'
  last_processed_index?: number // Last index processed in unfiltered array (for correct sinceIndex tracking when filtering)
  has_running_background_agents?: boolean // Whether background agents are still running for this session
  is_synthetic_turn?: boolean // True when running auto-notification turn (input remains locked as normal)
  can_steer?: boolean // True when a live foreground agent can accept steer injection
  runtime_state?: RuntimeSnapshot
  oldest_sequence?: number
  latest_sequence?: number
  // The viewer's queued messages the CLI has not taken yet (restore reads only).
  pending_messages?: PendingQueuedMessage[]
}

export interface PendingQueuedMessage {
  message_id?: string
  steering?: boolean
  delivery_error?: string
  client_message_id: string
  content: string
  queued_at?: string
  queue_position?: number
  /** The chat is being updated (new version or settings) before this message can go in. */
  refreshing?: boolean
  queue_reason?: 'starting' | 'running' | 'waiting' | 'refreshing'
  can_steer?: boolean
  /** Handed to its turn; the chat shows it as being sent until the turn's user row arrives. */
  started?: boolean
}

export interface TerminalEventsResponse {
  terminal_id: string
  events: PollingEvent[]
  has_older: boolean
  has_newer: boolean
  oldest_sequence?: number
  latest_sequence?: number
}

export interface TerminalSnapshot {
  terminal_id: string
  session_id: string
  owner_id?: string
  execution_id?: string
  parent_execution_id?: string
  execution_kind?: string
  label?: string
  scope?: string
  workflow_path?: string
  workflow_name?: string
  workflow_label?: string
  step_id?: string
  step_name?: string
  step_type?: string
  agent_name?: string
  display_title?: string
  display_meta?: string
  tmux_session?: string
  content_source?: 'tmux_pipe' | 'tmux_capture' | 'tmux_stream' | 'event_stream' | string
  // True for the short-lived rail row projected from the live execution tree
  // before the corresponding terminal/transcript snapshot has been retained.
  execution_tree_placeholder?: boolean
  // Rich step context — populated by the orchestrator's bridge for
  // workflow-step terminals. Used to render the transport-class chip
  // and the "step 3/7 · attempt 1 · triggered by X" meta row.
  step_index?: number
  step_total?: number
  parent_step_id?: string
  step_attempt?: number
  step_execution_mode?: string
  step_transport?: string
  step_triggered_by?: string
  content: string
  rows: TerminalSnapshotRow[]
  chunk_index: number
  active: boolean
  state?: 'running' | 'completed' | 'failed' | 'idle' | 'closing' | 'stale' | string
  process_state?: 'live' | 'closing' | 'closed' | string
  snapshot_kind?: 'live' | 'archived' | string
  close_reason?: string
  closes_at?: string
  retention_seconds?: number
  status: TerminalStatus
  created_at: string
  updated_at: string
}

export interface TerminalSnapshotRow {
  kind: string
  text?: string
  name?: string
  args?: string
  result?: string
  result_prefix?: '✓' | '✗' | string
}

export interface TerminalStatus {
  provider_label?: string
  status_text?: string
  assistant_preview?: string
  tool_summary?: string
  tool_name?: string
  tool_count?: number
  input_tokens?: number
  output_tokens?: number
  cache_creation_input_tokens?: number
  cache_read_input_tokens?: number
  total_input_tokens?: number
  total_output_tokens?: number
  cost_usd?: number
  // Raw provider statusline extras with no first-class field (context window,
  // git branch, rate limits, …). Carried through so nothing is dropped.
  status_meta?: Record<string, unknown>
  duration_ms?: number
  pre_validation_status?: 'passed' | 'failed' | string
  pre_validation_summary?: string
  pre_validation_passed_checks?: number
  pre_validation_failed_checks?: number
  pre_validation_total_checks?: number
}

export interface SSEEventMessage {
  events: PollingEvent[]
  session_status?: string
  display_status?: 'busy' | 'idle' | 'stopped'
  last_processed_index: number
  has_running_background_agents?: boolean
  is_synthetic_turn?: boolean
  can_steer?: boolean
  runtime_state?: RuntimeSnapshot
}

export interface SSEStatusMessage {
  session_status?: string
  display_status?: 'busy' | 'idle' | 'stopped'
  has_running_background_agents?: boolean
  is_synthetic_turn?: boolean
  can_steer?: boolean
  runtime_state?: RuntimeSnapshot
}

// ---- persisted chat history (what /api/chat-history/sessions/{id} returns) ----

export interface ChatHistoryMessagePart {
  Text?: string
  text?: string
  Type?: string
  type?: string
  Content?: string
  content?: string
}

export interface ChatHistoryMessage {
  Role?: string
  role?: string
  Parts?: ChatHistoryMessagePart[]
  parts?: ChatHistoryMessagePart[]
  /** Stable original position supplied only by the bounded formatted-resume projection. */
  resume_order?: number
  resume_source_message_count?: number
}

/** The part of a persisted conversation the restore converter needs; AgentWorks' fuller ChatHistoryConversation satisfies it. */
export interface RestorableConversation {
  session_id: string
  conversation_history: ChatHistoryMessage[]
  ui_events?: PollingEventSchema[]
  history_pagination?: { has_more: boolean; next_offset: number; start_turn: number; total_turns: number }
  history_source_message_count?: number
}
