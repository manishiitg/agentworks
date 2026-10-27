/** Parent-facing context belongs after the actual activity preview. */
export function ActivityDescription({ description, conversationOnly = false }: { description?: string; conversationOnly?: boolean }) {
  if (!description?.trim()) return null
  // For conversation-only practice this is the activity itself, so keep it visible.
  if (conversationOnly) return (
    <section className="fl-activity-description">
      <h3>How this activity works</h3>
      <p>{description}</p>
    </section>
  )
  return (
    <details className="fl-activity-description">
      <summary>About this activity</summary>
      <p>{description}</p>
    </details>
  )
}
