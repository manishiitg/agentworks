import { createContext, useContext } from 'react'
import { PanelRightClose, PanelRightOpen } from 'lucide-react'

/** The product owns visibility; shared chrome only calls its existing action. */
export const WorkspacePanelVisibilityContext = createContext<{
  open: boolean
  onToggle: () => void
} | null>(null)

export function WorkspacePanelToggle() {
  const panel = useContext(WorkspacePanelVisibilityContext)
  if (!panel) return null
  const Icon = panel.open ? PanelRightClose : PanelRightOpen
  const label = panel.open ? 'Close workspace panel' : 'Open workspace panel'
  return <button type="button" onClick={panel.onToggle} title={label} aria-label={label} aria-expanded={panel.open}
    className="hidden h-8 w-8 shrink-0 items-center justify-center rounded-lg text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring md:inline-flex">
    <Icon className="h-4 w-4" aria-hidden="true" />
  </button>
}
