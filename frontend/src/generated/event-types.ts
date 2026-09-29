/**
 * Event Type System - Discriminated Union Types
 * 
 * This file provides type-safe discriminated union types for the event system.
 * It builds on top of the generated events-bridge.ts and provides proper
 * type narrowing based on event.data.type.
 * 
 * USAGE:
 * ```typescript
 * import { getEventData, isEventType } from './event-types';
 * 
 * // Type-safe access with type guard
 * if (isEventType(event, 'tool_call_start')) {
 *   const data = getEventData(event);
 *   console.log(data.tool_name);  // Fully typed!
 * }
 * ```
 */

// NOTE: We do NOT use `export * from './events-bridge'` because the generated
// EventDataUnion / AgentEventForSchema types are misleading — the Go backend
// serializes event.data.data flat, not wrapped in EventDataUnion.
// Instead we selectively re-export individual event types (see bottom of file)
// and define corrected wrapper types below.

import type {
  // Event wrapper types (aliased — we override these below)
  PollingEvent as _RawPollingEventSchema,
  AgentEventForSchema as _RawAgentEventForSchema,
  
  // Individual event types
  AgentStartEvent,
  AgentEndEvent,
  AgentErrorEvent,
  ConversationStartEvent,
  ConversationEndEvent,
  ConversationErrorEvent,
  ConversationTurnEvent,
  LLMGenerationStartEvent,
  LLMGenerationEndEvent,
  LLMGenerationErrorEvent,
  LLMGenerationWithRetryEvent,
  ToolCallStartEvent,
  ToolCallEndEvent,
  ToolCallErrorEvent,
  ToolExecutionEvent,
  MCPServerSelectionEvent,
  UserMessageEvent,
  TokenUsageEvent,
  MaxTurnsReachedEvent,
  ContextCancelledEvent,
  LargeToolOutputDetectedEvent,
  LargeToolOutputFileWrittenEvent,
  LargeToolOutputFileWriteErrorEvent,
  RetryAttemptEvent,
  UnifiedCompletionEvent,
  OrchestratorEndEvent,
  OrchestratorAgentStartEvent,
  OrchestratorAgentEndEvent,
  OrchestratorAgentErrorEvent,
  StepTokenUsageEvent,
  RoutingEvaluatedEvent,
  ScriptedExecutionEvent,
  VariablesExtractedEvent,
  RequestHumanFeedbackEvent,
  BlockingHumanFeedbackEvent,
  // New Streaming Events
  StreamingStartEvent,
  StreamingChunkEvent,
  StreamingEndEvent,
  // New MCP Server Connection Events
  MCPServerConnectionStartEvent,
  MCPServerConnectionEndEvent,
  // New JSON Validation Events
  // New Other Events
  ConversationThinkingEvent,
  // Background Agent Events
  BackgroundAgentStartedEvent,
  BackgroundAgentCompletedEvent,
  BackgroundAgentTerminatedEvent,
  CodingAgentBackgroundTaskEvent,
  CodingAgentQuestionEvent,
  SyntheticTurnReadyEvent,
  AutoNotificationSteeredEvent,
  // Presentation Events
  PresentationUpdatedEvent,
  ProductInteractionEvent,
} from './events-bridge';

// =============================================================================
// CORRECTED WRAPPER TYPES
// =============================================================================
// The generated EventDataUnion wraps typed data in named fields (e.g. { token_usage?: ... })
// but the Go backend serializes event.data.data FLAT — it IS the typed event directly.
// These corrected types make data opaque, forcing use of isEventType()/getEventData().

export interface AgentEventForSchema extends Omit<_RawAgentEventForSchema, 'data'> {
  data?: unknown;
}

export interface PollingEventSchema extends Omit<_RawPollingEventSchema, 'data'> {
  data?: AgentEventForSchema;
}

export interface BatchExecutionCanceledEvent {
  timestamp?: string;
  trace_id?: string;
  span_id?: string;
  event_id?: string;
  parent_id?: string;
  is_end_event?: boolean;
  correlation_id?: string;
  hierarchy_level?: number;
  session_id?: string;
  component?: string;
  metadata?: {
    [k: string]: unknown;
  };
  total_groups?: number;
  completed_groups?: number;
  canceled_group_name?: string;
  remaining_group_names?: string[];
  reason?: string;
}

// Import event types that exist in events.ts but not in events-bridge.ts
import type {
  PreValidationCompletedEvent,
} from './events';

// =============================================================================
// EVENT TYPE CONSTANTS
// =============================================================================

/**
 * All valid event type string literals
 */
export type EventTypeString =
  | 'agent_start'
  | 'agent_end'
  | 'agent_error'
  | 'conversation_start'
  | 'conversation_end'
  | 'conversation_error'
  | 'conversation_turn'
  | 'llm_generation_start'
  | 'llm_generation_end'
  | 'llm_generation_error'
  | 'llm_generation_with_retry'
  | 'tool_call_start'
  | 'tool_call_end'
  | 'tool_call_error'
  | 'tool_execution'
  | 'mcp_server_selection'
  | 'user_message'
  | 'token_usage'
  | 'max_turns_reached'
  | 'context_cancelled'
  | 'large_tool_output_detected'
  | 'large_tool_output_file_written'
  | 'large_tool_output_file_write_error'
  | 'retry_attempt'
  | 'broken_pipe'
  | 'unified_completion'
  | 'orchestrator_end'
  | 'orchestrator_agent_start'
  | 'orchestrator_agent_end'
  | 'orchestrator_agent_error'
  | 'step_token_usage'
  | 'routing_evaluated'
  | 'pre_validation_completed'
  | 'learn_code_script_execution'
  | 'variables_extracted'
  | 'request_human_feedback'
  | 'blocking_human_feedback'
  // Streaming Events
  | 'streaming_start'
  | 'streaming_chunk'
  | 'streaming_end'
  // MCP Server Connection Detail Events
  | 'mcp_server_connection_start'
  | 'mcp_server_connection_end'
  // JSON Validation Events
  // Other Events
  | 'conversation_thinking'
  // Workflow Events
  // Batch Execution Events (only cancellation is emitted)
  | 'batch_execution_canceled'
  // Todo Task Events
  | 'todo_task_route_selected'
  | 'todo_task_step_completed'
  // Delegation Events
  | 'delegation_start'
  | 'delegation_end'
  // Background Agent Events
  | 'background_agent_started'
  | 'background_agent_completed'
  | 'background_agent_terminated'
  | 'coding_agent_background_task'
  | 'coding_agent_question'
  | 'synthetic_turn_ready'
  | 'auto_notification_steered'
  // Presentation Events
  | 'presentation_updated'
  // Product surface interactions (suggestions, celebrate, scene, ...)
  | 'product_interaction';

// =============================================================================
// EVENT TYPE TO DATA TYPE MAPPING
// =============================================================================

/**
 * Maps event type strings to their corresponding typed event data
 */
export interface EventTypeToDataMap {
  'agent_start': AgentStartEvent;
  'agent_end': AgentEndEvent;
  'agent_error': AgentErrorEvent;
  'conversation_start': ConversationStartEvent;
  'conversation_end': ConversationEndEvent;
  'conversation_error': ConversationErrorEvent;
  'conversation_turn': ConversationTurnEvent;
  'llm_generation_start': LLMGenerationStartEvent;
  'llm_generation_end': LLMGenerationEndEvent;
  'llm_generation_error': LLMGenerationErrorEvent;
  'llm_generation_with_retry': LLMGenerationWithRetryEvent;
  'tool_call_start': ToolCallStartEvent;
  'tool_call_end': ToolCallEndEvent;
  'tool_call_error': ToolCallErrorEvent;
  'tool_execution': ToolExecutionEvent;
  'mcp_server_selection': MCPServerSelectionEvent;
  'user_message': UserMessageEvent;
  'token_usage': TokenUsageEvent;
  'max_turns_reached': MaxTurnsReachedEvent;
  'context_cancelled': ContextCancelledEvent;
  'large_tool_output_detected': LargeToolOutputDetectedEvent;
  'large_tool_output_file_written': LargeToolOutputFileWrittenEvent;
  'large_tool_output_file_write_error': LargeToolOutputFileWriteErrorEvent;
  'retry_attempt': RetryAttemptEvent;
  'unified_completion': UnifiedCompletionEvent;
  'orchestrator_end': OrchestratorEndEvent;
  'orchestrator_agent_start': OrchestratorAgentStartEvent;
  'orchestrator_agent_end': OrchestratorAgentEndEvent;
  'orchestrator_agent_error': OrchestratorAgentErrorEvent;
  'step_token_usage': StepTokenUsageEvent;
  'routing_evaluated': RoutingEvaluatedEvent;
  'pre_validation_completed': PreValidationCompletedEvent;
  'learn_code_script_execution': ScriptedExecutionEvent;
  'variables_extracted': VariablesExtractedEvent;
  'request_human_feedback': RequestHumanFeedbackEvent;
  'blocking_human_feedback': BlockingHumanFeedbackEvent;
  // Streaming Events
  'streaming_start': StreamingStartEvent;
  'streaming_chunk': StreamingChunkEvent;
  'streaming_end': StreamingEndEvent;
  // MCP Server Connection Detail Events
  'mcp_server_connection_start': MCPServerConnectionStartEvent;
  'mcp_server_connection_end': MCPServerConnectionEndEvent;
  // JSON Validation Events
  // Other Events
  'conversation_thinking': ConversationThinkingEvent;
  // Workflow Events
  // Batch Execution Events (only cancellation is emitted)
  'batch_execution_canceled': BatchExecutionCanceledEvent;
  // Todo Task Events
  'todo_task_route_selected': TodoTaskRouteSelectedEvent;
  'todo_task_step_completed': TodoTaskStepCompletedEvent;
  // Delegation Events
  'delegation_start': DelegationStartEvent;
  'delegation_end': DelegationEndEvent;
  // Broken Pipe Events
  'broken_pipe': BrokenPipeEvent;
  // Background Agent Events
  'background_agent_started': BackgroundAgentStartedEvent;
  'background_agent_completed': BackgroundAgentCompletedEvent;
  'background_agent_terminated': BackgroundAgentTerminatedEvent;
  'coding_agent_background_task': CodingAgentBackgroundTaskEvent;
  'coding_agent_question': CodingAgentQuestionEvent;
  'synthetic_turn_ready': SyntheticTurnReadyEvent;
  'auto_notification_steered': AutoNotificationSteeredEvent;
  // Presentation Events
  'presentation_updated': PresentationUpdatedEvent;
  'product_interaction': ProductInteractionEvent;
}

// Todo Task event data types (not in generated schema)
export interface TodoTaskRouteSelectedEvent {
  timestamp?: string;
  trace_id?: string;
  span_id?: string;
  event_id?: string;
  parent_id?: string;
  is_end_event?: boolean;
  correlation_id?: string;
  hierarchy_level?: number;
  session_id?: string;
  component?: string;
  metadata?: {
    [k: string]: unknown;
  };
  step_index?: number;
  step_path?: string;
  step_id?: string;
  step_title?: string;
  iteration?: number;
  next_action?: string; // "delegate", "complete", "continue"
  selected_route_id?: string;
  selected_route_name?: string;
  use_generic_agent?: boolean;
  todo_id_to_execute?: string;
  todo_title?: string;
  instructions_to_sub_agent?: string;
  selection_reasoning?: string;
  all_tasks_complete?: boolean;
  completion_reason?: string;
  progress_summary?: string;
  model?: string;
  preferred_tier?: number; // 1=High, 2=Medium, 3=Low
  preferred_tier_label?: string; // "High", "Medium", "Low"
}

export interface TodoTaskStepCompletedEvent {
  timestamp?: string;
  trace_id?: string;
  span_id?: string;
  event_id?: string;
  parent_id?: string;
  is_end_event?: boolean;
  correlation_id?: string;
  hierarchy_level?: number;
  session_id?: string;
  component?: string;
  metadata?: {
    [k: string]: unknown;
  };
  step_index?: number;
  step_path?: string;
  step_id?: string;
  step_title?: string;
  total_iterations?: number;
  total_todos_count?: number;
  completed_count?: number;
  completion_reason?: string;
  next_step_id?: string;
}

// Delegation event data types (not in generated schema)
export interface BrokenPipeEvent {
  timestamp?: string;
  operation?: string; // "broken_pipe_detected", "retry_success", "retry_failure"
  tool_name?: string;
  server_name?: string;
  tool_call_id?: string;
  error?: string;
  duration?: string;
}

export interface DelegationStartEvent {
  timestamp?: string;
  trace_id?: string;
  span_id?: string;
  event_id?: string;
  parent_id?: string;
  is_end_event?: boolean;
  correlation_id?: string;
  hierarchy_level?: number;
  session_id?: string;
  component?: string;
  metadata?: {
    [k: string]: unknown;
  };
  delegation_id?: string;
  depth?: number;
  instruction?: string;
}

export interface DelegationEndEvent {
  timestamp?: string;
  trace_id?: string;
  span_id?: string;
  event_id?: string;
  parent_id?: string;
  is_end_event?: boolean;
  correlation_id?: string;
  hierarchy_level?: number;
  session_id?: string;
  component?: string;
  metadata?: {
    [k: string]: unknown;
  };
  delegation_id?: string;
  depth?: number;
  result?: string;
  error?: string;
  duration?: string;
}

// Background agent event data types (BackgroundAgentStartedEvent,
// BackgroundAgentCompletedEvent, BackgroundAgentTerminatedEvent,
// SyntheticTurnReadyEvent, AutoNotificationSteeredEvent) now come from the
// generated schema (see the events-bridge import above) — they used to be
// hand-written stubs here, drafted but never wired into EventTypeString /
// EventTypeToDataMap, so every consumer fell back to ad-hoc `as` casts
// regardless. The backend now emits real typed structs for these, so the
// generated interfaces are authoritative.

// =============================================================================
// TYPED EVENT INTERFACE
// =============================================================================

/**
 * A typed polling event where we know the event type
 */
export interface TypedEvent<T extends EventTypeString> {
  id: string;
  type: T;
  timestamp?: string;
  session_id?: string;
  error?: string;
  data: {
    type: T;
    timestamp?: string;
    event_index?: number;
    trace_id?: string;
    span_id?: string;
    parent_id?: string;
    correlation_id?: string;
    hierarchy_level?: number;
    session_id?: string;
    component?: string;
    data: EventTypeToDataMap[T];
  };
}

// =============================================================================
// TYPE GUARDS - The core of type-safe event handling
// =============================================================================

/**
 * Type guard to check if an event is of a specific type.
 * After this check, you can use getEventData() to get typed data.
 * 
 * @example
 * if (isEventType(event, 'tool_call_start')) {
 *   const data = getEventData(event);
 *   console.log(data.tool_name);  // TypeScript knows this exists!
 * }
 */
export function isEventType<T extends EventTypeString>(
  event: PollingEventSchema | undefined | null,
  eventType: T
): event is PollingEventSchema & { type: T; data: AgentEventForSchema & { type: T; data: EventTypeToDataMap[T] } } {
  const agentEvent = event?.data as AgentEventForSchema | undefined;
  return event?.type === eventType && agentEvent?.type === eventType;
}

/**
 * Get the typed event data from a typed event.
 * Use this AFTER checking with isEventType()
 * 
 * @example
 * if (isEventType(event, 'agent_start')) {
 *   const data = getEventData(event);  // Returns AgentStartEvent
 *   console.log(data.agent_type);
 * }
 */
export function getEventData<T extends EventTypeString>(
  event: PollingEventSchema & { type: T; data: { type: T; data: EventTypeToDataMap[T] } }
): EventTypeToDataMap[T] {
  return event.data.data as EventTypeToDataMap[T];
}

/**
 * Combined type guard and data extraction.
 * Returns undefined if event doesn't match, otherwise returns typed data.
 * 
 * @example
 * const data = getTypedEventData(event, 'tool_call_start');
 * if (data) {
 *   console.log(data.tool_name);  // TypeScript knows the type!
 * }
 */
export function getTypedEventData<T extends EventTypeString>(
  event: PollingEventSchema | undefined | null,
  eventType: T
): EventTypeToDataMap[T] | undefined {
  if (isEventType(event, eventType)) {
    return getEventData(event);
  }
  return undefined;
}

// =============================================================================
// HELPER FUNCTIONS
// =============================================================================

/**
 * Get the raw inner data from an event (event.data.data)
 * Returns unknown type - use type guards for type safety
 */
export function getRawEventData(
  event: PollingEventSchema | undefined | null
): unknown {
  if (!event?.data) return undefined;
  const agentEvent = event.data as AgentEventForSchema;
  return agentEvent.data;
}

/**
 * Check if event has inner data
 */
export function hasEventData(
  event: PollingEventSchema | undefined | null
): boolean {
  if (!event?.data) return false;
  const agentEvent = event.data as AgentEventForSchema;
  return agentEvent.data !== undefined;
}

/**
 * Get the event type from a polling event
 */
export function getEventType(
  event: PollingEventSchema | undefined | null
): EventTypeString | undefined {
  return event?.type as EventTypeString | undefined;
}

/**
 * Assert that an event is of a specific type.
 * Throws if the assertion fails. Use when you're certain of the type.
 * 
 * @example
 * // In a switch case where you know the type
 * case 'tool_call_start':
 *   return <ToolCallDisplay data={assertEventType(event, 'tool_call_start')} />
 */
export function assertEventType<T extends EventTypeString>(
  event: PollingEventSchema | undefined | null,
  eventType: T
): EventTypeToDataMap[T] {
  if (!isEventType(event, eventType)) {
    throw new Error(`Expected event type '${eventType}' but got '${event?.type}'`);
  }
  return getEventData(event);
}

/**
 * Safe assertion - returns the typed data or a fallback value
 * Use this in render functions where you can't throw
 */
export function getEventDataOrDefault<T extends EventTypeString>(
  event: PollingEventSchema | undefined | null,
  eventType: T,
  defaultValue: EventTypeToDataMap[T]
): EventTypeToDataMap[T] {
  if (isEventType(event, eventType)) {
    return getEventData(event);
  }
  return defaultValue;
}

// =============================================================================
// RE-EXPORTS FOR CONVENIENCE
// =============================================================================

// Export the individual event types for direct use
export type {
  AgentStartEvent,
  AgentEndEvent,
  AgentErrorEvent,
  ConversationStartEvent,
  ConversationEndEvent,
  ConversationErrorEvent,
  ConversationTurnEvent,
  LLMGenerationStartEvent,
  LLMGenerationEndEvent,
  LLMGenerationErrorEvent,
  LLMGenerationWithRetryEvent,
  ToolCallStartEvent,
  ToolCallEndEvent,
  ToolCallErrorEvent,
  ToolExecutionEvent,
  MCPServerSelectionEvent,
  UserMessageEvent,
  TokenUsageEvent,
  MaxTurnsReachedEvent,
  ContextCancelledEvent,
  LargeToolOutputDetectedEvent,
  LargeToolOutputFileWrittenEvent,
  LargeToolOutputFileWriteErrorEvent,
  RetryAttemptEvent,
  UnifiedCompletionEvent,
  OrchestratorEndEvent,
  OrchestratorAgentStartEvent,
  OrchestratorAgentEndEvent,
  OrchestratorAgentErrorEvent,
  StepTokenUsageEvent,
  RoutingEvaluatedEvent,
  ScriptedExecutionEvent,
  VariablesExtractedEvent,
  RequestHumanFeedbackEvent,
  BlockingHumanFeedbackEvent,
  // Streaming Events
  StreamingStartEvent,
  StreamingChunkEvent,
  StreamingEndEvent,
  // MCP Server Connection Detail Events
  MCPServerConnectionStartEvent,
  MCPServerConnectionEndEvent,
  // JSON Validation Events
  // Other Events
  ConversationThinkingEvent,
  // Background Agent Events
  BackgroundAgentStartedEvent,
  BackgroundAgentCompletedEvent,
  BackgroundAgentTerminatedEvent,
  SyntheticTurnReadyEvent,
  AutoNotificationSteeredEvent,
  // Presentation Events
  PresentationUpdatedEvent,
  ProductInteractionEvent,
} from './events-bridge';

// Export nested types from events.ts (used by event types but not in events-bridge.ts)
export type {
  PreValidationCompletedEvent,
  FileCheckResultForEvent,
  JSONCheckResultForEvent,
  ValidationErrorForEvent,
  TodoStep,
} from './events';
