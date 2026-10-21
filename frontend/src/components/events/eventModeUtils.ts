// Hidden events - events hidden in chat view
export const HIDDEN_EVENTS = new Set([
  'llm_generation_start',
  'llm_generation_with_retry',
  'conversation_start',
  'conversation_turn',
  'system_prompt',
  'agent_start',
  'llm_generation_end',
  // The server's turn phase feeds the Working line only; it is never a chat row.
  'turn_phase',
]);

// Helper function to check if an event should be shown
export const shouldShowEventByMode = (eventType: string, _mode?: unknown): boolean => {
  if (!eventType) return false

  return !HIDDEN_EVENTS.has(eventType)
}
