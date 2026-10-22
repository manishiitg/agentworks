import type { LucideIcon } from 'lucide-react'
import { WORKSPACE_PAGE_GUTTER } from '../workspace/WorkspacePageContent'

export interface WorkspaceViewTabOption<TabId extends string = string> {
  value: TabId
  label: string
  icon?: LucideIcon
  count?: number
}

export interface WorkspaceViewTabsProps {
  value: string
  onChange: (value: string) => void
  options: WorkspaceViewTabOption<string>[]
  ariaLabel: string
  /** An enclosing header already supplies the shared page gutter. */
  inset?: boolean
}

/**
 * The standard tab row for right-pane headers, rendered by the header's
 * `tabs` prop. Panels reuse it directly for sub-tabs (Slack/WhatsApp in
 * standalone hosts) so every tab bar in the right pane looks and behaves
 * the same.
 */
export function WorkspaceViewTabs({
  value,
  onChange,
  options,
  ariaLabel,
  inset = false,
}: WorkspaceViewTabsProps) {
  return (
    <div className="flex items-center gap-1 overflow-x-auto" role="tablist" aria-label={ariaLabel}>
      {options.map(option => {
        const selected = option.value === value
        const Icon = option.icon
        return (
          <button
            key={option.value}
            type="button"
            role="tab"
            aria-selected={selected}
            title={option.label}
            onClick={() => onChange(option.value)}
            className={`inline-flex shrink-0 items-center gap-2 border-b-2 ${WORKSPACE_PAGE_GUTTER} ${inset ? 'first:pl-0 last:pr-0' : ''} py-1.5 text-xs font-medium transition-colors ${
              selected
                ? 'border-primary text-foreground'
                : 'border-transparent text-muted-foreground hover:text-foreground'
            }`}
          >
            {Icon && <Icon className="h-4 w-4" aria-hidden="true" />}
            {option.label}
            {option.count !== undefined && <span className="ml-1 text-muted-foreground"> {option.count}</span>}
          </button>
        )
      })}
    </div>
  )
}
