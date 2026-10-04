import type { ReactNode } from 'react'
import { ChevronDown, ChevronRight } from 'lucide-react'

type WorkspaceToolbarGroupProps = {
  label: string
  open: boolean
  onToggle?: () => void
  title: string
  hideLabel?: boolean
  /** One of this group's views is the current one: highlight the label while collapsed. */
  active?: boolean
  /** Hide the toggle (label and arrow) while open: only the icons show. */
  hideToggleWhenOpen?: boolean
  children: ReactNode
} & Record<`data-${string}`, string | undefined>

/** Shared AgentWorks Views/Setup toolbar group used by workflow and product workspaces. */
export function WorkspaceToolbarGroup({ label, open, onToggle, title, hideLabel = false, active = false, hideToggleWhenOpen = false, children, ...rest }: WorkspaceToolbarGroupProps) {
  if (open && hideToggleWhenOpen) {
    return (
      <div {...rest} className="inline-flex h-full items-center gap-0.5 px-1 first:pl-0.5 last:pr-0.5">
        {children}
      </div>
    )
  }
  return (
    <div {...rest} className="inline-flex h-full items-center gap-0.5 px-1 first:pl-0.5 last:pr-0.5">
      {onToggle ? (
        <button
          type="button"
          onClick={onToggle}
          aria-expanded={open}
          aria-label={hideLabel ? label : undefined}
          title={title}
          className={`inline-flex h-6 items-center gap-1 rounded text-[11px] font-medium outline-none transition-colors hover:bg-background/70 ${hideLabel ? 'px-1' : 'px-2'} ${open ? 'text-foreground' : active ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}`}
        >
          {!hideLabel && <span>{label}</span>}
          {open ? <ChevronDown className="h-3 w-3" aria-hidden="true" /> : <ChevronRight className="h-3 w-3" aria-hidden="true" />}
        </button>
      ) : (
        <span title={title} className="inline-flex h-6 items-center px-2 text-[11px] font-medium text-foreground">{label}</span>
      )}
      {open && children}
    </div>
  )
}
