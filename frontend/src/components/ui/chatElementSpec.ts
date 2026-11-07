import { z } from 'zod'
import { defineCatalog } from '@json-render/core'
import { schema } from '@json-render/react/schema'

export type ChatCard = { title: string; description?: string; image?: string; href?: string }
export type DiagramNode = { id: string; title: string; description?: string; items?: { title: string; description?: string }[]; emphasis?: boolean }
export type DiagramEdge = { from: string; to: string; label?: string; bidirectional?: boolean }

export type SwimlaneStep = { id: string; lane: string; title: string; description?: string }
export type DiagramParticipant = { id: string; title: string }
export type SequenceMessage = { from: string; to: string; label: string; reply?: boolean }

export type ChatElement =
  | { type: 'chart'; title: string; style: 'line' | 'bar'; unit?: string; data: { label: string; value: number }[] }
  | { type: 'timeline'; title: string; items: { when: string; title: string; detail?: string }[] }
  | { type: 'details'; title: string; body: string }
  | { type: 'cards'; title: string; layout: 'list' | 'grid'; items: ChatCard[] }
  | { type: 'swimlane'; title: string; lanes: DiagramParticipant[]; steps: SwimlaneStep[] }
  | { type: 'sequence'; title: string; participants: DiagramParticipant[]; messages: SequenceMessage[] }
  | { type: 'diagram'; title: string; direction: 'vertical' | 'horizontal'; nodes: DiagramNode[]; edges: DiagramEdge[] }

const text = (max: number) => z.string().min(1).max(max).refine(value => value.trim().length > 0)
const webURL = text(2048).refine(value => {
  if (/[\s<>\\\u0000-\u001f\u007f]/.test(value)) return false
  try { return ['http:', 'https:'].includes(new URL(value).protocol) } catch { return false }
})
export const workspaceCardImage = (value: string): boolean =>
  /^(?:Crew|Workflow|Chats|_users|db\/assets|assets)\//.test(value) &&
  !/[\\\u0000-\u001f\u007f]/.test(value) && !value.split('/').some(part => part === '..' || part === '.')

const nodeID = text(48).regex(/^[a-zA-Z][a-zA-Z0-9_-]*$/)
const actor = z.strictObject({ id: nodeID, title: text(80) })
const unique = (items: { id: string }[]) => new Set(items.map(item => item.id)).size === items.length
const actors = z.array(actor).min(1).max(6).refine(unique)

export const chatWidgetSchemas = {
  chart: z.strictObject({ title: text(160), style: z.enum(['line', 'bar']), unit: text(40).optional(),
    data: z.array(z.strictObject({ label: text(80), value: z.number().finite().min(-1e15).max(1e15) })).min(1).max(30) }),
  timeline: z.strictObject({ title: text(160), items: z.array(z.strictObject({ when: text(80), title: text(160), detail: text(1500).optional() })).min(1).max(20) }),
  details: z.strictObject({ title: text(160), body: text(12000) }),
  cards: z.strictObject({ title: text(160), layout: z.enum(['list', 'grid']).default('list'), items: z.array(z.strictObject({
    title: text(160), description: text(1000).optional(), image: z.union([webURL, text(2048).refine(workspaceCardImage)]).optional(),
    href: webURL.transform(value => new URL(value).href).optional(),
  })).min(1).max(12) }),
  diagram: z.strictObject({ title: text(160), direction: z.enum(['vertical', 'horizontal']).default('vertical'),
    nodes: z.array(z.strictObject({ id: nodeID, title: text(100), description: text(280).optional(), emphasis: z.boolean().optional(),
      items: z.array(z.strictObject({ title: text(80), description: text(160).optional() })).min(1).max(6).optional(),
    })).min(1).max(16).refine(unique),
    edges: z.array(z.strictObject({ from: nodeID, to: nodeID, label: text(80).optional(), bidirectional: z.boolean().optional() })).max(24).default([]),
  }).refine(value => value.edges.every(edge => value.nodes.some(node => node.id === edge.from) && value.nodes.some(node => node.id === edge.to))),
  swimlane: z.strictObject({ title: text(160), lanes: actors,
    steps: z.array(z.strictObject({ id: nodeID, lane: nodeID, title: text(100), description: text(280).optional() })).min(1).max(16).refine(unique),
  }).refine(value => value.steps.every(step => value.lanes.some(lane => lane.id === step.lane))),
  sequence: z.strictObject({ title: text(160), participants: actors,
    messages: z.array(z.strictObject({ from: nodeID, to: nodeID, label: text(120), reply: z.boolean().optional() })).min(1).max(24),
  }).refine(value => value.messages.every(message => value.participants.some(actor => actor.id === message.from) && value.participants.some(actor => actor.id === message.to))),
}

// A deliberately compact chat catalog; registered widgets keep their existing
// presentation. No executable actions, bindings, arbitrary markup or page layouts.
export const chatVisualCatalog = defineCatalog(schema, {
  components: {
    chart: { props: chatWidgetSchemas.chart, slots: [], description: 'A compact line or bar chart of observed numbers.' },
    timeline: { props: chatWidgetSchemas.timeline, slots: [], description: 'Saved events in supplied order.' },
    details: { props: chatWidgetSchemas.details, slots: [], description: 'Initially collapsed supporting Markdown.' },
    cards: { props: chatWidgetSchemas.cards, slots: [], description: 'Image and text options in list or grid layout.' },
    diagram: { props: chatWidgetSchemas.diagram, slots: [], description: 'A flow or architecture with themed nodes and connectors.' },
    swimlane: { props: chatWidgetSchemas.swimlane, slots: [], description: 'Who performs each step, in supplied execution order.' },
    sequence: { props: chatWidgetSchemas.sequence, slots: [], description: 'Ordered calls and replies between named participants.' },
  },
  actions: {},
})

export function validateChatWidget(type: string, props: unknown): ChatElement | null {
  if (!Object.hasOwn(chatWidgetSchemas, type)) return null
  const result = chatWidgetSchemas[type as keyof typeof chatWidgetSchemas].safeParse(props)
  return result.success ? { ...result.data, type } as ChatElement : null
}
