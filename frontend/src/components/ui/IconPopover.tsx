import React, { useEffect, useRef, useState } from 'react'
import { Tooltip, TooltipContent, TooltipTrigger } from './tooltip'

/** Shared styling for the compact icon buttons used across the top bar. */
export const iconButtonClass =
  'p-1.5 rounded-md text-gray-500 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-700 hover:text-gray-700 dark:hover:text-gray-200 transition-colors'

interface IconPopoverProps {
  icon: React.ReactNode
  /** Accessible label, also shown as the trigger tooltip. */
  label: string
  children: React.ReactNode | ((close: () => void) => React.ReactNode)
  /** Side the panel anchors to relative to the trigger. */
  align?: 'left' | 'right'
  badge?: React.ReactNode
  dataTour?: string
  dataTestid?: string
  panelClassName?: string
}

/**
 * IconPopover - a generic icon button that toggles an anchored, scrollable
 * popover panel. Closes on outside-click and Escape. Must be rendered inside a
 * TooltipProvider.
 */
export default function IconPopover({
  icon,
  label,
  children,
  align = 'right',
  badge,
  dataTour,
  dataTestid,
  panelClassName = 'w-80',
}: IconPopoverProps) {
  const [open, setOpen] = useState(false)
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const onMouseDown = (event: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) {
        setOpen(false)
      }
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

  return (
    <div ref={containerRef} className="relative">
      <Tooltip>
        <TooltipTrigger asChild>
          <button
            type="button"
            data-tour={dataTour}
            data-testid={dataTestid}
            onClick={() => setOpen(prev => !prev)}
            aria-label={label}
            aria-expanded={open}
            className={`relative ${iconButtonClass} ${open ? 'bg-gray-100 text-gray-700 dark:bg-gray-700 dark:text-gray-200' : ''}`}
          >
            {icon}
            {badge}
          </button>
        </TooltipTrigger>
        <TooltipContent side="bottom">{label}</TooltipContent>
      </Tooltip>

      {open && (
        <div
          className={`absolute ${align === 'right' ? 'right-0' : 'left-0'} top-full mt-2 ${panelClassName} max-h-[75vh] overflow-y-auto rounded-lg border border-gray-200 dark:border-slate-700 bg-white dark:bg-slate-800 shadow-xl z-[60] p-3`}
        >
          {typeof children === 'function' ? children(() => setOpen(false)) : children}
        </div>
      )}
    </div>
  )
}
