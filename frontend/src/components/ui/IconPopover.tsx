import { ProductNavigationLabel, useProductNavigationSidebar } from '../workspace/ProductTopBar'
import React, { useEffect, useRef, useState } from 'react'
import { Tooltip, TooltipContent, TooltipTrigger } from './tooltip'

/** Shared styling for the compact icon buttons used across the top bar. */
export const iconButtonClass =
  'p-1.5 rounded-md text-muted-foreground  hover:bg-muted  hover:text-foreground  transition-colors'

interface IconPopoverProps {
  icon: React.ReactNode
  /** Accessible label, also shown as the trigger tooltip. */
  label: string
  navigationLabel?: string
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
  navigationLabel,
  children,
  align = 'right',
  badge,
  dataTour,
  dataTestid,
  panelClassName = 'w-80',
}: IconPopoverProps) {
  const sidebar = useProductNavigationSidebar()
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
            data-product-navigation-action
            data-tour={dataTour}
            data-testid={dataTestid}
            onClick={() => setOpen(prev => !prev)}
            aria-label={label}
            aria-expanded={open}
            className={`relative ${iconButtonClass} ${open ? 'bg-muted text-foreground  ' : ''}`}
          >
            {icon}
            <ProductNavigationLabel>{navigationLabel || label}</ProductNavigationLabel>
            {badge}
          </button>
        </TooltipTrigger>
        <TooltipContent side={sidebar ? 'right' : 'bottom'}>{label}</TooltipContent>
      </Tooltip>

      {open && (
        <div
          data-theme-surface="popover"
          className={`absolute ${sidebar ? 'left-[calc(100%+12px)] bottom-0' : `${align === 'right' ? 'right-0' : 'left-0'} top-full mt-2`} ${panelClassName} max-w-[calc(100vw-5rem)] max-h-[75vh] overflow-y-auto rounded-lg border border-border  bg-surface-popover shadow-xl z-[60] p-3`}
        >
          {typeof children === 'function' ? children(() => setOpen(false)) : children}
        </div>
      )}
    </div>
  )
}
