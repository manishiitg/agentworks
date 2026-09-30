import type { HTMLAttributes } from 'react'

/** Shared AgentWorks toolbar row used above chat/workspace split surfaces. */
export function WorkspaceTopToolbar({ className = '', ...props }: HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      {...props}
      data-terminal-focus-chrome="toolbar"
      // z-30, not z-10: this wrapper establishes its own stacking context
      // (position + z-index), so a dropdown opened from it (e.g. Setup,
      // its own z-50) only outranks OTHER content within this context, not
      // canvas-level floating controls outside it (WorkflowCanvas's top-right
      // icon group is z-20, sitting in a sibling context) -- confirmed live
      // via elementFromPoint at the overlap point landing on the icon group's
      // "Ask AI" button despite the open menu's z-50. z-30 clears every
      // canvas-level control (max z-20 today) while staying below true
      // modals (VariablesSidebar/FilePreviewModal use z-50/z-[60]).
      className={`relative z-30 flex min-h-10 min-w-0 flex-nowrap items-center gap-3 overflow-visible border-b border-border bg-background px-3 py-1.5 ${className}`}
    />
  )
}
