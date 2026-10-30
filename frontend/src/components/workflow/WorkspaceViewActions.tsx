import { WorkspaceViewIconButton } from './WorkspaceViewIconButton'
import type { ReactNode } from 'react'

export interface WorkspaceViewActionsProps {
  onRefresh: () => void | Promise<void>
  refreshing?: boolean
  refreshLabel?: string
  /** The panel refreshes itself, so the header shows no Refresh icon (owner, 2026-10-20). */
  hideRefresh?: boolean
  children?: ReactNode
}

/**
 * The standard actions shown in a right-side workspace view header. Order is
 * a product rule: page operations, then any explicit refresh.
 */
export function WorkspaceViewActions({
  onRefresh,
  refreshing = false,
  refreshLabel = 'Refresh view',
  hideRefresh = false,
  children,
}: WorkspaceViewActionsProps) {
  return (
    <div className="flex shrink-0 items-center gap-2">
      {children}
      {!hideRefresh && <WorkspaceViewIconButton label={refreshLabel} onClick={onRefresh} disabled={refreshing} spinning={refreshing} />}
    </div>
  )
}
