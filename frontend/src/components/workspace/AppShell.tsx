import { useRef, type ReactNode } from 'react'
import { useAppViewport } from '../../hooks/useAppViewport'
import { AppQuickSwitcher } from './AppQuickSwitcher'
import { AppSidebar } from './AppSidebar'
import { WorkspaceAccountMenu } from './WorkspaceAccountMenu'
import { ProductNavigationContext, CompactProductNavigationContext } from './productNavigationContexts'
import ToastHost from '../ui/ToastHost'
import { TooltipProvider } from '../ui/tooltip'

/** Authenticated layout: a stable left menu beside the active page's header/content. */
export function AppShell({ children }: { children: ReactNode }) {
  const viewport = useRef<HTMLDivElement>(null)
  useAppViewport(viewport)
  return <TooltipProvider><ProductNavigationContext.Provider value={false}><CompactProductNavigationContext.Provider value={true}>
    <div ref={viewport} data-app-shell className="flex h-dvh min-h-0 overflow-hidden bg-background">
      <AppSidebar />
      <div data-app-main className="relative min-h-0 min-w-0 flex-1">{children}</div>
      <WorkspaceAccountMenu />
      <AppQuickSwitcher />
      <ToastHost />
    </div>
  </CompactProductNavigationContext.Provider></ProductNavigationContext.Provider></TooltipProvider>
}
