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
