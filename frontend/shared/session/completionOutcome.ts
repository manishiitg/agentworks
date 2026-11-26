/** Display the server's explicit no-answer outcome; an empty reply alone is not an interruption. */
export function interruptedCompletionMessage(metadata: unknown): string {
  if (!metadata || typeof metadata !== 'object') return ''
  const fields = metadata as Record<string, unknown>
  if (fields.stopped === true) return 'Stopped'
  return fields.retained_turn_outcome === 'interrupted' && fields.final_response_missing === true
    ? 'This reply was interrupted and no answer was produced. Send your message again.'
    : ''
}

/** Server-owned partial text is not a completed answer. */
export function stoppedCompletionText(metadata: unknown): string {
  if (!metadata || typeof metadata !== 'object') return ''
  const fields = metadata as Record<string, unknown>
  return fields.stopped === true && typeof fields.partial_response === 'string' ? fields.partial_response : ''
}
