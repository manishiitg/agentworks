// Web searches made by coding agents, read back from their tool-call events so
// the chat can show "Searched: <query>" with clickable results instead of a
// generic tool card with raw JSON. Each agent reports a search differently;
// the shapes below were checked against real recorded calls (2026-10-07):
//
//   Claude Code  WebSearch   args {"query","mode"}; result text
//                "Web search results for query: \"…\"\n\nLinks: [{title,url},…]\n\n<summary>"
//   Cursor       WebSearch   args {"search_term","explanation"}, sometimes inside a
//                CallDynamicTool wrapper {"toolName":"WebSearch","arguments":{…}};
//                result "Title: Web search results\nContent: Links:\n1. [title](url)…"
//   Codex        web_search  args {"query"} when the adapter kept it (often none);
//                no result list. A query that is a URL is a page Codex opened.
//   AGY          search_web  args {"query"}; no result unless the call failed.
//
// Pure functions only: the same parse feeds the live chat, restored history
// and the step-log viewer.
import { formatToolCallResult } from './toolCallFormatting'

export interface WebSearchResultRow {
  title: string
  url: string
  domain: string
}

export interface WebSearchView {
  /** The query as the agent sent it; '' when the agent did not report one. */
  query: string
  /** Every query when one call ran several (Codex batches them). */
  queries: string[]
  results: WebSearchResultRow[]
  /** Pages the agent opened directly rather than searched for (Codex). */
  opened: string[]
  isError: boolean
  /** The result text, unwrapped, for an error or a "details" view. */
  text: string
}

const WEB_SEARCH_TOOL_NAMES = new Set(['websearch', 'web_search', 'search_web'])
const DYNAMIC_WRAPPER_NAMES = new Set(['calldynamictool', 'callmcptool'])

type JsonRecord = Record<string, unknown>

function isRecord(value: unknown): value is JsonRecord {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value)
}

function parseRecord(text: string | undefined): JsonRecord | null {
  if (!text || !text.trim()) return null
  try {
    const parsed: unknown = JSON.parse(text)
    return isRecord(parsed) ? parsed : null
  } catch {
    return null
  }
}

function str(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

function baseToolName(raw: string): string {
  const match = /^mcp__.+?__(.+)$/.exec(raw.trim())
  return (match ? match[1] : raw.trim()).toLowerCase()
}

/**
 * The search arguments when this call is a web search, else null. Unwraps
 * Cursor's CallDynamicTool / CallMcpTool envelope whose target is WebSearch.
 */
function webSearchArgs(name: string, args: string | undefined): JsonRecord | null {
  const base = baseToolName(name || '')
  if (WEB_SEARCH_TOOL_NAMES.has(base)) return parseRecord(args) ?? {}
  if (!DYNAMIC_WRAPPER_NAMES.has(base.replace(/[^a-z]/g, ''))) return null
  const envelope = parseRecord(args)
  if (!envelope) return null
  const target = baseToolName(str(envelope.toolName) || str(envelope.tool_name))
  if (!WEB_SEARCH_TOOL_NAMES.has(target)) return null
  const inner = envelope.arguments ?? envelope.args ?? envelope.input
  if (isRecord(inner)) return inner
  if (typeof inner === 'string') return parseRecord(inner) ?? {}
  return {}
}

export function isWebSearchToolCall(name: string, args?: string): boolean {
  return webSearchArgs(name, args) !== null
}

function isHttpUrl(value: string): boolean {
  return /^https?:\/\/\S+$/i.test(value)
}

function domainOf(url: string): string {
  try {
    return new URL(url).hostname.replace(/^www\./, '')
  } catch {
    return ''
  }
}

function row(title: unknown, url: unknown): WebSearchResultRow | null {
  const href = str(url)
  if (!isHttpUrl(href)) return null
  const domain = domainOf(href)
  if (!domain) return null
  return { title: str(title) || domain, url: href, domain }
}

/** Index just past the JSON array that opens at `start`, or -1. */
function jsonArrayEnd(text: string, start: number): number {
  let depth = 0
  let inString = false
  for (let i = start; i < text.length; i++) {
    const ch = text[i]
    if (inString) {
      if (ch === '\\') i++
      else if (ch === '"') inString = false
      continue
    }
    if (ch === '"') inString = true
    else if (ch === '[' || ch === '{') depth++
    else if (ch === ']' || ch === '}') {
      depth--
      if (depth === 0) return i + 1
    }
  }
  return -1
}

// Claude Code: one or more `Links: [{"title":…,"url":…}]` blocks.
function claudeLinkRows(text: string): WebSearchResultRow[] {
  const rows: WebSearchResultRow[] = []
  const marker = /Links:\s*\[/g
  let match: RegExpExecArray | null
  while ((match = marker.exec(text)) !== null) {
    const start = match.index + match[0].length - 1
    const end = jsonArrayEnd(text, start)
    if (end < 0) break
    try {
      const parsed: unknown = JSON.parse(text.slice(start, end))
      if (Array.isArray(parsed)) {
        for (const item of parsed) {
          if (!isRecord(item)) continue
          const next = row(item.title, item.url)
          if (next) rows.push(next)
        }
      }
    } catch {
      /* not the JSON form; other parsers below may still read it */
    }
    marker.lastIndex = end
  }
  return rows
}

// A JSON payload with a results/links array of {title,url} (bridge search tools).
function jsonResultRows(text: string): WebSearchResultRow[] {
  const parsed = parseRecord(text)
  if (!parsed) return []
  const list = [parsed.results, parsed.links, parsed.items].find(Array.isArray) as unknown[] | undefined
  if (!list) return []
  return list.flatMap(item => {
    if (!isRecord(item)) return []
    const next = row(item.title ?? item.name, item.url ?? item.link)
    return next ? [next] : []
  })
}

// Cursor and other markdown output: [title](https://…)
function markdownLinkRows(text: string): WebSearchResultRow[] {
  const rows: WebSearchResultRow[] = []
  for (const match of text.matchAll(/\[([^\]\n]+)\]\((https?:\/\/[^\s)]+)\)/g)) {
    const next = row(match[1], match[2])
    if (next) rows.push(next)
  }
  return rows
}

// Anything else (AGY's text): bare http(s) links.
function bareUrlRows(text: string): WebSearchResultRow[] {
  const rows: WebSearchResultRow[] = []
  for (const match of text.matchAll(/https?:\/\/[^\s<>"'`)\]}]+/g)) {
    const next = row('', match[0].replace(/[.,;:!?]+$/, ''))
    if (next) rows.push(next)
  }
  return rows
}

function dedupe(rows: WebSearchResultRow[]): WebSearchResultRow[] {
  const seen = new Set<string>()
  return rows.filter(item => {
    if (seen.has(item.url)) return false
    seen.add(item.url)
    return true
  })
}

/** Parses a web-search tool call; null when the call is not a web search. */
export function parseWebSearchToolCall(input: { name: string; args?: string; result?: string; isError?: boolean }): WebSearchView | null {
  const args = webSearchArgs(input.name, input.args)
  if (!args) return null

  const formatted = input.result ? formatToolCallResult(input.result) : null
  const text = formatted?.text ?? ''
  const isError = Boolean(input.isError || formatted?.isError)

  const action = isRecord(args.action) ? args.action : {}
  const queries = [
    str(args.query), str(args.search_term), str(args.searchTerm), str(args.q), str(action.query),
    ...(Array.isArray(action.queries) ? action.queries.map(str) : []),
  ].filter((value, index, all) => value && all.indexOf(value) === index)
  if (queries.length === 0) {
    const header = /^Web search results for query: "(.*)"\s*$/m.exec(text)
    if (header?.[1]) queries.push(header[1].trim())
  }

  const opened: string[] = []
  const actionUrl = str(action.url)
  if (isHttpUrl(actionUrl)) opened.push(actionUrl)
  // Codex reports an opened page as a search whose "query" is the URL.
  const searches = queries.filter(query => {
    if (!isHttpUrl(query)) return true
    if (!opened.includes(query)) opened.push(query)
    return false
  })

  let results: WebSearchResultRow[] = []
  if (text && !isError) {
    for (const parse of [claudeLinkRows, jsonResultRows, markdownLinkRows, bareUrlRows]) {
      results = dedupe(parse(text))
      if (results.length > 0) break
    }
  }

  return { query: searches[0] ?? '', queries: searches, results, opened, isError, text }
}
