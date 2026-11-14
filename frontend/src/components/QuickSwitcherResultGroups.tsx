import { Fragment, type ReactNode } from 'react'
import { quickSwitcherGroups, type SearchResultRow } from '../utils/quickSwitcherResults'

const headings: Record<string, string> = { artifact: 'Artifacts', schedule: 'Schedules', message: 'In messages · this workspace' }

// Status rows belong to their group, never above unrelated results. Empty
// groups with a status are appended after groups containing actual matches.
export function QuickSwitcherResultGroups<T extends SearchResultRow>({ rows, footers, renderRow }: {
  rows: T[]; footers: Partial<Record<string, ReactNode>>; renderRow: (row: T, index: number) => ReactNode
}) {
  const groups = quickSwitcherGroups(rows)
  for (const key of Object.keys(footers)) if (footers[key] && !groups.some(group => group.key === key)) groups.push({ key, rows: [] })
  const indexes = new Map(rows.map((row, index) => [row.id, index]))
  return <>{groups.map(group => <section key={group.key} role="group" aria-label={headings[group.key] || 'Navigation'}>
    {headings[group.key] && <h3 className="px-4 pb-1 pt-3 text-xs font-medium text-muted-foreground">{headings[group.key]}</h3>}
    {group.rows.map(row => <Fragment key={row.id}>{renderRow(row, indexes.get(row.id)!)}</Fragment>)}
    {footers[group.key]}
  </section>)}</>
}
