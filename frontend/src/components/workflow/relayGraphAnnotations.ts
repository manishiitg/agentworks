/** Display metadata only. These records never change Python execution. */
export type RelayNodeKind = 'input' | 'agent' | 'script' | 'decision' | 'output'
export interface RelaySourceNode {
  id: string
  type: RelayNodeKind
  label: string
  description?: string
  call?: string
  input?: unknown
  output?: unknown
  system_prompt?: string
  user_message?: string
  messages?: string[]
  model?: unknown
  tools?: string[]
  skills?: string[]
  mcp?: unknown
  line: number
}
export interface RelaySourceEdge { from: string; to: string; label?: string; line: number }
export interface RelaySourceGraph { nodes: RelaySourceNode[]; edges: RelaySourceEdge[]; errors: string[] }
const kinds: RelayNodeKind[] = ['input', 'agent', 'script', 'decision', 'output']
const validID = (value: unknown): value is string => typeof value === 'string' && /^[a-zA-Z][\w-]{0,63}$/.test(value)
const stringList = (value: unknown) => Array.isArray(value) && value.every(item => typeof item === 'string')

// Only standalone Python comments count. In particular, records inside quoted
// examples/docstrings must not become nodes. No imports or code evaluation.
function comments(source: string): { text: string; line: number }[] {
  const result: { text: string; line: number }[] = []
  let quote = '', triple = false
  source.split('\n').forEach((line, index) => {
    for (let i = 0; i < line.length; i++) {
      const char = line[i]
      if (quote) {
        if (char === '\\') { i++; continue }
        if (char === quote && (!triple || line.slice(i, i + 3) === quote.repeat(3))) {
          if (triple) i += 2
          quote = ''; triple = false
        }
      } else if (char === '#' ) {
        if (!line.slice(0, i).trim()) result.push({ text: line.slice(i), line: index + 1 })
        break
      } else if (char === '"' || char === "'") {
        quote = char; triple = line.slice(i, i + 3) === char.repeat(3)
        if (triple) i += 2
      }
    }
  })
  return result
}

export function parseRelaySourceGraph(source: string): RelaySourceGraph {
  const graph: RelaySourceGraph = { nodes: [], edges: [], errors: [] }
  const ids = new Set<string>(), edges = new Set<string>()
  for (const comment of comments(source)) {
    if (!/^#\s*@relay\b/.test(comment.text)) continue
    try {
      const match = comment.text.match(/^#\s*@relay\s+(node|edge)\s+(.+)$/)
      if (!match) throw new Error('Expected @relay node or @relay edge followed by one JSON object')
      const record = JSON.parse(match[2])
      if (!record || typeof record !== 'object' || Array.isArray(record)) throw new Error('Annotation must be a JSON object')
      if (match[1] === 'node') {
        if (!validID(record.id) || !kinds.includes(record.type) || typeof record.label !== 'string' || !record.label.trim()) throw new Error('Node needs a stable id, supported type and readable label')
        if (ids.has(record.id)) throw new Error(`Duplicate node id: ${record.id}`)
        for (const field of ['description', 'call', 'system_prompt', 'user_message']) {
          if (record[field] !== undefined && typeof record[field] !== 'string') throw new Error(`${field} must be text`)
        }
        for (const field of ['messages', 'tools', 'skills']) {
          if (record[field] !== undefined && !stringList(record[field])) throw new Error(`${field} must be a list of text`)
        }
        ids.add(record.id)
        graph.nodes.push({ ...record, line: comment.line })
      } else {
        if (!validID(record.from) || !validID(record.to) || (record.label !== undefined && typeof record.label !== 'string')) throw new Error('Edge needs from/to node ids and an optional text label')
        const key = JSON.stringify([record.from, record.to, record.label ?? ''])
        if (edges.has(key)) throw new Error('Duplicate edge')
        edges.add(key)
        graph.edges.push({ from: record.from, to: record.to, label: record.label, line: comment.line })
      }
      if (graph.nodes.length > 200 || graph.edges.length > 400) throw new Error('Graph exceeds 200 nodes or 400 edges')
    } catch (error) {
      graph.errors.push(`Line ${comment.line}: ${error instanceof Error ? error.message : 'Invalid graph annotation'}`)
    }
    if (graph.nodes.length > 200 || graph.edges.length > 400) break
  }
  for (const edge of graph.edges) {
    if (!ids.has(edge.from) || !ids.has(edge.to)) graph.errors.push(`Line ${edge.line}: Edge references an unknown node (${edge.from} → ${edge.to})`)
  }
  return graph
}
