import type { PollingEvent } from '../services/api-types'

export type CodingAgentChoiceAnswer = { id: string; selectedLabels: string[]; otherText?: string }

export type CodingAgentQuestionPrompt = {
  provider: string
  promptId: string
  state: 'pending' | 'answered' | 'interrupted'
  questions: Array<{ id: string; header: string; question: string; multiSelect: boolean; allowOther?: boolean; minSelections?: number; maxSelections?: number; options: Array<{ label: string; description: string }> }>
  answers: CodingAgentChoiceAnswer[]
}

export type CodingAgentQuestionAnswerHandler = (provider: string, promptId: string, answers: CodingAgentChoiceAnswer[], auto?: boolean) => Promise<void>

const record = (value: unknown): Record<string, unknown> => value && typeof value === 'object' ? value as Record<string, unknown> : {}
const text = (value: unknown) => typeof value === 'string' ? value.trim() : ''

// Both chat renderers fold requested/settled events into the original card.
// Correlation uses the provider AND prompt ID; question IDs can recur later.
export function codingAgentQuestionCards(events: PollingEvent[]) {
  const cards = new Map<string, CodingAgentQuestionPrompt>()
  const pending = new Map<string, CodingAgentQuestionPrompt>()
  const hiddenEvents = new Set<string>()
  for (const event of events) {
    if (event.type !== 'coding_agent_question') continue
    const outer = record(event.data)
    const payload = record(outer.data || outer)
    const provider = text(payload.provider)
    const promptId = text(payload.prompt_id)
    if (!promptId) continue
    const key = `${provider}:${promptId}`
    if (payload.kind === 'requested' || payload.kind === 'user_input_prompt_requested') {
      if (pending.has(key)) { hiddenEvents.add(event.id); continue }
      const questions = (Array.isArray(payload.questions) ? payload.questions : []).map(value => {
        const q = record(value)
        return {
          id: text(q.id), header: text(q.header), question: text(q.question), multiSelect: q.multi_select === true || q.multiSelect === true,
          allowOther: q.allow_other === true,
          minSelections: typeof q.min_selections === 'number' ? q.min_selections : undefined,
          maxSelections: typeof q.max_selections === 'number' ? q.max_selections : undefined,
          options: (Array.isArray(q.options) ? q.options : []).map(option => {
            const o = record(option)
            return { label: text(o.label), description: text(o.description) }
          }).filter(option => option.label),
        }
      }).filter(q => q.id && q.question && q.options.length)
      if (!questions.length) continue
      const prompt: CodingAgentQuestionPrompt = { provider, promptId, state: 'pending', questions, answers: [] }
      cards.set(event.id, prompt)
      pending.set(key, prompt)
    } else if (payload.kind === 'settled' || payload.kind === 'user_input_prompt_settled') {
      const prompt = pending.get(key)
      if (!prompt) continue // A history page may contain only the settlement.
      prompt.state = payload.outcome === 'answered' ? 'answered' : 'interrupted'
      prompt.answers = (Array.isArray(payload.answers) ? payload.answers : []).map(value => {
        const answer = record(value)
        return { id: text(answer.id), otherText: text(answer.other_text) || undefined, selectedLabels: Array.isArray(answer.selected_labels)
          ? answer.selected_labels.filter((label): label is string => typeof label === 'string')
          : [text(answer.selected_label)].filter(Boolean) }
      })
      hiddenEvents.add(event.id)
      pending.delete(key)
    }
  }
  return { cards, hiddenEvents }
}
