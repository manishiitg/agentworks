import { AccountAvatar } from '../ui/AccountAvatar'
import { openWorkspaceSettings } from '../../utils/workspaceSettingsNavigation'
import { openSubscriptionLimits } from '../../utils/subscriptionLimitsNavigation'
import { connectedLocations, useConnectedWorkPolling, useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import AppearanceControl from './AppearanceControl'
import { GetApp } from './GetApp'
import { AlertCircle, Settings } from 'lucide-react'
import { ProductSettingsMenu } from './ProductSettingsMenu'
import NotificationsControl from '../topbar/NotificationsControl'
import AccountControl from '../topbar/AccountControl'
import { useAuthStore } from '../../stores/useAuthStore'
import { BrainMark } from '../../products/knowledgebase/BrainMark'
import { navigationProductSurfaceIDs } from '../../products/productSurfaceConfig'
import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'
import { accountSignInLabel, useAccountSignInNeeded } from '../providers/AccountSignInBadge'

/** Persistent account administration with existing permission gates. */
export function WorkspaceAccountMenu() {
  const user = useAuthStore(state => state.user)
  const allowedProducts = useAuthStore(state => state.user?.allowed_products)
  const multiUser = useAuthStore(state => state.isMultiUserMode)
  const connections = useConnectedWorkStore(connectedLocations)
  const activeWorkspaceId = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const profiles = useWorkspaceConnectionStore(state => state.profiles)
  useConnectedWorkPolling()
  const signInNeeded = useAccountSignInNeeded(Boolean(user))
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
  const signInLabel = accountSignInLabel(signInNeeded)
  const trigger = <>
    <AccountAvatar name={loginName} accountID={connection?.user?.id || user.id} connection={connection?.id} className="h-5 w-5 text-[10px]" />
    <span className="min-w-0 flex-1 truncate text-left text-xs font-medium text-foreground" title={label}>{label}</span>
    {signInNeeded > 0 && <span data-account-sign-in-dot className="inline-flex h-4 min-w-4 shrink-0 items-center justify-center rounded-full bg-destructive px-1 text-[10px] font-semibold text-destructive-foreground" aria-hidden="true">{signInNeeded}</span>}
  </>
  return <ProductSettingsMenu accountTrigger={trigger} attention={signInLabel || undefined}>
    <AccountControl menuOnly accountLabel={label} hideVersion>
      {signInNeeded > 0 && <button type="button" role="menuitem" data-product-navigation-action onClick={openSubscriptionLimits} className="rounded-lg border border-destructive/30 bg-destructive/5 font-medium text-destructive hover:bg-destructive/10"><AlertCircle className="h-4 w-4 shrink-0" aria-hidden="true" /><span className="min-w-0 flex-1 truncate">{signInLabel}</span></button>}
      {navigationProductSurfaceIDs(allowedProducts).includes('knowledgebase') && <button type="button" role="menuitem" data-product-navigation-action onClick={() => openProductWorkspace('knowledgebase')}><BrainMark className="h-4 w-4" /><span>Brain</span></button>}
      <button type="button" role="menuitem" data-product-navigation-action onClick={() => openWorkspaceSettings('connections')}><Settings className="h-4 w-4" aria-hidden="true" /><span>Settings</span></button>
      <GetApp />
      <NotificationsControl />
      <AppearanceControl compact />
    </AccountControl>
  </ProductSettingsMenu>
}
