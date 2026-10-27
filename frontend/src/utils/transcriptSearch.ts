import type { TranscriptItem } from './terminalEventTranscript'
import { isInternalTranscriptMessage, pairToolCalls, turnFailureText } from './terminalEventTranscript'
import { failureHints, normalizeProductChatFailure } from '../platform/chat/productChatFailure'

export type TranscriptSearchRow = { key: string; text: string }
export type TranscriptSearchMatch = { key: string; before: string; match: string; after: string }

// Index only the transcript's already-selected rows, not raw event envelopes,
// child sessions, hidden prompts or bookkeeping metadata.
export function transcriptSearchText(item: TranscriptItem): string {
  if (item.kind === 'thinking') return item.text
  if (item.kind === 'tools') return pairToolCalls(item.events).map(pair => [pair.name, pair.args, pair.result].filter(Boolean).join('\n')).join('\n')
  if (isInternalTranscriptMessage(item.event)) return ''
  const outer = item.event.data as Record<string, unknown> | undefined
  const payload = outer?.data && typeof outer.data === 'object' ? outer.data as Record<string, unknown> : outer || {}
  const failure = turnFailureText(item.event)
  if (failure) {
    const display = normalizeProductChatFailure(failure, failureHints(payload))
    return `${display.message}\n${display.technicalDetails || ''}`
  }
  const metadata = payload.metadata as Record<string, unknown> | undefined
  if (typeof metadata?.display_content === 'string') return metadata.display_content
  return ['content', 'final_result', 'result', 'message', 'text'].map(key => typeof payload[key] === 'string' ? payload[key] : '').filter(Boolean).join('\n')
}

// One result per message/step group. Match snippets also make a result inside
// a long folded tool response readable without searching mounted DOM nodes.
export function findTranscriptMatches(rows: TranscriptSearchRow[], query: string): TranscriptSearchMatch[] {
  const needle = query.trim().toLocaleLowerCase()
  if (!needle) return []
  return rows.flatMap(row => {
    const at = row.text.toLocaleLowerCase().indexOf(needle)
    if (at < 0) return []
    return [{ key: row.key, before: row.text.slice(Math.max(0, at - 60), at), match: row.text.slice(at, at + needle.length), after: row.text.slice(at + needle.length, at + needle.length + 100) }]
  })
}

export const OPEN_CHAT_FIND = 'open-current-chat-find'

/** Only the mounted main chat opts in. Inspection and hidden chats do not. */
export function currentChatFindRoot(): HTMLElement | undefined {
  return Array.from(document.querySelectorAll<HTMLElement>('[data-chat-find-root]')).find(root => root.getClientRects().length > 0)
}

export function openCurrentChatFind() {
  currentChatFindRoot()?.dispatchEvent(new Event(OPEN_CHAT_FIND))
}
