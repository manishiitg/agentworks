import { useCallback, useEffect, useState } from 'react'
import { GatewayApiError, type GatewayConnector, type GatewayProvider } from './gatewayAdminApi'

export function gatewayErrorMessage(err: unknown): string {
  if (err instanceof GatewayApiError) return err.message
  if (err instanceof Error) return err.message
  return 'Something went wrong.'
}

export const tableClass = 'w-full border-collapse text-xs'
export const thClass = 'border-b border-border px-2 py-1.5 text-left font-medium text-muted-foreground'
export const tdClass = 'border-b border-border/60 px-2 py-1.5 align-top'
export const codeClass = 'rounded bg-muted px-1 py-0.5 font-mono text-[11px]'

/**
 * Loads console data once per attempt with cancellation. Panels refetch by
 * bumping the attempt counter (manual refresh only — no polling, no retry
 * storms against a gateway that may be down).
 */
export function useGatewayLoader<T>(load: () => Promise<T>, attempt: number): {
  data: T | null
  loading: boolean
  error: string | null
} {
  const [data, setData] = useState<T | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError(null)
    void load().then(
      (value) => {
        if (cancelled) return
        setData(value)
        setLoading(false)
      },
      (err: unknown) => {
        if (cancelled) return
        setError(gatewayErrorMessage(err))
        setLoading(false)
      },
    )
    return () => {
      cancelled = true
    }
    // load is panel-local and stable per attempt; attempt drives refetch.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [attempt])

  return { data, loading, error }
}

export function useAttempt(): [number, () => void] {
  const [attempt, setAttempt] = useState(0)
  const bump = useCallback(() => setAttempt((n) => n + 1), [])
  return [attempt, bump]
}

export function plural(count: number, one: string, many?: string): string {
  return `${count} ${count === 1 ? one : (many ?? `${one}s`)}`
}

export interface McpServerEntry {
  name: string
  url: string
}

/**
 * Parses a pasted MCP servers block in the standard Claude-style shape:
 * `{ "mcpServers": { name: { url } } }` (a bare `{ name: { url } }` map is
 * accepted too). Only Streamable-HTTP (`url`) servers can join the gateway;
 * anything else is reported as skipped with a reason.
 */
export function parseMcpServersJson(text: string): { servers: McpServerEntry[]; skipped: Array<{ name: string; reason: string }> } {
  let doc: unknown
  try {
    doc = JSON.parse(text)
  } catch {
    throw new Error('That is not valid JSON.')
  }
  const root = doc as Record<string, unknown>
  const map: unknown =
    root !== null && typeof root === 'object' && !Array.isArray(root) && 'mcpServers' in root ? root.mcpServers : root
  if (map === null || typeof map !== 'object' || Array.isArray(map)) {
    throw new Error('Expected { "mcpServers": { "<name>": { "url": "https://…/mcp" } } }.')
  }
  const servers: McpServerEntry[] = []
  const skipped: Array<{ name: string; reason: string }> = []
  for (const [name, entry] of Object.entries(map as Record<string, unknown>)) {
    if (entry === null || typeof entry !== 'object' || Array.isArray(entry)) {
      skipped.push({ name, reason: 'not an object' })
      continue
    }
    const url = (entry as Record<string, unknown>).url
    if (typeof url !== 'string' || url.trim() === '') {
      skipped.push({ name, reason: 'only Streamable-HTTP ("url") servers are supported' })
      continue
    }
    servers.push({ name, url: url.trim() })
  }
  if (servers.length === 0) {
    throw new Error(
      skipped.length > 0
        ? `No usable servers: ${skipped.map((s) => `${s.name} (${s.reason})`).join('; ')}.`
        : 'No servers found in that JSON.',
    )
  }
  return { servers, skipped }
}

export interface ToolArg {
  name: string
  type: string
  required: boolean
  description: string
}

/**
 * Decodes a tool's base64 JSON input schema into a flat argument list.
 * Returns null when the upstream sent no (or an unreadable) schema.
 */
export function parseToolArgs(schemaB64: string | null | undefined): ToolArg[] | null {
  if (!schemaB64) return null
  try {
    const schema = JSON.parse(atob(schemaB64)) as { properties?: Record<string, { type?: unknown; description?: unknown }>; required?: unknown }
    if (!schema.properties || typeof schema.properties !== 'object') return []
    const required = new Set(Array.isArray(schema.required) ? schema.required.filter((r): r is string => typeof r === 'string') : [])
    return Object.entries(schema.properties).map(([name, def]) => ({
      name,
      type: Array.isArray(def?.type) ? def.type.map(String).join(' | ') : typeof def?.type === 'string' ? def.type : 'any',
      required: required.has(name),
      description: typeof def?.description === 'string' ? def.description : '',
    }))
  } catch {
    return null
  }
}

/** Lowercase alphanumeric key; mirrors the gateway catalog.Key and the brand-slug normalization. */
export function normalizeServerKey(name: string): string {
  return name.toLowerCase().replace(/[^a-z0-9]/g, '')
}

export interface AgentWorksServer {
  name: string
  connection?: string
  status?: string
  toolCount: number
}

export interface ServerRow {
  key: string
  /** Display name: the AgentWorks server name when known, else the gateway provider. */
  name: string
  agentworks: AgentWorksServer | null
  /** Gateway connector instances for this server (multi-instance allowed). */
  gateway: GatewayConnector[]
  /** Catalog template used for one-click "add to gateway" (null when unknown). */
  catalogMatch: GatewayProvider | null
}

/**
 * Centralizes the two server lists: every AgentWorks MCP server plus every
 * gateway connector, merged by normalized name so one row shows both sides.
 */
export function mergeServerRows(
  agentworks: AgentWorksServer[],
  connectors: GatewayConnector[],
  providers: GatewayProvider[],
): ServerRow[] {
  const rows = new Map<string, ServerRow>()
  const ensure = (key: string, name: string): ServerRow => {
    let row = rows.get(key)
    if (!row) {
      row = { key, name, agentworks: null, gateway: [], catalogMatch: null }
      rows.set(key, row)
    }
    return row
  }

  for (const server of agentworks) {
    const row = ensure(normalizeServerKey(server.name), server.name)
    row.name = server.name
    row.agentworks = server
  }
  for (const connector of connectors) {
    const row = ensure(normalizeServerKey(connector.Provider), connector.Provider)
    row.gateway.push(connector)
  }
  for (const row of rows.values()) {
    row.catalogMatch = providers.find((p) => p.Key === row.key) ?? null
  }
  return [...rows.values()].sort((a, b) => a.name.localeCompare(b.name))
}
