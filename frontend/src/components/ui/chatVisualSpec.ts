import { createSpecStreamCompiler, parseSpecStreamLine, type Spec } from '@json-render/core'
import { z } from 'zod'
import { validateChatWidget } from './chatElementSpec'

const patchSchema = z.discriminatedUnion('op', [
  z.strictObject({ op: z.enum(['add', 'replace']), path: z.string(), value: z.unknown() }),
  z.strictObject({ op: z.literal('remove'), path: z.string() }),
])
const envelope = z.strictObject({
  root: z.literal('visual'),
  elements: z.strictObject({ visual: z.strictObject({
    type: z.string(), props: z.record(z.string(), z.unknown()), children: z.array(z.never()).default([]),
  }) }),
})

// The compiler owns JSONL parsing and patch application. The app's catalog
// still owns what may appear in a message. One leaf widget per fenced block
// preserves chat composition and its disclosure identity.
export function compileChatVisual(source: string, progressive = false): Spec | null {
  if (source.length > 50000) return null
  const lines = source.split('\n')
  const accepted: string[] = []
  for (let index = 0; index < lines.length; index++) {
    const line = lines[index].trim()
    if (!line) continue
    const parsed = parseSpecStreamLine(line)
    // A cut string/number/object waits for its complete operation, never
    // repaired into a different value. Invalid completed streams stay invalid.
    if (!parsed && progressive && index === lines.length - 1) break
    const patch = patchSchema.safeParse(parsed)
    if (!patch.success || accepted.length >= 256) return null
    const parts = patch.data.path.split('/').slice(1).map(part => part.replace(/~1/g, '/').replace(/~0/g, '~'))
    if (!patch.data.path.startsWith('/') || parts.length > 10 ||
      !['root', 'elements'].includes(parts[0]) ||
      (parts[0] === 'root' && parts.length !== 1) ||
      (parts[0] === 'elements' && parts.length > 1 && parts[1] !== 'visual') ||
      parts.some(part => ['__proto__', 'prototype', 'constructor'].includes(part) || /^\d+$/.test(part) && Number(part) > 30)) return null
    accepted.push(line)
  }
  try {
    const compiler = createSpecStreamCompiler()
    const { result } = compiler.push(accepted.join('\n') + '\n')
    const checked = envelope.safeParse(result)
    if (!checked.success) return null
    const raw = checked.data.elements.visual
    const widget = validateChatWidget(raw.type, raw.props)
    if (!widget) return null
    const { type, ...props } = widget
    return { root: 'visual', elements: { visual: { type, props, children: [] } } }
  } catch { return null }
}
