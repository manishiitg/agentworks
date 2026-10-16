const TRANSPORT_CONTEXT_MARKERS = [
  '\n\n📁 Files in context:',
  '\n\nPrevious workflow-builder conversation file:',
]

const PULSE_TURN_MARKER = 'PULSE TURN:'
const PULSE_TURN_LABELS: Array<[string, string]> = [
  ['PULSE TURN: daily goal check', 'Pulse: daily goal check'],
  ['PULSE TURN: Goal Work', 'Pulse: Goal Work'],
  ['PULSE TURN: a run of this workflow failed', 'Pulse: a run failed'],
]

/** Pulse's automatic turns (goal check, Goal Work, a failed run) carry their
 * task text; show a short label instead. Messages between people and agents
 * are plain and shown as they are. Returns null when it is not such a turn. */
export function pulseTurnDisplayText(content: string): string | null {
  const turnAt = content.indexOf(PULSE_TURN_MARKER)
  if (turnAt < 0) return null
  const turn = content.slice(turnAt)
  for (const [prefix, label] of PULSE_TURN_LABELS) {
    if (turn.startsWith(prefix)) return label
  }
  return 'Pulse turn'
}

const FILES_IN_CONTEXT_MARKER = '\n\n📁 Files in context:'
const IMAGE_FILE = /\.(png|jpe?g|gif|webp|bmp|avif)$/i

/** The files a person attached to a message, read from the request context stored with it (the same on screen now and
 * after a reload). Request-only context such as the restored-conversation file is not an attachment. */
export function getUserMessageAttachmentPaths(content: string): string[] {
  const at = content.indexOf(FILES_IN_CONTEXT_MARKER)
  if (at < 0) return []
  const line = content.slice(at + FILES_IN_CONTEXT_MARKER.length).split('\n')[0] ?? ''
  const seen = new Set<string>()
  const paths: string[] = []
  for (const part of line.split(',')) {
    const path = part.trim()
    if (path && !seen.has(path)) { seen.add(path); paths.push(path) }
  }
  return paths
}

export function isImageAttachmentPath(path: string): boolean {
  return IMAGE_FILE.test(path)
}

/** Remove request-only context that was not typed by the user. */
export function getDisplaySafeUserMessageContent(content: string): string {
  const pulse = pulseTurnDisplayText(content)
  if (pulse !== null) return pulse
  const markerIndexes = TRANSPORT_CONTEXT_MARKERS
    .map(marker => content.indexOf(marker))
    .filter(index => index >= 0)
  const end = markerIndexes.length > 0 ? Math.min(...markerIndexes) : content.length
  return content.slice(0, end).trim()
}
