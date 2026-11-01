import type { WorkspaceStatusRow } from '../../products/work/useWorkspaceStatus'

/** Summarize observed connection state; missing state stays unknown. */
export function connectionSummary(rows: WorkspaceStatusRow[], count = false): string | undefined {
  if (!count && !rows.some(row => typeof row.connected === 'boolean' || row.attention)) return undefined
  const connected = rows.filter(row => row.connected).length
  const attention = rows.filter(row => row.attention).length
  const counted = count || rows.length > 1
  const label = counted ? `${connected} connected` : connected ? 'Connected' : attention ? 'Needs attention' : 'Not connected'
  return counted && attention ? `${label} · ${attention} need attention` : label
}
