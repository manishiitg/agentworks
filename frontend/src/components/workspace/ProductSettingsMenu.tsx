import { useEffect, useId, useRef, useState, type ReactNode, type MouseEvent as ReactMouseEvent } from 'react'
import { ChevronUp, Settings, UserRound } from 'lucide-react'
import { createPortal } from 'react-dom'
import { useProductNavigationToolbarStore } from '../../stores/useProductNavigationToolbarStore'

import { SettingsMenuContext } from './productNavigationContexts'
export { useProductSettingsMenu, useProductSetupTiles } from './productNavigationContexts'

/** Existing actions remain mounted and enforce their own permissions. */
export function ProductSettingsMenu({ children, activity, accountTrigger }: { children: ReactNode; activity?: ReactNode; accountTrigger?: ReactNode }) {
  const target = useProductNavigationToolbarStore(state => state.settingsTarget)
  const sidebarTarget = useProductNavigationToolbarStore(state => state.sidebarSettingsTarget)
  const compactTarget = useProductNavigationToolbarStore(state => state.compactSettingsTarget)
  const [open, setOpen] = useState(false)
  const accountMenu = Boolean(accountTrigger)
  const menuLabel = accountMenu ? 'Account & settings' : sidebarTarget ? 'Setup' : 'Settings'
  const root = useRef<HTMLDivElement>(null)
  const trigger = useRef<HTMLButtonElement>(null)
  const id = useId()
  useEffect(() => {
    if (!open) return
    const close = (event: MouseEvent) => { if (!root.current?.contains(event.target as Node)) setOpen(false) }
    const key = (event: KeyboardEvent) => {
      if (event.key === 'Escape') { setOpen(false); trigger.current?.focus() }
    }
    document.addEventListener('mousedown', close)
    document.addEventListener('keydown', key)
    return () => { document.removeEventListener('mousedown', close); document.removeEventListener('keydown', key) }
  }, [open])
  useEffect(() => {
    const show = () => {
      if (accountMenu || sidebarTarget || !target) {
        window.dispatchEvent(new Event('close-workspace-picker'))
        setOpen(true)
      }
    }
    window.addEventListener('open-product-settings', show)
    return () => window.removeEventListener('open-product-settings', show)
  }, [target, sidebarTarget, accountMenu])
  const focusFirst = () => requestAnimationFrame(() => root.current?.querySelector<HTMLButtonElement>('[role="menuitem"], [role="menuitemcheckbox"]')?.focus())
  // Close before the action changes page/toolbar and reparents this portal.
  // Goals keeps its picker state above the portal, unlike Crew/Code selectors.
  const closePickerBeforeAction = (event: ReactMouseEvent) => {
    if (event.target instanceof Element && event.target.closest('[data-product-navigation-action]')) {
      window.dispatchEvent(new Event('close-workspace-picker'))
    }
  }
  if (target && !sidebarTarget && !accountMenu) return createPortal(
    <SettingsMenuContext.Provider value="tiles">
      {activity && <div onClickCapture={closePickerBeforeAction} className="mb-1.5 border-b border-border pb-1.5 [&_button[data-product-navigation-action]]:flex [&_button[data-product-navigation-action]]:w-full [&_button[data-product-navigation-action]]:items-center [&_button[data-product-navigation-action]]:gap-2.5 [&_button[data-product-navigation-action]]:px-2 [&_button[data-product-navigation-action]]:py-1 [&_button[data-product-navigation-action]]:text-left [&_button[data-product-navigation-action]]:text-xs">
        {activity}
      </div>}
      <h2 className="px-1 pb-1 text-[11px] font-medium text-muted-foreground">Setup</h2>
      <div onClickCapture={closePickerBeforeAction} role="menu" aria-label="Setup" className="grid grid-cols-2 gap-1 [&_button[data-product-navigation-action]]:grid [&_button[data-product-navigation-action]]:min-h-10 [&_button[data-product-navigation-action]]:grid-cols-[16px_minmax(0,1fr)] [&_button[data-product-navigation-action]]:content-center [&_button[data-product-navigation-action]]:gap-x-2 [&_button[data-product-navigation-action]]:gap-y-1 [&_button[data-product-navigation-action]]:rounded-lg [&_button[data-product-navigation-action]]:border [&_button[data-product-navigation-action]]:border-border/70 [&_button[data-product-navigation-action]]:bg-background/40 [&_button[data-product-navigation-action]]:px-2 [&_button[data-product-navigation-action]]:py-1.5 [&_button[data-product-navigation-action]]:text-left [&_button[data-product-navigation-action]]:text-foreground [&_button[data-product-navigation-action]:hover]:border-primary/40 [&_button[data-product-navigation-action]:hover]:bg-muted [&_button[data-product-navigation-action]:focus-visible]:outline-none [&_button[data-product-navigation-action]:focus-visible]:ring-2 [&_button[data-product-navigation-action]:focus-visible]:ring-ring [&_button[data-product-navigation-action]>svg]:col-start-1 [&_button[data-product-navigation-action]>svg]:row-start-1 [&_button[data-product-navigation-action]>svg]:mt-0.5 [&_button[data-product-navigation-action]>svg]:text-muted-foreground [&_[data-product-navigation-label]]:col-start-2 [&_[data-product-navigation-label]]:row-start-1 [&_[data-product-navigation-label]]:text-xs [&_[data-product-navigation-label]]:font-medium [&_[data-product-navigation-description]]:hidden [&_[data-product-navigation-description]]:col-start-2 [&_[data-product-navigation-description]]:row-start-2 [&_[data-product-navigation-description]]:text-[10px] [&_[data-product-navigation-description]]:leading-snug [&_[data-product-navigation-description]]:text-muted-foreground">
        {children}
      </div>
    </SettingsMenuContext.Provider>, target)
  const menu = <div ref={root} data-product-settings className={accountMenu && !sidebarTarget && !compactTarget ? 'fixed bottom-3 left-3 z-40 hidden md:block md:left-2' : 'relative'}>
    <button ref={trigger} data-product-settings-trigger type="button" aria-label={menuLabel} aria-haspopup="menu" aria-expanded={open} aria-controls={id}
      onClick={() => { window.dispatchEvent(new Event('close-workspace-picker')); setOpen(value => !value) }}
      onKeyDown={event => { if (event.key === 'ArrowDown' || event.key === 'ArrowUp') { event.preventDefault(); setOpen(true); focusFirst() } }}
      title={accountMenu ? 'Account & settings' : sidebarTarget ? 'AI accounts, connected tools and access' : 'Settings'} className={`${sidebarTarget ? `${accountMenu ? 'min-h-12' : 'h-10'} w-full justify-start gap-2.5 px-3 py-1.5` : 'h-8 w-8 justify-center'} flex items-center rounded-lg text-sm text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring`}>
      {accountMenu ? sidebarTarget ? accountTrigger : <UserRound className="h-4 w-4" aria-hidden="true" /> : <Settings className="h-4 w-4" aria-hidden="true" />}
      {sidebarTarget && <>{!accountMenu && <span className="flex-1 text-left">Setup</span>}<ChevronUp className={`h-3.5 w-3.5 transition-transform ${open ? 'rotate-180' : ''}`} aria-hidden="true" /></>}
    </button>
    <SettingsMenuContext.Provider value="menu">
      <div id={id} hidden={!open} role="menu" aria-label={menuLabel}
        onClickCapture={closePickerBeforeAction}
        onClick={event => { if (event.target instanceof Element && event.target.closest('[data-product-navigation-action]')) setOpen(false) }}
        onKeyDown={event => {
          if (!event.currentTarget.contains(event.target as Node) || !['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return
          event.preventDefault()
          const items = Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="menuitem"]:not(:disabled), [role="menuitemcheckbox"]:not(:disabled)'))
          const current = items.indexOf(document.activeElement as HTMLButtonElement)
          const next = event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1 : (current + (event.key === 'ArrowUp' ? -1 : 1) + items.length) % items.length
          items[next]?.focus()
        }}
        className={`absolute ${accountMenu && !sidebarTarget ? 'bottom-0 left-full ml-2 w-64' : sidebarTarget ? 'bottom-full left-0 mb-2 w-full min-w-64' : 'right-0 top-full mt-2 w-60'} z-50 max-h-[calc(100dvh-100px)] max-w-[calc(100vw-5rem)] space-y-1 overflow-auto rounded-xl border border-border bg-popover p-2 shadow-xl [&_button[data-product-navigation-action]]:flex [&_button[data-product-navigation-action]]:w-full [&_button[data-product-navigation-action]]:items-center [&_button[data-product-navigation-action]]:gap-2.5 [&_button[data-product-navigation-action]]:px-3 [&_button[data-product-navigation-action]]:py-1.5 [&_button[data-product-navigation-action]]:text-left [&_button[data-product-navigation-action]]:text-xs`}>
        {children}
        {activity && <div className="mt-1 border-t border-border pt-1">{activity}</div>}
      </div>
    </SettingsMenuContext.Provider>
  </div>
  const destination = sidebarTarget ?? (accountMenu ? compactTarget : null)
  return destination ? createPortal(menu, destination) : menu
}
