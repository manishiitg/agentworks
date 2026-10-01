import type { HTMLAttributes } from 'react'
import { cn } from '../../lib/utils'

/** Shared icon group frame in the right workspace toolbar. */
export function WorkspaceToolbarFrame({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cn('inline-flex h-8 items-center divide-x divide-border rounded-lg border border-border bg-muted/60 py-0.5 shadow-sm', className)} {...props} />
}
