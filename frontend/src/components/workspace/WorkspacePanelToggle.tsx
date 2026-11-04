import { createContext, useContext } from 'react'
import { PanelRight } from 'lucide-react'

/** The product owns visibility; shared chrome only calls its existing action. */
export const WorkspacePanelVisibilityContext = createContext<{
  open: boolean
  onToggle: () => void
} | null>(null)

export function WorkspacePanelToggle() {
  const panel = useContext(WorkspacePanelVisibilityContext)
  if (!panel) return null
  // Its own icon, not a mirror of the left menu's (owner, 5 Nov); highlighted while the panel is open.
  const label = panel.open ? 'Close workspace panel' : 'Open workspace panel'
  return <button type="button" onClick={panel.onToggle} title={label} aria-label={label} aria-expanded={panel.open}
    className={`hidden h-8 w-8 shrink-0 items-center justify-center rounded-lg ${panel.open ? 'bg-muted text-foreground' : 'text-muted-foreground'} hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring md:inline-flex`}>
    <PanelRight className="h-4 w-4" aria-hidden="true" />
  </button>
}
