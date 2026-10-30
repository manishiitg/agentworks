import { createContext, useContext } from 'react'
import { PanelRightClose, PanelRightOpen } from 'lucide-react'

export type MobileWorkspacePane = 'chat' | 'workspace'
export const MobileWorkspaceNavigationContext = createContext<{
  pane: MobileWorkspacePane
  onChange: (pane: MobileWorkspacePane) => void
} | null>(null)

/** The phone header keeps chat primary; workspace navigation is one menu action. */
export function MobileWorkspaceNavigation() {
  const navigation = useContext(MobileWorkspaceNavigationContext)
  if (!navigation) return null
  const inWorkspace = navigation.pane === 'workspace'
  const label = inWorkspace ? 'Close workspace panel' : 'Open workspace panel'
  const Icon = inWorkspace ? PanelRightClose : PanelRightOpen
  return <nav aria-label="Workspace navigation" className="ml-auto flex shrink-0 items-center md:hidden">
    <button type="button" aria-label={label} title={label} aria-expanded={inWorkspace}
      onClick={() => navigation.onChange(inWorkspace ? 'chat' : 'workspace')}
      className="inline-flex h-11 w-11 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
      <Icon className="h-5 w-5" aria-hidden="true" />
    </button>
  </nav>
}
