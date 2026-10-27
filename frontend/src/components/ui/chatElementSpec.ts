export type ChatCard = { title: string; description?: string; image?: string; href?: string }
export type DiagramNode = { id: string; title: string; description?: string; items?: { title: string; description?: string }[]; emphasis?: boolean }
export type DiagramEdge = { from: string; to: string; label?: string; bidirectional?: boolean }

export type ChatElement =
  | { type: 'chart'; title: string; style: 'line' | 'bar'; unit?: string; data: { label: string; value: number }[] }
  | { type: 'timeline'; title: string; items: { when: string; title: string; detail?: string }[] }
  | { type: 'details'; title: string; body: string }
  | { type: 'cards'; title: string; layout: 'list' | 'grid'; items: ChatCard[] }
  | { type: 'diagram'; title: string; direction: 'vertical' | 'horizontal'; nodes: DiagramNode[]; edges: DiagramEdge[] }

const record = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value)
const text = (value: unknown, max: number): value is string =>
  typeof value === 'string' && value.trim().length > 0 && value.length <= max
const optionalText = (value: unknown, max: number): value is string | undefined =>
  value === undefined || text(value, max)

const webURL = (value: unknown): value is string => {
  if (!text(value, 2048) || /[\s<>\\\u0000-\u001f\u007f]/.test(value)) return false
  try { return ['http:', 'https:'].includes(new URL(value).protocol) } catch { return false }
}
export const workspaceCardImage = (value: string): boolean =>
  /^(?:Crew|Workflow|Chats|_users|db\/assets|assets)\//.test(value) &&
  !/[\\\u0000-\u001f\u007f]/.test(value) && !value.split('/').some(part => part === '..' || part === '.')

const nodeID = (value: unknown): value is string => text(value, 48) && /^[a-zA-Z][a-zA-Z0-9_-]*$/.test(value)

// A presentation format only: project known data fields, never HTML, styles,
// callbacks or URLs to fetch. The caller handles incomplete/invalid widgets.
export function parseChatElement(source: string): ChatElement | null {
  if (source.length > 50000) return null
  try {
    const value: unknown = JSON.parse(source)
    if (!record(value) || !text(value.title, 160)) return null
    if (value.type === 'details' && text(value.body, 12000)) {
      return { type: 'details', title: value.title, body: value.body }
    }
    if (value.type === 'cards' && (value.layout === undefined || value.layout === 'list' || value.layout === 'grid') &&
      Array.isArray(value.items) && value.items.length > 0 && value.items.length <= 12) {
      const items: ChatCard[] = []
      for (const item of value.items) {
        if (!record(item) || !text(item.title, 160) || !optionalText(item.description, 1000) ||
          item.href !== undefined && !webURL(item.href) ||
          item.image !== undefined && !(webURL(item.image) || text(item.image, 2048) && workspaceCardImage(item.image))) return null
        items.push({ title: item.title, description: item.description, image: item.image as string | undefined, href: item.href === undefined ? undefined : new URL(item.href as string).href })
      }
      return { type: 'cards', title: value.title, layout: value.layout ?? 'list', items }
    }
    if (value.type === 'diagram' && (value.direction === undefined || value.direction === 'vertical' || value.direction === 'horizontal') &&
      Array.isArray(value.nodes) && value.nodes.length > 0 && value.nodes.length <= 16 &&
      (value.edges === undefined || Array.isArray(value.edges) && value.edges.length <= 24)) {
      const nodes: DiagramNode[] = [], edges: DiagramEdge[] = [], ids = new Set<string>()
      for (const node of value.nodes) {
        if (!record(node) || !nodeID(node.id) || ids.has(node.id) || !text(node.title, 100) || !optionalText(node.description, 280) ||
          node.emphasis !== undefined && typeof node.emphasis !== 'boolean') return null
        ids.add(node.id)
        const items: NonNullable<DiagramNode['items']> = []
        if (node.items !== undefined) {
          if (!Array.isArray(node.items) || node.items.length === 0 || node.items.length > 6) return null
          for (const item of node.items) {
            if (!record(item) || !text(item.title, 80) || !optionalText(item.description, 160)) return null
            items.push({ title: item.title, description: item.description })
          }
        }
        nodes.push({ id: node.id, title: node.title, description: node.description, emphasis: node.emphasis as boolean | undefined, ...(items.length ? { items } : {}) })
      }
      for (const edge of value.edges ?? []) {
        if (!record(edge) || !nodeID(edge.from) || !nodeID(edge.to) || !ids.has(edge.from) || !ids.has(edge.to) ||
          !optionalText(edge.label, 80) || edge.bidirectional !== undefined && typeof edge.bidirectional !== 'boolean') return null
        edges.push({ from: edge.from, to: edge.to, label: edge.label, bidirectional: edge.bidirectional as boolean | undefined })
      }
      return { type: 'diagram', title: value.title, direction: value.direction ?? 'vertical', nodes, edges }
    }
    if (value.type === 'timeline' && Array.isArray(value.items) && value.items.length > 0 && value.items.length <= 20) {
      const items: Extract<ChatElement, { type: 'timeline' }>['items'] = []
      for (const item of value.items) {
        if (!record(item) || !text(item.when, 80) || !text(item.title, 160) || !optionalText(item.detail, 1500)) return null
        items.push({ when: item.when, title: item.title, detail: item.detail })
      }
      return { type: 'timeline', title: value.title, items }
    }
    if (value.type === 'chart' && (value.style === 'line' || value.style === 'bar') && optionalText(value.unit, 40)
      && Array.isArray(value.data) && value.data.length > 0 && value.data.length <= 30) {
      const data: Extract<ChatElement, { type: 'chart' }>['data'] = []
      for (const point of value.data) {
        if (!record(point) || !text(point.label, 80) || typeof point.value !== 'number'
          || !Number.isFinite(point.value) || Math.abs(point.value) > 1e15) return null
        data.push({ label: point.label, value: point.value })
      }
      return { type: 'chart', title: value.title, style: value.style, unit: value.unit, data }
    }
  } catch { /* Final widget validation never repairs malformed JSON. */ }
  return null
}
