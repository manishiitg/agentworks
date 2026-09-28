import { WorkspaceViewIconButton } from './WorkspaceViewIconButton'
import type { ReactNode } from 'react'

export interface WorkspaceViewActionsProps {
  workspacePath: string | null
  message: string
  onRefresh: () => void | Promise<void>
  refreshing?: boolean
  refreshLabel?: string
  /** Ask AI routing override (Crew panes route to the project chat). */
  onAsk?: (message: string) => void | Promise<void>
  walkthrough?: ReactNode
}

/**
 * The standard actions shown in a right-side workspace view header. Order is
 * a product rule: walkthrough always left, refresh always right. The header
 * reads the Ask AI config (workspacePath, message, onAsk) off this element's
 * props and carries it into the walkthrough popup — Ask AI never renders in
 * the row itself.
 */
export function WorkspaceViewActions({
  onRefresh,
  refreshing = false,
  refreshLabel = 'Refresh view',
  walkthrough,
}: WorkspaceViewActionsProps) {
  return (
    <div className="flex shrink-0 items-center gap-2">
      {walkthrough}
      <WorkspaceViewIconButton label={refreshLabel} onClick={onRefresh} disabled={refreshing} spinning={refreshing} />
    </div>
  )
}
