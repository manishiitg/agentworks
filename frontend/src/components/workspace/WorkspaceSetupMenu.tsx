import { useEffect, useRef } from 'react'
import { useProductNavigationToolbarStore } from '../../stores/useProductNavigationToolbarStore'
import { ProductSettingsMenu } from './ProductSettingsMenu'
import ProvidersControl from '../topbar/ProvidersControl'
import UsersControl from '../topbar/UsersControl'
import AccountControl from '../topbar/AccountControl'
import { useAuthStore } from '../../stores/useAuthStore'

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

/** Account entry belongs to the app, while help/shortcut actions belong to the page. */
export function WorkspaceAccountMenu() {
  const actions = useProductNavigationToolbarStore(state => state.setupActions)
  const onOpenWalkthrough = actions?.onOpenWalkthrough
  const onOpenShortcuts = actions?.onOpenShortcuts
  const user = useAuthStore(state => state.user)
  const multiUser = useAuthStore(state => state.isMultiUserMode)
  if (!user) return null
  const name = multiUser ? user.username || user.email || 'Your account' : 'Local account'
  const trigger = <>
    <span aria-hidden="true" className="grid h-7 w-7 shrink-0 place-items-center rounded-full bg-primary/15 text-xs font-semibold text-primary">{name.trim().charAt(0).toUpperCase() || '?'}</span>
    <span className="flex min-w-0 flex-1 flex-col text-left"><span className="truncate text-xs font-medium text-foreground">{name}</span><span className="text-[11px] text-muted-foreground">Account &amp; settings</span></span>
  </>
  return <ProductSettingsMenu accountTrigger={trigger}>
    <AccountControl menuOnly onOpenWalkthrough={onOpenWalkthrough} onOpenShortcuts={onOpenShortcuts}>
      <p className="px-2 pb-1 pt-2 text-[11px] font-medium text-muted-foreground">Workspace</p>
      <ProvidersControl />
      <UsersControl />
    </AccountControl>
  </ProductSettingsMenu>
}
