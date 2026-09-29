import { Suspense, lazy, useState } from 'react'
import { Users } from 'lucide-react'
import { Tooltip, TooltipContent, TooltipTrigger } from '../ui/tooltip'
import { useAuthStore } from '../../stores/useAuthStore'

// Loaded on open: it pulls in the admin panel and its API calls.
const UsersDialog = lazy(() => import('./UsersDialog'))

/**
 * UsersControl - top-bar icon for admins on a multi-user server: opens the
 * user directory (add people by email, roles, products). Nothing renders for
 * anyone else, and the server refuses the admin routes to non-admins anyway.
 */
export default function UsersControl() {
  const { user, isMultiUserMode } = useAuthStore()
  const [open, setOpen] = useState(false)
  if (!user || user.is_admin !== true || !isMultiUserMode) return null
  return (
    <>
      <Tooltip>
        <TooltipTrigger asChild>
          <button
            type="button"
            onClick={() => setOpen(true)}
            aria-label="Users and access"
            aria-haspopup="dialog"
            className="relative rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
          >
            <Users className="h-4 w-4" />
          </button>
        </TooltipTrigger>
        <TooltipContent side="bottom">Users &amp; access</TooltipContent>
      </Tooltip>
      {open && <Suspense fallback={null}><UsersDialog isOpen onClose={() => setOpen(false)} /></Suspense>}
    </>
  )
}
