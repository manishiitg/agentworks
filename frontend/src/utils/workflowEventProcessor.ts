
/**
 * Workflow Event Processor Utility
 *
 * Small predicates over polled events (completion/error detection,
 * retention). The former extractWorkflowInfo batch/step aggregator was
 * removed on 2026-09-22 with the unemitted batch event family.
 */

import type { PollingEvent } from '../services/api-types'
import { EVENT_TYPES } from '../constants/runningWorkflows'

/**
 * Check if events contain workflow completion events.
 *
 * Note: Only workflow-level end events indicate true workflow completion.
 * agent_end and conversation_end are NOT completion events as workflows have multiple
 * agent calls and each agent has its own conversation.
 *
 * @param events - Array of events to check
 * @returns True if events contain workflow completion
 */
export function hasWorkflowCompletion(events: PollingEvent[]): boolean {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  return events.some(e => e.type && EVENT_TYPES.COMPLETION.includes(e.type as any))
}

/**
 * Check if events contain workflow error events.
 *
 * Note: Only workflow_error indicates workflow failure (orchestrator_error was removed: never emitted).
 * agent_error and conversation_error are NOT treated as workflow failures as the
 * orchestrator handles these and may retry or continue execution.
 *
 * @param events - Array of events to check
 * @returns True if events contain workflow errors
 */
export function hasWorkflowError(events: PollingEvent[]): boolean {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  return events.some(e => e.type && EVENT_TYPES.ERROR.includes(e.type as any))
}

/**
 * Check if an event should be retained during cleanup.
 *
 * Important events include completion, error, human feedback, and progress events
 * that are critical for understanding workflow state.
 *
 * @param event - Event to check
 * @returns True if event should be retained
 */
export function shouldRetainEvent(event: PollingEvent): boolean {
  if (!event.type) return false
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  return EVENT_TYPES.IMPORTANT.includes(event.type as any)
}
