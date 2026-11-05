import { useProductSettingsMenu } from '../workspace/ProductSettingsMenu'
import { ProductNavigationDescription, ProductNavigationLabel, useProductNavigationSidebar } from '../workspace/ProductTopBar'
import { Bell } from 'lucide-react'
import { Tooltip, TooltipContent, TooltipTrigger } from '../ui/tooltip'
import { useAuthStore } from '../../stores/useAuthStore'
import { useAppStore } from '../../stores/useAppStore'
import { openWorkspaceSettings } from '../../utils/workspaceSettingsNavigation'

/** Account menu entry for Settings › Notifications, for every signed-in person (owner, 5 Nov). */
export default function NotificationsControl() {
  const sidebar = useProductNavigationSidebar()
  const inSettings = useProductSettingsMenu()
  const user = useAuthStore(state => state.user)
  const active = useAppStore(state => state.adminPage === 'notifications')
  if (!user) return null
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          role={inSettings ? 'menuitem' : undefined}
          data-product-navigation-action
          onClick={() => openWorkspaceSettings('notifications')}
          aria-label="Notifications"
          aria-pressed={active}
          className={`relative rounded-md p-1.5 transition-colors ${active ? 'bg-primary/10 text-primary' : 'text-muted-foreground hover:bg-muted hover:text-foreground'}`}
        >
          <Bell className="h-4 w-4 shrink-0" />
          <ProductNavigationLabel>Notifications</ProductNavigationLabel>
          <ProductNavigationDescription>When an agent finishes</ProductNavigationDescription>
        </button>
      </TooltipTrigger>
      <TooltipContent side={sidebar ? 'right' : 'bottom'}>Notifications</TooltipContent>
    </Tooltip>
  )
}
