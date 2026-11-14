import { openWorkspaceSettings } from '../../utils/workspaceSettingsNavigation'
import { connectedLocations, useConnectedWorkPolling, useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import AppearanceControl from './AppearanceControl'
import { GetApp } from './GetApp'
import { Settings } from 'lucide-react'
import { ProductSettingsMenu } from './ProductSettingsMenu'
import NotificationsControl from '../topbar/NotificationsControl'
import AccountControl from '../topbar/AccountControl'
import { useAuthStore } from '../../stores/useAuthStore'
import { useProductNavigationToolbarStore } from '../../stores/useProductNavigationToolbarStore'
import { BrainMark } from '../../products/knowledgebase/BrainMark'
import { navigationProductSurfaceIDs } from '../../products/productSurfaceConfig'
import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'

/** Persistent account administration with existing permission gates. */
export function WorkspaceAccountMenu() {
  const user = useAuthStore(state => state.user)
  const allowedProducts = useAuthStore(state => state.user?.allowed_products)
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
    <AccountControl menuOnly accountLabel={label} hideVersion>
      {navigationProductSurfaceIDs(allowedProducts).includes('knowledgebase') && <button type="button" role="menuitem" data-product-navigation-action onClick={() => openProductWorkspace('knowledgebase')}><BrainMark className="h-4 w-4" /><span>Brain</span></button>}
      <button type="button" role="menuitem" data-product-navigation-action onClick={() => openWorkspaceSettings('connections')}><Settings className="h-4 w-4" aria-hidden="true" /><span>Settings</span></button>
      <NotificationsControl />
      <AppearanceControl compact />
    </AccountControl>
  </ProductSettingsMenu>
}
