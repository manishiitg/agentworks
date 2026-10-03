import { useProductNavigationSidebar } from '../workspace/ProductTopBar'
import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { LayoutGrid, Plus } from 'lucide-react'
import { Tooltip, TooltipContent, TooltipTrigger } from '../ui/tooltip'

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
  const rootRef = useRef<HTMLDivElement>(null)
  const panelRef = useRef<HTMLDivElement>(null)
  const [panelPosition, setPanelPosition] = useState({ left: 68, top: 12 })
  const selectionHint = placeholder === 'Select Automation' ? 'Choose automation' : 'Choose workspace'
  const sidebarTitle = label ? `${selectionHint} · ${label}` : selectionHint

  useLayoutEffect(() => {
    if (!sidebar || !open) return
    const positionPanel = () => {
      const trigger = rootRef.current?.getBoundingClientRect()
      const panel = panelRef.current?.getBoundingClientRect()
      if (!trigger || !panel) return
      const left = Math.max(12, Math.min(trigger.right + 12, window.innerWidth - panel.width - 12))
      const top = Math.max(12, Math.min(trigger.top, window.innerHeight - panel.height - 12))
      setPanelPosition(previous => previous.left === left && previous.top === top ? previous : { left, top })
    }
    positionPanel()
    window.addEventListener('resize', positionPanel)
    return () => window.removeEventListener('resize', positionPanel)
  }, [sidebar, open, children])

  useEffect(() => {
    if (!open) return
    const onMouseDown = (event: MouseEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) onClose()
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose()
    }
    document.addEventListener('mousedown', onMouseDown)
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('mousedown', onMouseDown)
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [onClose, open])

  return (
    <div ref={rootRef} className={`relative flex min-w-0 items-center ${sidebar ? 'w-full' : ''}`}>
      <div
        data-tour={dataTour}
        data-testid={testId}
        className={`flex min-w-0 items-center ${sidebar ? 'w-full' : 'overflow-hidden rounded-md border border-gray-200 bg-white dark:border-gray-600 dark:bg-gray-800'}`}
      >
        <Tooltip>
          <TooltipTrigger asChild>
          <button
            type="button"
            onClick={onToggle}
            aria-haspopup="menu"
            aria-expanded={open}
            aria-label={label || placeholder}
            className={`${sidebar ? `h-9 w-full justify-center rounded-lg text-muted-foreground hover:bg-secondary hover:text-foreground ${open ? 'bg-secondary text-foreground' : ''}` : 'px-3 py-1 hover:bg-gray-100 dark:hover:bg-slate-700'} flex min-w-0 items-center gap-2 transition-colors`}
            title={sidebar ? sidebarTitle : title}
          >
            {sidebar ? <LayoutGrid className="h-4 w-4 shrink-0" /> : leading ?? <div className={`h-2 w-2 shrink-0 rounded-full ${active ? 'bg-green-500' : 'bg-gray-400'}`} />}
            <span className={`${sidebar ? 'hidden' : compactOnNarrow ? 'hidden min-[1180px]:block' : 'block'} max-w-[190px] truncate whitespace-nowrap text-sm font-medium ${active ? 'text-gray-700 dark:text-gray-300' : 'text-gray-500 dark:text-gray-400'}`}>
              {label || placeholder}
            </span>
            {!sidebar && badge}
          </button>
          </TooltipTrigger>
          <TooltipContent side={sidebar ? 'right' : 'bottom'}>{sidebar ? sidebarTitle : title || label || placeholder}</TooltipContent>
        </Tooltip>
        {middleControl}
        {onAdd && addLabel && <button
          type="button"
          data-testid={addTestId}
          aria-label={addLabel}
          onClick={() => { onClose(); onAdd() }}
          disabled={addDisabled}
          title={addTitle || addLabel}
          className={`${sidebar ? 'hidden' : ''} border-l border-gray-200 px-2 py-1 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-700 disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-600 dark:text-gray-400 dark:hover:bg-slate-700 dark:hover:text-gray-200`}
        >
          <Plus className="h-3 w-3" />
        </button>}
      </div>
      {open && (
        <div ref={panelRef} style={sidebar ? panelPosition : undefined} className={`preset-dropdown ${sidebar ? 'fixed w-80 max-h-[75vh] overflow-y-auto [&>div]:max-h-none' : 'absolute left-0 top-full mt-1 w-64'} z-50 max-w-[calc(100vw-5rem)] rounded-lg border border-border bg-popover text-popover-foreground shadow-xl`}>
          {sidebar && <header className="sticky top-0 z-10 border-b border-border bg-popover px-4 py-3 text-sm font-semibold">{placeholder === 'Select Automation' ? 'Automations' : 'Workspaces'}</header>}
          {children}
        </div>
      )}
    </div>
  )
}
