import { Users } from 'lucide-react'
import { Tooltip, TooltipContent, TooltipTrigger } from '../ui/tooltip'
import { useAuthStore } from '../../stores/useAuthStore'
import { useAppStore } from '../../stores/useAppStore'
import { useLLMStore } from '../../stores/useLLMStore'

/**
 * UsersControl - top-bar icon for admins on a multi-user server: opens the
 * Users & access full page (add people by email, roles, products). Nothing
 * renders for anyone else, and the server refuses the admin routes to
 * non-admins anyway.
 */
export default function UsersControl() {
  const { user, isMultiUserMode } = useAuthStore()
  const active = useAppStore(state => state.adminPage === 'users')
  const setAdminPage = useAppStore(state => state.setAdminPage)
  if (!user || user.is_admin !== true || !isMultiUserMode) return null
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          onClick={() => { useLLMStore.getState().setShowLLMModal(false); setAdminPage('users') }}
          aria-label="Users and access"
          aria-pressed={active}
          data-tour="global-users"
          className={`relative rounded-md p-1.5 transition-colors ${active ? 'bg-primary/10 text-primary' : 'text-muted-foreground hover:bg-muted hover:text-foreground'}`}
        >
          <Users className="h-4 w-4" />
        </button>
      </TooltipTrigger>
      <TooltipContent side="bottom">Users &amp; access</TooltipContent>
    </Tooltip>
  )
}
