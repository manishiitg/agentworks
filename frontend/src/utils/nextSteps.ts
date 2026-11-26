import type { PollingEvent } from '../services/api-types'

// Summary and next steps (owner, 26 Nov, like Claude Desktop's card), for every coding app. The agent ends a reply
// with a ```next block of one JSON line (chat_reply_style.go, chatNextStepsNote; keep the format in step):
// {"summary": "...", "question": "...", "options": ["...", "..."]}. The reply's text never shows the block; the
// message box shows it as a card with one-click answers.

export type NextSteps = { summary: string; question: string; options: string[] }

// A finished block anywhere at the end of the text, and a block still being written (no closing fence yet).
const NEXT_BLOCK = /\n?```next[ \t]*\n([\s\S]*?)\n?```[ \t]*$/
const OPEN_NEXT_BLOCK = /\n?```next[ \t]*(\n[\s\S]*)?$/

/** The reply's text without its next-steps block (also while the block is still streaming in). */
export function stripNextSteps(text: string): string {
  if (!text || !text.includes('```next')) return text
  const trimmed = text.replace(/\s+$/, '')
  if (NEXT_BLOCK.test(trimmed)) return trimmed.replace(NEXT_BLOCK, '').replace(/\s+$/, '')
  return trimmed.replace(OPEN_NEXT_BLOCK, '').replace(/\s+$/, '')
}

export function parseNextSteps(text: string): NextSteps | undefined {
  const match = NEXT_BLOCK.exec((text || '').replace(/\s+$/, ''))
  if (!match) return
  try {
    const value = JSON.parse(match[1].trim()) as Record<string, unknown>
    const options = Array.isArray(value.options) ? value.options.filter((item): item is string => typeof item === 'string' && item.trim() !== '').map(item => item.trim()).slice(0, 3) : []
    const question = typeof value.question === 'string' ? value.question.trim() : ''
    const summary = typeof value.summary === 'string' ? value.summary.trim() : ''
    if (!question || !options.length) return
    return { summary, question, options }
  } catch {
    return
  }
}

function completionText(event: PollingEvent): string {
  const outer = event.data as Record<string, unknown> | undefined
  const data = ((outer?.data as Record<string, unknown> | undefined) || outer) as Record<string, unknown> | undefined
  for (const key of ['content', 'final_result', 'result']) {
    const value = data?.[key]
    if (typeof value === 'string' && value.trim()) return value
  }
  return ''
}

/** The reply that carries the card: the chat's last reply, only when it finished after the person's last message.
 * Returns the stored event itself so the store does not re-render the message box on every update. */
export function currentNextStepsEvent(events: PollingEvent[] | undefined): PollingEvent | undefined {
  if (!events) return
  for (let i = events.length - 1; i >= 0; i--) {
    const event = events[i]
    if (event.type === 'user_message') return
    if (event.type !== 'unified_completion' && event.type !== 'llm_generation_end') continue
    const text = completionText(event)
    if (!text) continue
    return parseNextSteps(text) ? event : undefined
  }
}

export function nextStepsOf(event: PollingEvent | undefined): NextSteps | undefined {
  return event ? parseNextSteps(completionText(event)) : undefined
}
