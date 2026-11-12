import { connectedLocations, useConnectedWorkPolling, useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import AppearanceControl from './AppearanceControl'
import { GetApp } from './GetApp'
import { RotateCcw } from 'lucide-react'
import { openLocalSetup } from '../../utils/homeNavigation'
import { ConnectedServersMenuItem } from './ConnectedServersMenuItem'
import { ProductSettingsMenu } from './ProductSettingsMenu'
import ProvidersControl from '../topbar/ProvidersControl'
import UsersControl from '../topbar/UsersControl'
import NotificationsControl from '../topbar/NotificationsControl'
import AccountControl from '../topbar/AccountControl'
import { useAuthStore } from '../../stores/useAuthStore'
import { useProductNavigationToolbarStore } from '../../stores/useProductNavigationToolbarStore'
import { ProductSurfaceSwitcher } from '../ProductSurfaceSwitcher'

/** Persistent account administration with existing permission gates. */
export function WorkspaceAccountMenu() {
  const user = useAuthStore(state => state.user)
  const multiUser = useAuthStore(state => state.isMultiUserMode)
  const sidebarTarget = useProductNavigationToolbarStore(state => state.sidebarSettingsTarget)
  const connections = useConnectedWorkStore(connectedLocations)
  const activeWorkspaceId = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const profiles = useWorkspaceConnectionStore(state => state.profiles)
  useConnectedWorkPolling()
  if (!user) return null
  const name = multiUser ? user.username || user.email || 'Your account' : 'Local account'
  // Use the login returned for this exact connection, never a provider account.
  // A local window can have several saved networks; show the first plus a count.
  const networks = connections.filter(connection => connection.id !== 'local')
  const connection = activeWorkspaceId === 'local' ? networks[0] : networks.find(item => item.id === activeWorkspaceId)
  const networkName = connection?.name || (activeWorkspaceId !== 'local' ? profiles.find(profile => profile.id === activeWorkspaceId)?.name : '')
  const loginName = connection?.user?.username || connection?.user?.email || name
  const networkCount = activeWorkspaceId === 'local' && networks.length > 1 ? ` +${networks.length - 1}` : ''
  const label = networkName ? `${loginName} · ${networkName}${networkCount}` : name
  const trigger = <>
    <span aria-hidden="true" className="grid h-5 w-5 shrink-0 place-items-center rounded-full bg-primary/15 text-[10px] font-semibold text-primary">{loginName.trim().charAt(0).toUpperCase() || '?'}</span>
    <span className="min-w-0 flex-1 truncate text-left text-xs font-medium text-foreground" title={label}>{label}</span>
  </>
  return <ProductSettingsMenu accountTrigger={trigger} aboveTrigger={<GetApp compact={!sidebarTarget} />}>
    <AccountControl menuOnly>
      <ProductSurfaceSwitcher menuOnly includeConnectTools
        className="!border-t-0 border-b pb-2 [&_h3]:sr-only [&>div]:!grid-cols-3 [&_button[data-product-option]]:!flex-col [&_button[data-product-option]]:!items-center [&_button[data-product-option]]:!gap-1 [&_button[data-product-option]]:!px-1 [&_button[data-product-option]]:!py-2 [&_button[data-product-option]]:!text-center [&_button[data-product-option]]:!text-[11px] [&_button[data-product-option]>span]:max-w-full [&_button[data-product-option]>span]:truncate" />
      <p className="px-2 pb-1 pt-2 text-[11px] font-medium text-muted-foreground">Workspace</p>
      {window.electronAPI?.getDesktopConnection?.() === 'local' && <button type="button" role="menuitem" data-product-navigation-action onClick={openLocalSetup}><RotateCcw className="h-4 w-4" aria-hidden="true" /><span>Run setup again</span></button>}
      <ConnectedServersMenuItem />
      <ProvidersControl />
      <UsersControl />
      <NotificationsControl />
      <AppearanceControl />
    </AccountControl>
  </ProductSettingsMenu>
}
