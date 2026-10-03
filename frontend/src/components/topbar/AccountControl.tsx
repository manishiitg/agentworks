import { ProductNavigationLabel, useProductNavigationSidebar } from '../workspace/ProductTopBar'
import { lazy, Suspense, useEffect, useRef, useState } from 'react'
import { Activity, ArrowLeft, HelpCircle, Keyboard, KeyRound, LogOut } from 'lucide-react'
import { Tooltip, TooltipContent, TooltipTrigger } from '../ui/tooltip'
import { useAuthStore } from '../../stores/useAuthStore'
import NotificationsControl from './NotificationsControl'
import ChangePasswordDialog from './ChangePasswordDialog'
import { APP_VERSION } from '../../version'

const RuntimeHealthControl = lazy(() => import('./RuntimeHealthControl'))

/**
 * AccountControl - the signed-in user's avatar (their initial) which opens a
 * small account menu for hosted and local installations. Same outside-click /
 * Escape behaviour as IconPopover; not reusing it because the trigger here
 * is the round avatar itself rather than a padded icon button.
 */
interface AccountControlProps {
  onOpenWalkthrough?: () => void
  onOpenShortcuts?: () => void
}

export default function AccountControl({ onOpenWalkthrough, onOpenShortcuts }: AccountControlProps) {
  const sidebar = useProductNavigationSidebar()
  const { user, logout, isMultiUserMode } = useAuthStore()
  const [open, setOpen] = useState(false)
  const [showRuntimeHealth, setShowRuntimeHealth] = useState(false)
  const [changingPassword, setChangingPassword] = useState(false)
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const onMouseDown = (event: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) setOpen(false)
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onMouseDown)
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('mousedown', onMouseDown)
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [open])

  if (!user) return null

  const displayName = isMultiUserMode ? (user.username || user.email || 'User') : 'Local account'
  const initial = displayName.trim().charAt(0).toUpperCase() || '?'
  const itemClass =
    'w-full flex items-center gap-2 px-2 py-1.5 rounded-md text-sm text-left text-gray-700 dark:text-gray-200 hover:bg-gray-100 dark:hover:bg-gray-700 transition-colors'

  return (
    <div ref={containerRef} className="relative">
      <Tooltip>
        <TooltipTrigger asChild>
          <button
            type="button"
            onClick={() => { setShowRuntimeHealth(false); setOpen((prev) => !prev) }}
            aria-label={`Account: ${displayName}`}
            aria-haspopup="menu"
            aria-expanded={open}
            className={`${sidebar ? 'h-9 w-full justify-center rounded-lg hover:bg-secondary' : 'w-7 h-7 rounded-full justify-center bg-primary/15 hover:bg-primary/25'} flex items-center text-xs font-semibold text-primary select-none transition-colors ${open ? 'ring-2 ring-primary/40' : ''}`}
          >
            <span className="grid h-7 w-7 shrink-0 place-items-center rounded-full bg-primary/15">{initial}</span>
            <ProductNavigationLabel>{displayName}</ProductNavigationLabel>
          </button>
        </TooltipTrigger>
        <TooltipContent side={sidebar ? 'right' : 'bottom'}>{displayName}</TooltipContent>
      </Tooltip>

      {open && (
        <div
          role={showRuntimeHealth ? 'dialog' : 'menu'}
          aria-label={showRuntimeHealth ? 'Runtime health' : 'Account'}
          className={`absolute ${sidebar ? 'left-[calc(100%+12px)] bottom-0' : 'right-0 top-full mt-2'} ${showRuntimeHealth ? 'w-[28rem]' : 'w-56'} max-w-[calc(100vw-5rem)] max-h-[80vh] overflow-y-auto rounded-lg border border-gray-200 dark:border-slate-700 bg-white dark:bg-slate-800 shadow-xl z-[60] p-1.5`}
        >
          {showRuntimeHealth ? <div className="p-2">
            <button type="button" className={`${itemClass} mb-3`} onClick={() => setShowRuntimeHealth(false)}>
              <ArrowLeft className="h-4 w-4" /> Back to account
            </button>
            <Suspense fallback={<div className="p-3 text-sm text-muted-foreground">Loading runtime health…</div>}><RuntimeHealthControl embedded /></Suspense>
          </div> : <>
          <div className="px-2 py-1.5 mb-1 border-b border-gray-200 dark:border-slate-700">
            <p className="text-sm font-medium text-gray-900 dark:text-gray-100 truncate">{displayName}</p>
            {isMultiUserMode && user?.email && user.username !== user.email && (
              <p className="text-xs text-gray-500 dark:text-gray-400 truncate">{user.email}</p>
            )}
            <p className="text-xs text-gray-500 dark:text-gray-400">{`AgentWorks v${APP_VERSION}`}</p>
          </div>
          {onOpenWalkthrough && <button type="button" role="menuitem" className={itemClass} onClick={() => {
            setOpen(false)
            onOpenWalkthrough()
          }}>
            <HelpCircle className="h-4 w-4 text-muted-foreground" />
            Help &amp; walkthrough
          </button>}
          {onOpenShortcuts && <button type="button" role="menuitem" className={itemClass} onClick={() => {
            setOpen(false)
            onOpenShortcuts()
          }}>
            <Keyboard className="h-4 w-4 text-muted-foreground" />
            Keyboard shortcuts
          </button>}
          <button type="button" role="menuitem" className={itemClass} onClick={() => setShowRuntimeHealth(true)}>
            <Activity className="h-4 w-4 text-muted-foreground" /> Runtime health
          </button>
          {<NotificationsControl menuItem />}
          <div role="separator" className="my-1 border-t border-border" />
          {isMultiUserMode && <button
            type="button"
            role="menuitem"
            className={itemClass}
            onClick={() => {
              setOpen(false)
              setChangingPassword(true)
            }}
          >
            <KeyRound className="w-4 h-4 text-gray-500 dark:text-gray-400" />
            Change password
          </button>}
          { /* User management: the Users icon in the top bar (admins only), and Access → Users in a workflow. */ }
          {isMultiUserMode && <button
            type="button"
            role="menuitem"
            className={`${itemClass} hover:text-red-600 dark:hover:text-red-400`}
            onClick={() => {
              setOpen(false)
              logout()
            }}
          >
            <LogOut className="w-4 h-4" />
            Sign out
          </button>}
          </>}
        </div>
      )}

      <ChangePasswordDialog isOpen={changingPassword} onClose={() => setChangingPassword(false)} />
    </div>
  )
}
