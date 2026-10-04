import { ProductNavigationLabel, useProductNavigationSidebar } from '../workspace/ProductTopBar'
import { Users } from 'lucide-react'
import { Tooltip, TooltipContent, TooltipTrigger } from '../ui/tooltip'
import { useAuthStore } from '../../stores/useAuthStore'
import { useAppStore } from '../../stores/useAppStore'
import { useLLMStore } from '../../stores/useLLMStore'

/**
 * Shared navigation entry for administrators in local and server installs.
 * The directory API independently enforces administrator access.
 */
export default function UsersControl() {
  const sidebar = useProductNavigationSidebar()
  const user = useAuthStore(state => state.user)
  const active = useAppStore(state => state.adminPage === 'users')
  const setAdminPage = useAppStore(state => state.setAdminPage)
  if (!user || user.is_admin !== true) return null
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          data-product-navigation-action
          onClick={() => { useLLMStore.getState().setShowLLMModal(false); setAdminPage('users') }}
          aria-label="Users and access"
          aria-pressed={active}
          data-tour="global-users"
          className={`relative rounded-md p-1.5 transition-colors ${active ? 'bg-primary/10 text-primary' : 'text-muted-foreground hover:bg-muted hover:text-foreground'}`}
        >
          <Users className="h-4 w-4 shrink-0" />
          <ProductNavigationLabel>Users & access</ProductNavigationLabel>
        </button>
      </TooltipTrigger>
      <TooltipContent side={sidebar ? 'right' : 'bottom'}>Users &amp; access</TooltipContent>
    </Tooltip>
  )
}
