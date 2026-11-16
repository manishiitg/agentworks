import { lazy, Suspense, useRef, type ReactNode } from 'react'
import { useAppViewport } from '../../hooks/useAppViewport'
import { useNotifyFocus } from '../../hooks/useNotifyFocus'
import { AppQuickSwitcher } from './AppQuickSwitcher'
import { AppSidebar } from './AppSidebar'
import { TeamInviteProvider, TeamInviteLine } from './TeamInviteRow'
import { WorkspaceAccountMenu } from './WorkspaceAccountMenu'
import { ProductNavigationContext, CompactProductNavigationContext } from './productNavigationContexts'
import ToastHost from '../ui/ToastHost'
import { TooltipProvider } from '../ui/tooltip'

import '../appearance/appearanceMaterials.css'
import { useAppearanceStore } from '../../stores/useAppearanceStore'
import { AppearanceImage } from '../appearance/AppearanceImage'
import { AppearanceVideo } from '../appearance/AppearanceVideo'

const AppearanceBackground = lazy(() => import('../appearance/AppearanceBackground'))

/** Authenticated layout: a stable left menu beside the active page's header/content. */
export function AppShell({ children }: { children: ReactNode }) {
  const hasThemeObjects = useAppearanceStore(state => !!(state.preview ?? state.current).background_script || !!(state.preview ?? state.current).background_shader || !!(state.preview ?? state.current).decorations?.length)
  const viewport = useRef<HTMLDivElement>(null)
  useAppViewport(viewport)
  useNotifyFocus()
  return <TooltipProvider><ProductNavigationContext.Provider value={false}><CompactProductNavigationContext.Provider value={true}>
    <TeamInviteProvider><div ref={viewport} data-app-shell data-theme-surface="app" className="flex h-dvh min-h-0 overflow-hidden bg-surface-page">
      <AppearanceVideo />
      {hasThemeObjects && <Suspense fallback={null}><AppearanceBackground /></Suspense>}
      <AppearanceImage />
      <AppSidebar />
      <div data-app-main data-theme-surface="main" className="relative flex min-h-0 min-w-0 flex-1 flex-col"><TeamInviteLine /><div className="relative min-h-0 min-w-0 flex-1">{children}</div></div>
      {/* Empty theme hook: custom CSS paints ambient motion here, above the solid page surfaces. */}
      <div data-app-ambient aria-hidden className="pointer-events-none fixed inset-0 z-[1] overflow-hidden" />
      <WorkspaceAccountMenu />
      <AppQuickSwitcher />
      <ToastHost />
    </div></TeamInviteProvider>
  </CompactProductNavigationContext.Provider></ProductNavigationContext.Provider></TooltipProvider>
}
