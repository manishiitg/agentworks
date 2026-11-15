import { lazy, Suspense, useRef, type ReactNode } from 'react'
import { useAppViewport } from '../../hooks/useAppViewport'
import { useNotifyFocus } from '../../hooks/useNotifyFocus'
import { AppQuickSwitcher } from './AppQuickSwitcher'
import { AppSidebar } from './AppSidebar'
import { TeamInviteProvider } from './TeamInviteRow'
import { WorkspaceAccountMenu } from './WorkspaceAccountMenu'
import { ProductNavigationContext, CompactProductNavigationContext } from './productNavigationContexts'
import ToastHost from '../ui/ToastHost'
import { TooltipProvider } from '../ui/tooltip'

import { useAppearanceStore } from '../../stores/useAppearanceStore'
const AppearanceBackground = lazy(() => import('../appearance/AppearanceBackground'))

/** Authenticated layout: a stable left menu beside the active page's header/content. */
export function AppShell({ children }: { children: ReactNode }) {
  const scene = useAppearanceStore(state => state.backgroundScene)
  const scope = useAppearanceStore(state => state.scope)
  const viewport = useRef<HTMLDivElement>(null)
  useAppViewport(viewport)
  useNotifyFocus()
  return <TooltipProvider><ProductNavigationContext.Provider value={false}><CompactProductNavigationContext.Provider value={true}>
    <TeamInviteProvider><div ref={viewport} data-app-shell className="flex h-dvh min-h-0 overflow-hidden bg-surface-page">
      {scene === 'neon-orbit' && <Suspense fallback={null}><AppearanceBackground key={scope} /></Suspense>}
      <AppSidebar />
      <div data-app-main className="relative min-h-0 min-w-0 flex-1">{children}</div>
      <WorkspaceAccountMenu />
      <AppQuickSwitcher />
      <ToastHost />
    </div></TeamInviteProvider>
  </CompactProductNavigationContext.Provider></ProductNavigationContext.Provider></TooltipProvider>
}
