import type { ReactNode } from 'react'

/** The EXPLORER title and the folder row that carries the toolbar icons. */
export function ExplorerHeader({ title, titleAction, toolbar, leading }: {
  title: string
  /** Right of the EXPLORER title (Ask AI). */
  titleAction?: ReactNode
  toolbar: ReactNode
  /** Left of the folder name (select-all in selection mode). */
  leading?: ReactNode
}) {
  return (
    <div className="shrink-0 border-b border-border">
      <div className="flex h-9 items-center justify-between gap-2 px-3">
        <h2 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">Explorer</h2>
        <div className="flex items-center gap-1">{titleAction}</div>
      </div>
      <div className="flex h-8 items-center gap-1 px-2">
        {leading}
        <span className="min-w-0 flex-1 truncate text-xs font-semibold uppercase tracking-wide text-foreground" title={title}>{title}</span>
        <div className="flex shrink-0 items-center gap-0.5">{toolbar}</div>
      </div>
    </div>
  )
}
