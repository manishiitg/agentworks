import type { ReactNode } from 'react'
import { ChevronDown } from 'lucide-react'
import ConnectionIcon from '../connectors/ConnectionIcon'
import { brandSlugFor } from '../connectors/brandSlug'
import { Button } from '../ui/Button'

/** Shared connection summary; callers supply only the actions their scope permits. */
export function McpServerHeader({ name, brandName, status, statusDot, detail, toolCount, toolSummary, selection, expanded = false, onToggleTools, toolsLabel, compactToggle = false, actions }: {
  name: string; status: string; statusDot?: string; detail?: ReactNode; toolCount?: number
  brandName?: string; toolSummary?: ReactNode; compactToggle?: boolean
  selection?: ReactNode; expanded?: boolean; onToggleTools?: () => void; toolsLabel?: string; actions?: ReactNode
}) {
  return <div className="flex flex-wrap items-center justify-between gap-3">
    <div className="flex min-w-0 flex-1 items-center gap-2.5">
      {selection}
      <ConnectionIcon icon={brandSlugFor(brandName || name)} name={name} size="xs" />
      <div className="min-w-0 space-y-1">
        <h4 className="break-words text-sm font-semibold text-foreground">{name}</h4>
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
          {status && <span className="inline-flex items-center gap-1.5">
            <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${statusDot || 'bg-muted-foreground'}`} aria-hidden />{status}
          </span>}
          {toolSummary}
          {toolCount !== undefined && <><span aria-hidden>·</span><span>{toolCount} {toolCount === 1 ? 'tool' : 'tools'}</span></>}
          {detail}
        </div>
      </div>
    </div>
    <div className="ml-auto flex shrink-0 items-center gap-2">
      {onToggleTools && <Button variant={compactToggle ? 'ghost' : 'outline'} size={compactToggle ? 'icon' : 'sm'} className={compactToggle ? 'h-7 w-7' : undefined} aria-expanded={expanded}
        aria-label={toolsLabel || `${expanded ? 'Hide' : 'Show'} tools on ${name}`} onClick={onToggleTools}>
        {!compactToggle && (expanded ? 'Hide tools' : 'View tools')}<ChevronDown className={`transition-transform ${expanded ? 'rotate-180' : ''}`} aria-hidden />
      </Button>}
      {actions}
    </div>
  </div>
}
