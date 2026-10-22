import React from 'react'
import { useWorkspacePanelHeaderSlots } from '../workspace/WorkspacePanelHeaderSlots'

interface InspectorShellProps {
  /** The shell box's classes. */
  className: string
  /** The view's own header row content (title, controls). */
  header: React.ReactNode
  /** Extra classes for the header row wrapper, per view. */
  headerClassName?: string
  children: React.ReactNode
}

/**
 * The right-pane frame shared by the inspector views (Costs, Execution
 * Logs). The view supplies only its header and body.
 */
export default function InspectorShell({
  className,
  header,
  headerClassName = '',
  children,
}: InspectorShellProps) {
  const slots = useWorkspacePanelHeaderSlots()
  return (
    <div className={className}>
      <div className={slots?.heading ? 'contents' : headerClassName}>
        {header}
      </div>

      {children}
    </div>
  )
}
