import { ProductSettingsMenu } from './ProductSettingsMenu'
import ProvidersControl from '../topbar/ProvidersControl'
import McpControl from '../topbar/McpControl'
import VaultsControl from '../topbar/VaultsControl'
import UsersControl from '../topbar/UsersControl'
import AccountControl from '../topbar/AccountControl'
import { useAuthStore } from '../../stores/useAuthStore'

/** Shared account, preferences and Setup actions keep their existing permission gates. */
export function WorkspaceSetupMenu({ onOpenWalkthrough, onOpenShortcuts }: {
  onOpenWalkthrough?: () => void
  onOpenShortcuts?: () => void
}) {
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
      <McpControl />
      <VaultsControl />
      <UsersControl />
    </AccountControl>
  </ProductSettingsMenu>
}
