/** Turn a published function's input schema into form fields, and the filled
 * fields back into the call arguments. Only the person's own entries are sent;
 * the dashboard page can pre-fill defaults but never submit. */

export type ActionFieldKind = 'text' | 'number' | 'integer' | 'boolean' | 'choice' | 'json'

export type ActionField = {
  name: string
  label: string
  kind: ActionFieldKind
  required: boolean
  description?: string
  choices?: string[]
  value: string | boolean
}

type SchemaProperty = { type?: unknown; description?: unknown; enum?: unknown; default?: unknown; title?: unknown }
type Schema = { properties?: Record<string, SchemaProperty>; required?: unknown }

function propertyType(property: SchemaProperty): string {
  const raw = Array.isArray(property.type) ? property.type.find(t => t !== 'null') : property.type
  return typeof raw === 'string' ? raw : ''
}

function asText(value: unknown): string {
  if (value === undefined || value === null) return ''
  return typeof value === 'string' ? value : typeof value === 'number' || typeof value === 'boolean' ? String(value) : JSON.stringify(value, null, 2)
}

export function buildActionFields(schema: unknown, defaults?: Record<string, unknown>): ActionField[] {
  const root = (schema && typeof schema === 'object' ? schema : {}) as Schema
  const required = new Set(Array.isArray(root.required) ? root.required.filter((n): n is string => typeof n === 'string') : [])
  const fields: ActionField[] = []
  for (const [name, property] of Object.entries(root.properties ?? {})) {
    const prop = (property && typeof property === 'object' ? property : {}) as SchemaProperty
    const type = propertyType(prop)
    const given = defaults && Object.prototype.hasOwnProperty.call(defaults, name) ? defaults[name] : prop.default
    const label = typeof prop.title === 'string' && prop.title.trim() ? prop.title : name
    const description = typeof prop.description === 'string' ? prop.description : undefined
    const choices = Array.isArray(prop.enum) && prop.enum.length > 0 && prop.enum.every(v => typeof v === 'string') ? (prop.enum as string[]) : undefined
    const base = { name, label, required: required.has(name), description }
    if (choices) fields.push({ ...base, kind: 'choice', choices, value: asText(given) })
    else if (type === 'boolean') fields.push({ ...base, kind: 'boolean', value: given === true })
    else if (type === 'integer' || type === 'number') fields.push({ ...base, kind: type, value: asText(given) })
    else if (type === 'string') fields.push({ ...base, kind: 'text', value: asText(given) })
    else fields.push({ ...base, kind: 'json', value: asText(given) })
  }
  return fields
}

export function actionArgs(fields: ActionField[]): { ok: true; args: Record<string, unknown> } | { ok: false; error: string } {
  const args: Record<string, unknown> = {}
  for (const field of fields) {
    if (field.kind === 'boolean') { args[field.name] = field.value === true; continue }
    const text = typeof field.value === 'string' ? field.value.trim() : ''
    if (!text) {
      if (field.required) return { ok: false, error: `${field.label} is required.` }
      continue
    }
    if (field.kind === 'number' || field.kind === 'integer') {
      const n = Number(text)
      if (!Number.isFinite(n) || (field.kind === 'integer' && !Number.isInteger(n))) return { ok: false, error: `${field.label} must be ${field.kind === 'integer' ? 'a whole number' : 'a number'}.` }
      args[field.name] = n
    } else if (field.kind === 'json') {
      try { args[field.name] = JSON.parse(text) } catch { return { ok: false, error: `${field.label} must be valid JSON.` } }
    } else {
      args[field.name] = field.kind === 'text' ? (typeof field.value === 'string' ? field.value : text) : text
    }
  }
  return { ok: true, args }
}
