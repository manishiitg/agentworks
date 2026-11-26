const TRANSPORT_CONTEXT_MARKERS = [
  '📁 Files in context:',
  'Previous workflow-builder conversation file:',
  // The chat's reply style and summary-card note (chat_reply_style.go): for the agent, not the person.
  '⚙️ Chat settings:',
]



const FILES_IN_CONTEXT_MARKER = '📁 Files in context:'
// A caption-less request starts with its file context after transport trimming.
// Keep inline mentions in ordinary prose visible; only a start/block is context.
function transportContextAt(content: string, marker: string): number {
  if (content.trimStart().startsWith(marker)) return content.indexOf(marker)
  const block = content.indexOf(`\n\n${marker}`)
  return block < 0 ? -1 : block + 2
}

const IMAGE_FILE = /\.(png|jpe?g|gif|webp|bmp|avif)$/i

/** The files a person attached to a message, read from the request context stored with it (the same on screen now and
 * after a reload). Request-only context such as the restored-conversation file is not an attachment. */
export function getUserMessageAttachmentPaths(content: string): string[] {
  const at = transportContextAt(content, FILES_IN_CONTEXT_MARKER)
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

/** The name a person gave an attached file: uploads are stored as `<id>-<name>`, and the id is not shown. */
export function attachmentDisplayName(path: string): string {
  const base = path.split('/').pop() || path
  return base.replace(/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}-(?=.)/i, '')
}

export function isImageAttachmentPath(path: string): boolean {
  return IMAGE_FILE.test(path)
}

/** Remove request-only context that was not typed by the user. */
// Forks made before 24 Nov saved their note without the closing bracket when the parent had no purpose.
const FIRST_TURN_NOTE = /^\[This chat is [\s\S]*?(?:\]|send only a hand-off, a blocker or a result\.)\n\n/

export function getDisplaySafeUserMessageContent(content: string): string {
  const markerIndexes = TRANSPORT_CONTEXT_MARKERS
    .map(marker => transportContextAt(content, marker))
    .filter(index => index >= 0)
  const end = markerIndexes.length > 0 ? Math.min(...markerIndexes) : content.length
  // A fork's, shared copy's or helper's one-time first-turn note ("[This chat is …]") is for the agent, not the person.
  const text = content.slice(0, end).trim().replace(FIRST_TURN_NOTE, '')
  // Text after the block is the server's note to the agent, not the person's answer.
  const reply = /^<send_user_message_question_reply>\s*([\s\S]*?)\s*<\/send_user_message_question_reply>/.exec(text)
  if (reply) {
    try {
      const answers: unknown = JSON.parse(reply[1])
      if (Array.isArray(answers) && answers.length && answers.every(item => item && typeof item.answer === 'string')) {
        return answers.map(item => item.answer).join('\n\n')
      }
    } catch { /* Preserve malformed or ordinary user text. */ }
  }
  return text
}
