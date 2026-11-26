import { z } from 'zod'
import { chatVisualCatalog, chatWidgetSchemas } from './chatElementSpec'
import { CHAT_VISUAL_MAX_LENGTH } from './chatVisualSpec'

const fields = {
  diagram: chatWidgetSchemas.diagram.optional().describe(chatVisualCatalog.data.components.diagram.description!),
  chart: chatWidgetSchemas.chart.optional().describe(chatVisualCatalog.data.components.chart.description!),
  timeline: chatWidgetSchemas.timeline.optional().describe(chatVisualCatalog.data.components.timeline.description!),
  cards: chatWidgetSchemas.cards.optional().describe(chatVisualCatalog.data.components.cards.description!),
  sequence: chatWidgetSchemas.sequence.optional().describe('Specialist visual for ordered calls and replies between participants.'),
  swimlane: chatWidgetSchemas.swimlane.optional().describe('Specialist visual for who does each step, grouped into lanes.'),
}

const requestSchema = z.strictObject(fields).refine(
  value => Object.keys(value).length === 1,
  'Provide exactly one visual: diagram, chart, timeline, cards, sequence or swimlane',
)

export function authorVisualInputSchema() {
  return {
    ...z.toJSONSchema(requestSchema, { target: 'draft-7', io: 'input', unrepresentable: 'any' }),
    minProperties: 1,
    maxProperties: 1,
  }
}

export function authorChatVisual(input: unknown): { markdown: string } | { error: string } {
  const result = requestSchema.safeParse(input)
  if (!result.success) {
    return { error: result.error.issues.slice(0, 8).map(issue =>
      [issue.path.join('.'), issue.message].filter(Boolean).join(': '),
    ).join('\n') }
  }
  const [type, props] = Object.entries(result.data)[0]
  if (!props) return { error: 'Provide the visual fields as an object' }
  const add = (path: string, value: unknown) => JSON.stringify({ op: 'add', path, value })
  const fields = Object.entries(props)
  const initial = Object.fromEntries(fields.map(([key, value]) => [key, Array.isArray(value) ? [] : value]))
  const lines = [add('/root', 'visual'), add('/elements/visual', { type, props: initial, children: [] })]
  // The catalog declares nodes/actors before the lists that refer to them.
  for (const [key, value] of fields) {
    if (Array.isArray(value)) {
      for (const item of value) lines.push(add(`/elements/visual/props/${key}/-`, item))
    }
  }
  const body = lines.join('\n')
  if (body.length > CHAT_VISUAL_MAX_LENGTH) return { error: 'The visual is too long. Shorten the text or use fewer items.' }
  return { markdown: '```chat-ui\n' + body + '\n```' }
}
