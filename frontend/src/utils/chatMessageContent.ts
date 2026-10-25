const TRANSPORT_CONTEXT_MARKERS = [
  '\n\n📁 Files in context:',
  '\n\nPrevious workflow-builder conversation file:',
]



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
  const markerIndexes = TRANSPORT_CONTEXT_MARKERS
    .map(marker => content.indexOf(marker))
    .filter(index => index >= 0)
  const end = markerIndexes.length > 0 ? Math.min(...markerIndexes) : content.length
  return content.slice(0, end).trim()
}
