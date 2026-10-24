import { lazy, Suspense, useEffect, useRef, useState } from 'react'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import type { WalkthroughSurface } from '../../utils/onboarding'
import { workspaceSettingsPage } from '../../utils/workspaceSettingsRoutes'
import { useProductNavigationToolbarStore } from '../../stores/useProductNavigationToolbarStore'
import { ProductSettingsMenu } from './ProductSettingsMenu'
import ProvidersControl from '../topbar/ProvidersControl'
import UsersControl from '../topbar/UsersControl'
import AccountControl from '../topbar/AccountControl'
import { useAuthStore } from '../../stores/useAuthStore'

const WorkflowWalkthrough = lazy(() => import('../workflow/WorkflowWalkthrough'))

/** Shared account, preferences and Setup actions keep their existing permission gates. */
export function WorkspaceSetupMenu({ onOpenWalkthrough, onOpenShortcuts }: {
  onOpenWalkthrough?: () => void
  onOpenShortcuts?: () => void
}) {
  const owner = useRef({})
  useEffect(() => {
    useProductNavigationToolbarStore.setState({ setupActions: { owner: owner.current, onOpenWalkthrough, onOpenShortcuts } })
    return () => {
      if (useProductNavigationToolbarStore.getState().setupActions?.owner === owner.current) useProductNavigationToolbarStore.setState({ setupActions: null })
    }
  }, [onOpenWalkthrough, onOpenShortcuts])
  return null
}

/** Keep manual Help available even when a page pauses its automatic walkthrough. */
export function WorkspaceAccountMenu() {
  const actions = useProductNavigationToolbarStore(state => state.setupActions)
  const onOpenWalkthrough = actions?.onOpenWalkthrough
  const onOpenShortcuts = actions?.onOpenShortcuts
  const user = useAuthStore(state => state.user)
  const multiUser = useAuthStore(state => state.isMultiUserMode)
  const [manualHelp, setManualHelp] = useState<{ surface: WalkthroughSurface; token: number } | null>(null)
  const openHelp = () => {
    const settings = workspaceSettingsPage()
    const product = useProductSurfaceStore.getState().productSurface
    const shared: Partial<Record<typeof product, WalkthroughSurface>> = {
      knowledgebase: 'brain', 'mcp-gateway': 'vault', bullpen: 'bridge', relays: 'relays',
    }
    const globalHelp = ['/home', '/dashboards'].includes(window.location.pathname) || settings === 'users' || settings === 'mcp'
    if (!globalHelp && (settings === 'providers' || !shared[product]) && onOpenWalkthrough) {
      onOpenWalkthrough()
      return
    }
    const surface = globalHelp ? 'app' : settings === 'providers' ? 'providers' : shared[product] ?? 'app'
    setManualHelp(previous => ({ surface, token: (previous?.token ?? 0) + 1 }))
  }
  if (!user) return null
  const name = multiUser ? user.username || user.email || 'Your account' : 'Local account'
  const trigger = <>
    <span aria-hidden="true" className="grid h-7 w-7 shrink-0 place-items-center rounded-full bg-primary/15 text-xs font-semibold text-primary">{name.trim().charAt(0).toUpperCase() || '?'}</span>
    <span className="flex min-w-0 flex-1 flex-col text-left"><span className="truncate text-xs font-medium text-foreground">{name}</span><span className="text-[11px] text-muted-foreground">Account &amp; settings</span></span>
  </>
  return <><ProductSettingsMenu accountTrigger={trigger}>
    <AccountControl menuOnly onOpenWalkthrough={openHelp} onOpenShortcuts={onOpenShortcuts}>
      <p className="px-2 pb-1 pt-2 text-[11px] font-medium text-muted-foreground">Workspace</p>
      <ProvidersControl />
      <UsersControl />
    </AccountControl>
  </ProductSettingsMenu>
    {manualHelp && <Suspense fallback={<div role="status" className="fixed bottom-4 right-4 z-[10000] rounded-lg border border-border bg-popover p-3 text-sm">Opening help…</div>}>
      <WorkflowWalkthrough isOpen surface={manualHelp.surface} openToken={manualHelp.token} onClose={() => setManualHelp(null)} />
    </Suspense>}
  </>
}
