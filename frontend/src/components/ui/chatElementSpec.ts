export type ChatElement =
  | { type: 'chart'; title: string; style: 'line' | 'bar'; unit?: string; data: { label: string; value: number }[] }
  | { type: 'timeline'; title: string; items: { when: string; title: string; detail?: string }[] }
  | { type: 'details'; title: string; body: string }

const record = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value)
const text = (value: unknown, max: number): value is string =>
  typeof value === 'string' && value.trim().length > 0 && value.length <= max
const optionalText = (value: unknown, max: number): value is string | undefined =>
  value === undefined || text(value, max)

// A presentation format only: project known data fields, never HTML, styles,
// callbacks or URLs to fetch. Incomplete/invalid fences remain ordinary code.
export function parseChatElement(source: string): ChatElement | null {
  if (source.length > 50000) return null
  try {
    const value: unknown = JSON.parse(source)
    if (!record(value) || !text(value.title, 160)) return null
    if (value.type === 'details' && text(value.body, 12000)) {
      return { type: 'details', title: value.title, body: value.body }
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
  } catch { /* Streaming JSON and ordinary invalid code stay readable. */ }
  return null
}
