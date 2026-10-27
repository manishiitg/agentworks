import { parseChatElement, type ChatElement } from './chatElementSpec'

// Read bounded JSON structure, not terminal text. Only complete properties and
// complete array records can become data; never repair a number or invent braces.
export function parseStreamingChatElement(source: string): ChatElement | null {
  if (source.length > 50000) return null
  const complete = parseChatElement(source)
  if (complete) return complete
  let cursor = 0
  const skip = () => { while (/\s/.test(source[cursor] || '') && cursor < source.length) cursor++ }
  const stringEnd = (start: number) => {
    for (let i = start + 1; i < source.length; i++) {
      if (source[i] === '\\') { i++; continue }
      if (source[i] === '"') return i + 1
    }
    return -1
  }
  const valueEnd = (start: number) => {
    if (source[start] === '"') return stringEnd(start)
    if (source[start] === '{' || source[start] === '[') {
      const stack = [source[start]]
      for (let i = start + 1; i < source.length; i++) {
        const ch = source[i]
        if (ch === '"') {
          const end = stringEnd(i)
          if (end < 0) return -1
          i = end - 1
        } else if (ch === '{' || ch === '[') stack.push(ch)
        else if (ch === '}' || ch === ']') {
          if (stack.pop() !== (ch === '}' ? '{' : '[')) throw new Error('Invalid JSON nesting')
          if (stack.length === 0) return i + 1
        }
      }
      return -1
    }
    for (let i = start; i < source.length; i++) if (/[\s,}\]]/.test(source[i])) return i
    return -1
  }
  const partialBody = (start: number) => {
    let end = start + 1
    for (let i = end; i < source.length; i++) {
      if (source[i] === '\\') {
        if (i + 1 >= source.length) break
        if (source[i + 1] === 'u') {
          if (i + 6 > source.length) break
          i += 5
        } else i++
      }
      end = i + 1
    }
    const body: string = JSON.parse(source.slice(start, end) + '"')
    return /[\uD800-\uDBFF]$/.test(body) ? body.slice(0, -1) : body
  }
  try {
    skip()
    if (source[cursor++] !== '{') return null
    const fields: Record<string, unknown> = Object.create(null)
    let incompleteArray = ''
    while (cursor < source.length) {
      skip()
      if (source[cursor] === '}') break
      if (source[cursor] !== '"') return null
      const keyEnd = stringEnd(cursor)
      if (keyEnd < 0) break
      const key: string = JSON.parse(source.slice(cursor, keyEnd))
      if (Object.hasOwn(fields, key)) return null
      cursor = keyEnd
      skip()
      if (source[cursor++] !== ':') break
      skip()
      if (['data', 'items', 'nodes', 'edges'].includes(key) && source[cursor] === '[') {
        const records: unknown[] = []
        fields[key] = records
        cursor++
        let closed = false
        while (cursor < source.length) {
          skip()
          if (source[cursor] === ']') { cursor++; closed = true; break }
          const end = valueEnd(cursor)
          if (end < 0) break
          records.push(JSON.parse(source.slice(cursor, end)))
          if (records.length > 30) return null
          cursor = end
          skip()
          if (source[cursor] === ']') { cursor++; closed = true; break }
          if (source[cursor] !== ',') {
            if (cursor < source.length) return null
            break
          }
          cursor++
        }
        if (!closed) { incompleteArray = key; break }
      } else {
        const end = valueEnd(cursor)
        if (end < 0) {
          if (key === 'body' && source[cursor] === '"') fields.body = partialBody(cursor)
          break
        }
        fields[key] = JSON.parse(source.slice(cursor, end))
        cursor = end
      }
      skip()
      if (source[cursor] === '}') break
      if (source[cursor] !== ',') {
        if (cursor < source.length) return null
        break
      }
      cursor++
    }
    // An edge declared before its nodes waits until both complete nodes exist.
    // Do not drop bad references from a completed graph; final validation stays strict.
    if (fields.type === 'diagram' && incompleteArray === 'nodes' && Array.isArray(fields.nodes) && Array.isArray(fields.edges)) {
      const ids = new Set(fields.nodes.map(node => node?.id))
      fields.edges = fields.edges.filter(edge => ids.has(edge?.from) && ids.has(edge?.to))
    }
    return parseChatElement(JSON.stringify(fields))
  } catch { return null }
}
