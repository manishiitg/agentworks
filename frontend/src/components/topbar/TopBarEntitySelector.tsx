import { useCompactProductNavigation, useProductNavigationSidebar } from '../workspace/ProductTopBar'
import { useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { LayoutGrid, Plus } from 'lucide-react'
import { ProductPickerMenu } from '../workspace/ProductPickerMenu'
import { Tooltip, TooltipContent, TooltipTrigger } from '../ui/tooltip'
import { useProductNavigationToolbarStore } from '../../stores/useProductNavigationToolbarStore'

type TopBarEntitySelectorProps = {
  label?: string
  placeholder: string
  active?: boolean
  title?: string
  open: boolean
  onToggle: () => void
  onClose: () => void
  onAdd?: () => void
  addLabel?: string
  addTitle?: string
  addDisabled?: boolean
  addTestId?: string
  badge?: ReactNode
  leading?: ReactNode
  compactOnNarrow?: boolean
  middleControl?: ReactNode
  children: ReactNode
  dataTour?: string
  testId?: string
}

/** Shared AgentWorks top-bar selector shell for automations and product projects. */
export function TopBarEntitySelector({
  label,
  placeholder,
  active = Boolean(label),
  title,
  open,
  onToggle,
  onClose,
  onAdd,
  addLabel,
  addTitle,
  addDisabled = false,
  addTestId,
  badge,
  leading,
  compactOnNarrow = false,
  middleControl,
  children,
  dataTour,
  testId,
}: TopBarEntitySelectorProps) {
  const sidebar = useProductNavigationSidebar()
  const compact = useCompactProductNavigation()
  const trigger = useProductNavigationToolbarStore(state => state.workspaceTrigger)
  const toolbar = useProductNavigationToolbarStore(state => state.target)
  const toolbarKind = useProductNavigationToolbarStore(state => state.targetKind)
  const sidebarSettings = useProductNavigationToolbarStore(state => state.sidebarSettingsTarget)
  const titleNavigation = compact && !!trigger && !!toolbar && (toolbarKind !== 'page' || trigger.hasAttribute('data-product-header-picker'))
  const rootRef = useRef<HTMLDivElement>(null)
  const panelRef = useRef<HTMLDivElement>(null)
  const [panelPosition, setPanelPosition] = useState({ left: 68, top: 12 })
  const ownSetupSlot = useRef<HTMLDivElement | null>(null)
  const registerSetup = useCallback((node: HTMLDivElement | null) => {
    const previous = ownSetupSlot.current
    ownSetupSlot.current = node
    if (node || useProductNavigationToolbarStore.getState().settingsTarget === previous) {
      useProductNavigationToolbarStore.setState({ settingsTarget: node })
    }
  }, [])
  useEffect(() => {
    if (!compact) return
    const show = () => { if (!sidebarSettings && !open) onToggle() }
    const close = () => onClose()
    window.addEventListener('open-product-settings', show)
    window.addEventListener('close-workspace-picker', close)
    return () => {
      window.removeEventListener('open-product-settings', show)
      window.removeEventListener('close-workspace-picker', close)
    }
  }, [compact, open, onToggle, onClose, sidebarSettings])

  const selectionHint = toolbarKind === 'page' ? 'Switch project' : placeholder === 'Select Automation' ? 'Choose automation' : 'Choose workspace'
  const sidebarTitle = label ? `${selectionHint} · ${label}` : selectionHint

  useLayoutEffect(() => {
    if ((!sidebar && !titleNavigation) || !open) return
    const positionPanel = () => {
      const anchor = (titleNavigation ? trigger : rootRef.current)?.getBoundingClientRect()
      const panel = panelRef.current?.getBoundingClientRect()
      if (!anchor || !panel) return
      const left = Math.max(12, Math.min(titleNavigation ? anchor.left : anchor.right + 12, window.innerWidth - panel.width - 12))
      const top = Math.max(12, Math.min(titleNavigation ? anchor.bottom + 8 : anchor.top, window.innerHeight - panel.height - 12))
      setPanelPosition(previous => previous.left === left && previous.top === top ? previous : { left, top })
    }
    positionPanel()
    window.addEventListener('resize', positionPanel)
    return () => window.removeEventListener('resize', positionPanel)
  }, [sidebar, titleNavigation, trigger, open, children])

  useEffect(() => {
    if (!titleNavigation) return
    const show = (event: Event) => {
      if (!open) onToggle()
      if ((event as CustomEvent).detail?.focusFirst) requestAnimationFrame(() => panelRef.current?.querySelector<HTMLElement>('button:not(:disabled), a[href]')?.focus())
    }
    window.addEventListener('open-workspace-picker', show)
    useProductNavigationToolbarStore.setState({ pickerOpen: open })
    return () => {
      window.removeEventListener('open-workspace-picker', show)
      useProductNavigationToolbarStore.setState({ pickerOpen: false })
    }
  }, [titleNavigation, open, onToggle])

  useEffect(() => {
    if (!open) return
    const onMouseDown = (event: MouseEvent) => {
        if (!rootRef.current?.contains(event.target as Node) && !trigger?.contains(event.target as Node)) onClose()
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') { onClose(); trigger?.focus() }
      if (event.defaultPrevented || !(event.target instanceof Element) || !rootRef.current?.contains(event.target) || !event.target.closest('[data-picker-setup]')) return
      if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return
      event.preventDefault()
      const items = Array.from(rootRef.current.querySelectorAll<HTMLButtonElement>('[data-picker-setup] [role="menuitem"]:not(:disabled)'))
      const current = items.indexOf(document.activeElement as HTMLButtonElement)
      const next = event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1 : (current + (event.key === 'ArrowUp' ? -1 : 1) + items.length) % items.length
      items[next]?.focus()
    }
    const selectSetup = (event: MouseEvent) => {
      if (event.target instanceof Element && rootRef.current?.contains(event.target) && event.target.closest('[data-picker-setup] [data-product-navigation-action]')) onClose()
    }
    document.addEventListener('click', selectSetup)
    document.addEventListener('mousedown', onMouseDown)
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('click', selectSetup)
      document.removeEventListener('mousedown', onMouseDown)
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [onClose, open, trigger])

  return (
    <div ref={rootRef} data-product-settings className={`relative flex min-w-0 items-center ${sidebar ? 'w-full' : ''}`}>
      <div
        data-tour={dataTour}
        data-testid={testId}
        className={`${titleNavigation ? 'hidden' : 'flex'} min-w-0 items-center ${sidebar ? 'w-full' : compact ? '' : 'overflow-hidden rounded-md border border-gray-200 bg-white dark:border-gray-600 dark:bg-gray-800'}`}
      >
        <Tooltip>
          <TooltipTrigger asChild>
          <button
            type="button"
            data-product-settings-trigger
            onClick={onToggle}
            aria-haspopup="menu"
            aria-expanded={open}
            aria-label={compact && toolbarKind === 'page' ? 'Switch workspace' : label || placeholder}
            className={`${compact ? `h-8 max-w-[220px] justify-center rounded-lg px-2 text-muted-foreground hover:bg-secondary hover:text-foreground ${open ? 'bg-secondary text-foreground' : ''}` : 'px-3 py-1 hover:bg-gray-100 dark:hover:bg-slate-700'} flex min-w-0 items-center gap-2 transition-colors`}
            title={compact ? sidebarTitle : title}
          >
            {compact ? <LayoutGrid className="h-4 w-4 shrink-0" /> : leading ?? <div className={`h-2 w-2 shrink-0 rounded-full ${active ? 'bg-green-500' : 'bg-gray-400'}`} />}
            <span className={`${compact && toolbarKind === 'page' ? 'sr-only' : compact ? 'block' : compactOnNarrow ? 'hidden min-[1180px]:block' : 'block'} max-w-[190px] truncate whitespace-nowrap text-sm font-medium ${active ? 'text-gray-700 dark:text-gray-300' : 'text-gray-500 dark:text-gray-400'}`}>
              {label || placeholder}
            </span>
            {!compact && badge}
          </button>
          </TooltipTrigger>
          <TooltipContent side={sidebar ? 'right' : 'bottom'}>{compact ? sidebarTitle : title || label || placeholder}</TooltipContent>
        </Tooltip>
        {middleControl}
        {onAdd && addLabel && <button
          type="button"
          data-testid={addTestId}
          aria-label={addLabel}
          onClick={() => { onClose(); onAdd() }}
          disabled={addDisabled}
          title={addTitle || addLabel}
          className={`${compact ? 'hidden' : ''} border-l border-gray-200 px-2 py-1 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-700 disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-600 dark:text-gray-400 dark:hover:bg-slate-700 dark:hover:text-gray-200`}
        >
          <Plus className="h-3 w-3" />
        </button>}
      </div>
      {(open || compact) && (
        <div ref={panelRef} hidden={!open} style={sidebar || titleNavigation ? panelPosition : undefined} className={`preset-dropdown ${sidebar || titleNavigation ? 'fixed w-[420px] max-h-[calc(100dvh-64px)] overflow-hidden' : compact ? 'absolute right-0 top-full mt-1 w-[420px] max-h-[calc(100dvh-64px)] overflow-hidden' : 'absolute left-0 top-full mt-1 w-64'} ${compact ? (open ? 'flex flex-col' : 'hidden') : ''} z-50 max-w-[calc(100vw-24px)] rounded-lg border border-border bg-popover text-popover-foreground shadow-xl`}>
          {compact ? <ProductPickerMenu title={placeholder === 'Select Automation' ? 'Automations' : placeholder === 'Select Flow' ? 'Flows' : 'Workspaces'}
            footer={!sidebarSettings && <section data-picker-setup className="shrink-0 border-t border-border p-1.5"><div ref={registerSetup} data-picker-setup-slot /></section>}>
            {children}
          </ProductPickerMenu> : children}
        </div>
      )}
    </div>
  )
}
