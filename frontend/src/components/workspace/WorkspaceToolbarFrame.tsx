import type { HTMLAttributes } from 'react'
import { PanelTopClose, PanelTopOpen } from 'lucide-react'
import { cn } from '../../lib/utils'
import { usePanelSwitcherStore } from '../../stores/usePanelSwitcherStore'
import { markFeatureUsed } from '../../utils/featureUsage'

const frameClass = 'inline-flex h-8 items-center divide-x divide-border rounded-lg border border-border bg-muted/60 py-0.5 shadow-sm'
const toggleClass = 'grid h-7 w-7 place-items-center text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary'

/** Shared icon group frame in the right workspace toolbar. It ends with a
 * minimize icon; minimized, every product shows only the restore icon, and
 * ⌘/Ctrl+J still opens any panel. */
export function WorkspaceToolbarFrame({ className, children, ...props }: HTMLAttributes<HTMLDivElement>) {
  const minimized = usePanelSwitcherStore(state => state.toolbarMinimized)
  const setMinimized = usePanelSwitcherStore(state => state.setToolbarMinimized)
  if (minimized) {
    return <div className={cn(frameClass, className)} {...props}>
      <button type="button" className={toggleClass} aria-label="Show toolbar" title="Show toolbar · ⌘J / Ctrl+J opens any panel" onClick={() => setMinimized(false)}>
        <PanelTopOpen className="h-4 w-4" />
      </button>
    </div>
  }
  return <div className={cn(frameClass, className)} {...props}>
    {children}
    <button type="button" className={toggleClass} aria-label="Hide toolbar" title="Hide toolbar · ⌘J / Ctrl+J opens any panel" onClick={() => { markFeatureUsed('toolbar-hide'); setMinimized(true) }}>
      <PanelTopClose className="h-4 w-4" />
    </button>
  </div>
}
