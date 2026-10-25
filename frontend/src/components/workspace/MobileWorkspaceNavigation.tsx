import { createContext, useContext } from 'react'
import { MessageSquare, PanelsTopLeft } from 'lucide-react'

export type MobileWorkspacePane = 'chat' | 'workspace'
export const MobileWorkspaceNavigationContext = createContext<{
  pane: MobileWorkspacePane
  onChange: (pane: MobileWorkspacePane) => void
} | null>(null)

/** Switch presentation without unmounting the conversation or its unsent draft. */
export function MobileWorkspaceNavigation() {
  const navigation = useContext(MobileWorkspaceNavigationContext)
  if (!navigation) return null
  return <nav aria-label="Workspace panes" className="flex w-full basis-full gap-1 rounded-lg bg-muted/60 p-1 md:hidden">
    {(['chat', 'workspace'] as const).map(pane => {
      const Icon = pane === 'chat' ? MessageSquare : PanelsTopLeft
      return <button key={pane} type="button" aria-pressed={navigation.pane === pane}
        onClick={() => navigation.onChange(pane)}
        className={`flex min-h-10 min-w-0 flex-1 items-center justify-center gap-2 rounded-md px-3 text-sm font-medium focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${navigation.pane === pane ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}`}>
        <Icon className="h-4 w-4" aria-hidden="true" />{pane === 'chat' ? 'Chat' : 'Workspace'}
      </button>
    })}
  </nav>
}
