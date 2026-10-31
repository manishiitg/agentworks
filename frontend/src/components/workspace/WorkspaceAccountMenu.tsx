import { ConnectedServersMenuItem } from './ConnectedServersMenuItem'
import { ProductSettingsMenu } from './ProductSettingsMenu'
import ProvidersControl from '../topbar/ProvidersControl'
import UsersControl from '../topbar/UsersControl'
import AccountControl from '../topbar/AccountControl'
import { useAuthStore } from '../../stores/useAuthStore'
import { useProductNavigationToolbarStore } from '../../stores/useProductNavigationToolbarStore'
import { ProductSurfaceSwitcher } from '../ProductSurfaceSwitcher'

/** Persistent account administration with existing permission gates. */
export function WorkspaceAccountMenu() {
  const user = useAuthStore(state => state.user)
  const multiUser = useAuthStore(state => state.isMultiUserMode)
  const sidebarTarget = useProductNavigationToolbarStore(state => state.sidebarSettingsTarget)
  if (!user) return null
  const name = multiUser ? user.username || user.email || 'Your account' : 'Local account'
  const trigger = <>
    <span aria-hidden="true" className="grid h-7 w-7 shrink-0 place-items-center rounded-full bg-primary/15 text-xs font-semibold text-primary">{name.trim().charAt(0).toUpperCase() || '?'}</span>
    <span className="flex min-w-0 flex-1 flex-col text-left"><span className="truncate text-xs font-medium text-foreground">{name}</span><span className="text-[11px] text-muted-foreground">Account &amp; settings</span></span>
  </>
  return <ProductSettingsMenu accountTrigger={trigger}>
    <AccountControl menuOnly>
      {!sidebarTarget && <ProductSurfaceSwitcher menuOnly includeConnectTools
        className="!border-t-0 border-b pb-2 [&_h3]:sr-only [&>div]:!grid-cols-3 [&_button[data-product-option]]:!flex-col [&_button[data-product-option]]:!items-center [&_button[data-product-option]]:!gap-1 [&_button[data-product-option]]:!px-1 [&_button[data-product-option]]:!py-2 [&_button[data-product-option]]:!text-center [&_button[data-product-option]]:!text-[11px] [&_button[data-product-option]>span]:max-w-full [&_button[data-product-option]>span]:truncate" />}
      <p className="px-2 pb-1 pt-2 text-[11px] font-medium text-muted-foreground">Workspace</p>
      <ConnectedServersMenuItem />
      <ProvidersControl />
      <UsersControl />
    </AccountControl>
  </ProductSettingsMenu>
}
