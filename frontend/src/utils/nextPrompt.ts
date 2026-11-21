import type { PollingEvent } from '../services/api-types'

export function nextPromptFields(event: PollingEvent): { text: string; for_message_id: string } | undefined {
  if (event.type !== 'next_prompt') return
  const outer = event.data as { data?: unknown } | undefined
  const data = (outer?.data || outer) as Record<string, unknown> | undefined
  if (typeof data?.text !== 'string' || !data.text.trim() || typeof data.for_message_id !== 'string') return
  return { text: data.text, for_message_id: data.for_message_id }
}

// Return the existing object so Zustand does not rerender the composer for
// every token. Stop at a newer input; prediction text never classifies a row.
export function currentNextPrompt(events: PollingEvent[] | undefined): PollingEvent | undefined {
  if (!events) return
  for (let i = events.length - 1; i >= 0; i--) {
    const event = events[i]
    if (event.type === 'user_message') return
    const candidate = nextPromptFields(event)
    if (!candidate) continue
    for (let j = i - 1; j >= 0; j--) {
      if (events[j].type === 'user_message') return events[j].id === candidate.for_message_id ? event : undefined
    }
    return
  }
}

export type NextPromptKey = Pick<KeyboardEvent, 'key' | 'ctrlKey' | 'altKey' | 'metaKey' | 'shiftKey' | 'isComposing'>

// Deliberately no send/queue callback. Acceptance always clears autoRun so an
// existing one-click-composer instruction cannot turn Tab into a submission.
export function applyNextPromptKey(event: NextPromptKey, text: string, fillDraft: (text: string, patch: { autoRun: false }) => void, dismiss: () => void): boolean {
  if (!text || event.isComposing || event.ctrlKey || event.altKey || event.metaKey || event.shiftKey) return false
  if (event.key !== 'Tab' && event.key !== 'Escape') return false
  if (event.key === 'Tab') fillDraft(text, { autoRun: false })
  dismiss()
  return true
}
