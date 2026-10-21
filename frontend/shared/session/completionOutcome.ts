/** Display the server's explicit no-answer outcome; an empty reply alone is not an interruption. */
export function interruptedCompletionMessage(metadata: unknown): string {
  if (!metadata || typeof metadata !== 'object') return ''
  const fields = metadata as Record<string, unknown>
  return fields.retained_turn_outcome === 'interrupted' && fields.final_response_missing === true
    ? 'This reply was interrupted and no answer was produced. Send your message again.'
    : ''
}
