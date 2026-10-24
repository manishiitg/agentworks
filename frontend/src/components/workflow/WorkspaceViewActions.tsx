import { WorkspaceViewIconButton } from './WorkspaceViewIconButton'
import type { ReactNode } from 'react'
import { AskAIButton } from './AskAIButton'
import { useWorkspacePanelHosted } from '../workspace/WorkspacePanelNavigation'

export interface WorkspaceViewActionsProps {
  workspacePath: string | null
  message: string
  onRefresh: () => void | Promise<void>
  refreshing?: boolean
  refreshLabel?: string
  /** Ask AI routing override (Crew panes route to the project chat). */
  onAsk?: (message: string) => void | Promise<void>
  /** The panel refreshes itself, so the header shows no Refresh icon (owner, 2026-10-20). */
  hideRefresh?: boolean
  children?: ReactNode
}

/**
 * The standard actions shown in a right-side workspace view header. Order is
 * a product rule: page operations, contextual chat, then any explicit refresh.
 * The shared header reads the question from these props and publishes it to
 * the panel header. Standalone views retain the same assistance inline.
 */
export function WorkspaceViewActions({
  workspacePath,
  message,
  onAsk,
  onRefresh,
  refreshing = false,
  refreshLabel = 'Refresh view',
  hideRefresh = false,
  children,
}: WorkspaceViewActionsProps) {
  const hosted = useWorkspacePanelHosted()
  return (
    <div className="flex shrink-0 items-center gap-2">
      {children}
      {!hosted && <AskAIButton workspacePath={workspacePath} message={message} onAsk={onAsk} />}
      {!hideRefresh && <WorkspaceViewIconButton label={refreshLabel} onClick={onRefresh} disabled={refreshing} spinning={refreshing} />}
    </div>
  )
}
