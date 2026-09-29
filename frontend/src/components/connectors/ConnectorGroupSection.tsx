import { useState, type ReactNode } from 'react'
import { Plus } from 'lucide-react'

/** How many cards a directory group shows before "+ N more". */
export const GROUP_PREVIEW = 4

/**
 * One shelf of the connector directory: its first GROUP_PREVIEW cards, and a
 * "+ N more" that expands the rest. A search shows every match.
 */
export function ConnectorGroupSection<T>({ label, entries, render, expandAll = false, gridGap = 'gap-2' }: {
  label: string
  entries: T[]
  render: (entry: T) => ReactNode
  expandAll?: boolean
  gridGap?: string
}) {
  const [expanded, setExpanded] = useState(false)
  const open = expanded || expandAll || entries.length <= GROUP_PREVIEW
  const shown = open ? entries : entries.slice(0, GROUP_PREVIEW)
  return (
    <div className="mb-5 last:mb-0">
      <p className="mb-2 text-xs font-semibold text-muted-foreground">{label}</p>
      <div className={`grid grid-cols-1 ${gridGap} md:grid-cols-2`}>{shown.map(render)}</div>
      {!open && (
        <button
          type="button"
          onClick={() => setExpanded(true)}
          className="mt-2 inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-primary hover:bg-primary/10"
        >
          <Plus className="h-3.5 w-3.5" />{entries.length - GROUP_PREVIEW} more
        </button>
      )}
    </div>
  )
}
