/** Provider-supplied bold summary heading, including a heading still streaming.
 * This formats existing thinking only; it never classifies reply text. */
export function thinkingSummary(text: string): { title: string; body: string } | null {
  const source = text.trimStart()
  if (!source.startsWith('**')) return null
  const closing = source.indexOf('**', 2)
  const boundary = closing < 0 ? source.indexOf('\n') : closing
  const title = source.slice(2, boundary < 0 ? undefined : boundary).replace(/\*/g, '').trim()
  if (!title) return null
  return { title, body: boundary < 0 ? '' : source.slice(boundary + (closing < 0 ? 1 : 2)).trim() }
}
