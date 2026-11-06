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
import { useEffect } from 'react'
import { useChatPreferences } from '../../hooks/useChatPreferences'
import { useAgentsViewStore } from '../chat/AgentCard'

/** Persistent account administration with existing permission gates. */
export function WorkspaceAccountMenu() {
  const user = useAuthStore(state => state.user)
  const multiUser = useAuthStore(state => state.isMultiUserMode)
  // The account menu is always on screen: it hands the agents choice to the Crew page and the left menu.
  const preferences = useChatPreferences()
  useEffect(() => { if (preferences.ready) useAgentsViewStore.getState().set(preferences.agentsView) }, [preferences.ready, preferences.agentsView])
  const sidebarTarget = useProductNavigationToolbarStore(state => state.sidebarSettingsTarget)
  if (!user) return null
  const name = multiUser ? user.username || user.email || 'Your account' : 'Local account'
  const trigger = <>
    <span aria-hidden="true" className="grid h-7 w-7 shrink-0 place-items-center rounded-full bg-primary/15 text-xs font-semibold text-primary">{name.trim().charAt(0).toUpperCase() || '?'}</span>
    <span className="flex min-w-0 flex-1 flex-col text-left"><span className="truncate text-xs font-medium text-foreground">{name}</span><span className="text-[11px] text-muted-foreground">Account &amp; settings</span></span>
  </>
  return <ProductSettingsMenu accountTrigger={trigger} aboveTrigger={<GetApp compact={!sidebarTarget} />}>
    <AccountControl menuOnly>
      <ProductSurfaceSwitcher menuOnly includeConnectTools
        className="!border-t-0 border-b pb-2 [&_h3]:sr-only [&>div]:!grid-cols-3 [&_button[data-product-option]]:!flex-col [&_button[data-product-option]]:!items-center [&_button[data-product-option]]:!gap-1 [&_button[data-product-option]]:!px-1 [&_button[data-product-option]]:!py-2 [&_button[data-product-option]]:!text-center [&_button[data-product-option]]:!text-[11px] [&_button[data-product-option]>span]:max-w-full [&_button[data-product-option]>span]:truncate" />
      <AgentsViewChoice />
      <p className="px-2 pb-1 pt-2 text-[11px] font-medium text-muted-foreground">Workspace</p>
      {window.electronAPI?.getDesktopConnection?.() === 'local' && <button type="button" role="menuitem" data-product-navigation-action onClick={openLocalSetup}><RotateCcw className="h-4 w-4" aria-hidden="true" /><span>Run setup again</span></button>}
      <ConnectedServersMenuItem />
      <ProvidersControl />
      <UsersControl />
      <NotificationsControl />
    </AccountControl>
  </ProductSettingsMenu>
}

/** How a Crew's agents are shown, the person's own choice saved on the server (owner, 4 Nov): in the left menu (the
 * default) or as tabs above the chat. */
function AgentsViewChoice() {
  const preferences = useChatPreferences()
  if (!preferences.ready) return null
  return <div className="flex items-center justify-between gap-2 px-2 py-1.5 text-xs">
    <span className="text-muted-foreground">Agents</span>
    <span role="radiogroup" aria-label="Show agents" className="inline-flex overflow-hidden rounded-full border border-border">
      {([['menu', 'Left menu'], ['tabs', 'Tabs']] as const).map(([value, label]) => <button key={value} type="button" role="radio" aria-checked={preferences.agentsView === value}
        onClick={() => preferences.save.mutate({ agents_view: value })}
        className={`px-2.5 py-0.5 ${preferences.agentsView === value ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:bg-muted'}`}>{label}</button>)}
    </span>
  </div>
}
