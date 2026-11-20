import { lazy, Suspense, useRef, type ReactNode } from 'react'
import { useAppViewport } from '../../hooks/useAppViewport'
import { useNotifyFocus } from '../../hooks/useNotifyFocus'
import { AppQuickSwitcher } from './AppQuickSwitcher'
import { WorkspaceProjectsProvider } from './WorkspaceProjectsProvider'
import { AppSidebar } from './AppSidebar'
import { TeamInviteProvider, TeamInviteLine } from './TeamInviteRow'
import { WorkspaceAccountMenu } from './WorkspaceAccountMenu'
import { ProductNavigationContext, CompactProductNavigationContext } from './productNavigationContexts'
import ToastHost from '../ui/ToastHost'
import { TooltipProvider } from '../ui/tooltip'

import '../appearance/appearanceMaterials.css'
import { shownTheme, useAppearanceStore } from '../../stores/useAppearanceStore'
import { AppearanceLayer } from '../appearance/AppearanceDecorations'
import { AppearanceImage } from '../appearance/AppearanceImage'
import { AppearanceVideo } from '../appearance/AppearanceVideo'

const AppearanceBackground = lazy(() => import('../appearance/AppearanceBackground'))

/** Authenticated layout: a stable left menu beside the active page's header/content. */
export function AppShell({ children }: { children: ReactNode }) {
  const hasThemeObjects = useAppearanceStore(state => !!shownTheme(state).background_script || !!shownTheme(state).background_shader)
  const viewport = useRef<HTMLDivElement>(null)
  useAppViewport(viewport)
  useNotifyFocus()
  return <TooltipProvider><ProductNavigationContext.Provider value={false}><CompactProductNavigationContext.Provider value={true}>
    <TeamInviteProvider><WorkspaceProjectsProvider><div ref={viewport} data-app-shell data-theme-surface="app" className="flex h-dvh min-h-0 overflow-hidden bg-surface-page">
      <AppearanceVideo />
      {hasThemeObjects && <Suspense fallback={null}><AppearanceBackground /></Suspense>}
      <AppearanceImage />
      <AppSidebar />
      <div data-app-main data-theme-surface="main" className="relative flex min-h-0 min-w-0 flex-1 flex-col"><AppearanceLayer /><TeamInviteLine /><div className="relative min-h-0 min-w-0 flex-1">{children}</div></div>
      <WorkspaceAccountMenu />
      <AppQuickSwitcher />
      <ToastHost />
    </div></WorkspaceProjectsProvider></TeamInviteProvider>
  </CompactProductNavigationContext.Provider></ProductNavigationContext.Provider></TooltipProvider>
}
