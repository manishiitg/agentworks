import { useEffect, useRef, type ReactNode } from 'react'
import { Plus } from 'lucide-react'

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
  const rootRef = useRef<HTMLDivElement>(null)

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
    <div ref={rootRef} className="relative flex min-w-0 items-center">
      <div
        data-tour={dataTour}
        data-testid={testId}
        className="flex min-w-0 items-center overflow-hidden rounded-md border border-gray-200 bg-white dark:border-gray-600 dark:bg-gray-800"
      >
        <button
          type="button"
          onClick={onToggle}
          aria-haspopup="menu"
          aria-expanded={open}
          aria-label={label || placeholder}
          className="flex min-w-0 items-center gap-2 px-3 py-1 transition-colors hover:bg-gray-100 dark:hover:bg-slate-700"
          title={title}
        >
          {leading ?? <div className={`h-2 w-2 shrink-0 rounded-full ${active ? 'bg-green-500' : 'bg-gray-400'}`} />}
          <span className={`${compactOnNarrow ? 'hidden min-[1180px]:block' : 'block'} max-w-[190px] truncate whitespace-nowrap text-sm font-medium ${active ? 'text-gray-700 dark:text-gray-300' : 'text-gray-500 dark:text-gray-400'}`}>
            {label || placeholder}
          </span>
          {badge}
        </button>
        {middleControl}
        {onAdd && addLabel && <button
          type="button"
          data-testid={addTestId}
          aria-label={addLabel}
          onClick={() => { onClose(); onAdd() }}
          disabled={addDisabled}
          title={addTitle || addLabel}
          className="border-l border-gray-200 px-2 py-1 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-700 disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-600 dark:text-gray-400 dark:hover:bg-slate-700 dark:hover:text-gray-200"
        >
          <Plus className="h-3 w-3" />
        </button>}
      </div>
      {open && (
        <div className="preset-dropdown absolute left-0 top-full z-50 mt-1 w-64 rounded-lg border border-gray-200 bg-white shadow-lg dark:border-slate-700 dark:bg-slate-800">
          {children}
        </div>
      )}
    </div>
  )
}
