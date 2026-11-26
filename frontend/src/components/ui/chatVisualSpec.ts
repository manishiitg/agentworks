import { createSpecStreamCompiler, parseSpecStreamLine, type Spec } from '@json-render/core'
import { z } from 'zod'
import { validateChatWidget } from './chatElementSpec'

export const CHAT_VISUAL_MAX_LENGTH = 50000

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
// preserves chat composition and its disclosure identity. A block without its
// `/root` line still means the one "visual" root, and a missing `children` is
// the empty list: both are common slips that change nothing a widget may hold.
export function compileChatVisual(source: string, progressive = false): Spec | null {
  if (source.length > CHAT_VISUAL_MAX_LENGTH) return null
  const lines = source.split('\n')
  const compiler = createSpecStreamCompiler({ root: 'visual' })
  let operations = 0
  // While a reply streams, show the latest complete state that is a valid
  // widget (for example once a list has its first row), so a widget grows one
  // line at a time. The finished reply is checked strictly as a whole.
  let shown: Spec | null = null
  try {
    for (let index = 0; index < lines.length; index++) {
      const line = lines[index].trim()
      if (!line) continue
      const parsed = parseSpecStreamLine(line)
      // A cut string/number/object waits for its complete operation, never
      // repaired into a different value. Invalid completed streams stay invalid.
      if (!parsed && progressive && index === lines.length - 1) break
      const patch = patchSchema.safeParse(parsed)
      if (!patch.success || ++operations > 256) return null
      const parts = patch.data.path.split('/').slice(1).map(part => part.replace(/~1/g, '/').replace(/~0/g, '~'))
      if (!patch.data.path.startsWith('/') || parts.length > 10 ||
        !['root', 'elements'].includes(parts[0]) ||
        (parts[0] === 'root' && parts.length !== 1) ||
        (parts[0] === 'elements' && parts.length > 1 && parts[1] !== 'visual') ||
        parts.some(part => ['__proto__', 'prototype', 'constructor'].includes(part) || /^\d+$/.test(part) && Number(part) > 100)) return null
      const { result } = compiler.push(line + '\n')
      if (progressive) shown = checkedVisual(result) ?? shown
    }
    return progressive ? shown : checkedVisual(compiler.getResult())
  } catch { return null }
}

function checkedVisual(result: unknown): Spec | null {
  const checked = envelope.safeParse(result)
  if (!checked.success) return null
  const raw = checked.data.elements.visual
  // validateChatWidget returns parsed copies, so later patches cannot change
  // a state that was already shown.
  const widget = validateChatWidget(raw.type, raw.props)
  if (!widget) return null
  const { type, ...props } = widget
  return { root: 'visual', elements: { visual: { type, props, children: [] } } }
}
