export type SearchResultRow = { id: string; type: string; label: string; subtitle: string; searchText?: string }

// Artifact slugs are unique only inside a workspace, not in the directory.
export function quickSwitcherArtifactID(row: { workspace: string; dashboard_id: string; document_path: string }): string {
  return `artifact:${JSON.stringify([row.workspace, row.dashboard_id, row.document_path])}`
}

export function uniqueQuickSwitcherArtifacts<T extends { workspace: string; dashboard_id: string; document_path: string }>(rows: T[]): T[] {
  return Array.from(new Map(rows.map(row => [quickSwitcherArtifactID(row), row])).values())
}

export function quickSwitcherMatchRank(item: SearchResultRow, query: string): number {
  const label = item.label.toLowerCase()
  if (label === query) return 0
  if (label.startsWith(query)) return 1
  if (label.includes(query) || (item.type === 'panel' && query.split(/\s+/).every(word => label.includes(word)))) return 2
  return 3
}

export function quickSwitcherMatches(item: SearchResultRow, query: string): boolean {
  // Resources match only visible fields, not hidden descriptions or paths.
  const visible = `${item.label} ${item.subtitle}`.toLowerCase()
  if (item.type === 'artifact' || item.type === 'schedule') return visible.includes(query)
  const searchable = `${visible} ${item.searchText || ''}`.toLowerCase()
  return item.type === 'panel' ? query.split(/\s+/).every(word => searchable.includes(word)) : searchable.includes(query)
}

export const quickSwitcherGroupKey = (row: SearchResultRow): string =>
  ['artifact', 'schedule', 'message'].includes(row.type) ? row.type : 'navigation'

/** Resource groups are contiguous; the best ranked row places the group.
 * Navigation stays intact, including project/chat trees and their keyboard order. */
export function quickSwitcherGroups<T extends SearchResultRow>(rows: T[]): { key: string; rows: T[] }[] {
  const groups = new Map<string, T[]>()
  for (const row of rows) {
    const key = quickSwitcherGroupKey(row)
    const group = groups.get(key) || []
    group.push(row); groups.set(key, group)
  }
  return Array.from(groups, ([key, rows]) => ({ key, rows }))
}
