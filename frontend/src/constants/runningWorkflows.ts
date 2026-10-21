/**
 * Configuration constants for running workflows feature
 *
 * This file centralizes all configuration values used in the running workflows
 * system to make them easy to find, modify, and maintain.
 */

/**
 * Polling intervals in milliseconds
 */
export const POLLING_INTERVALS = {
  /** Fast polling when drawer is open and user is actively viewing */
  ACTIVE: 2000,
  /** Normal polling when drawer is closed but workflows are running */
  BACKGROUND: 5000,
  /** Slow polling when no workflows are actively running */
  IDLE: 10000,
} as const

/**
 * Workflow tracking limits
 */
export const WORKFLOW_LIMITS = {
  /** Maximum number of workflows to track simultaneously */
  MAX_TRACKED_WORKFLOWS: 10,
  /** Time in ms before auto-removing completed/failed workflows (1 hour) */
  COMPLETED_WORKFLOW_TTL: 3600000,
  /** Auto-stop a workflow waiting for input after this duration (30 min) */
  WAITING_INPUT_AUTO_STOP_MS: 30 * 60 * 1000,
} as const

/**
 * Event management configuration
 */
export const EVENT_CONFIG = {
  /** Maximum events to retain per session for completed workflows */
  MAX_EVENTS_PER_COMPLETED_SESSION: 50,
  /** Maximum events to retain per session for active workflows */
  MAX_EVENTS_PER_ACTIVE_SESSION: 200,
  /** Cleanup events when count exceeds this threshold */
  CLEANUP_THRESHOLD: 250,
} as const

/**
 * Validation and caching configuration
 */
export const VALIDATION_CONFIG = {
  /** Cache validation results for this duration (1 minute) */
  VALIDATION_CACHE_TTL: 60000,
  /** Maximum number of consecutive poll failures before marking as error */
  MAX_POLL_RETRIES: 3,
  /** Base delay for exponential backoff in ms */
  RETRY_BASE_DELAY: 1000,
  /** Maximum delay for exponential backoff in ms */
  RETRY_MAX_DELAY: 30000,
} as const

/**
 * LocalStorage keys
 */
export const STORAGE_KEYS = {
  /** Key for persisting running workflows list */
  RUNNING_WORKFLOWS: 'mcp_agent_builder_running_workflows',
  /** Key for validation cache timestamp */
  VALIDATION_TIMESTAMP: 'mcp_agent_builder_validation_timestamp',
} as const

/**
 * Event types for workflow status detection
 */
export const EVENT_TYPES = {
  /**
   * Events that indicate workflow completion.
   *
   * PLAT-063/064: 'workflow_end' and 'batch_execution_end' were removed. Neither
   * is emitted by any Go code — the entire workflow_start/workflow_progress/
   * workflow_end family was a dead parallel to the live orchestrator_* family
   * (BaseOrchestrator.EmitOrchestratorEnd, one caller, workflow_orchestrator.go)
   * and was deleted outright in batch 4. They looked like redundant completion
   * signals; they provided none, and their presence made this array look
   * safer against a single-event failure than it
   * actually is. 'orchestrator_end' is the only real signal.
   */
  COMPLETION: ['orchestrator_end'] as const,
  /** Events that indicate workflow errors (fatal). orchestrator_error was removed: never emitted. */
  ERROR: ['workflow_error'] as const,
  /** Important events that should always be retained during cleanup */
  IMPORTANT: [
    'agent_error',
    'conversation_error',
    'unified_completion',
    'conversation_end',
    'request_human_feedback',
    'blocking_human_feedback',
    'orchestrator_end',
    'agent_end'
  ] as const,
} as const
