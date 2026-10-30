import { Children, Fragment, cloneElement, createElement, isValidElement, type ComponentType, type ReactElement, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { useWorkspacePanelHeaderSlots } from '../workspace/WorkspacePanelHeaderSlots'
import { WorkspaceViewTabs, type WorkspaceViewTabOption } from './WorkspaceViewTabs'
import { WorkspaceViewActions, type WorkspaceViewActionsProps } from './WorkspaceViewActions'
import { WorkspaceViewIconButton } from './WorkspaceViewIconButton'

export interface WorkspaceViewHeaderTabs {
  value: string
  // Plain strings: every pane keeps its own narrow tab union and adapts
  // at the boundary (the header only ever passes back an option value).
  onChange: (value: string) => void
  options: WorkspaceViewTabOption<string>[]
  ariaLabel: string
}

type WorkspaceViewHeaderProps = {
  /**
   * Leading icon as a component reference (`icon={Activity}`): rendered in
   * the standard h-8 tile spanning the title + subtitle block. A custom
   * element renders as-is at the same footprint.
   */
  icon?: ComponentType<{ className?: string }> | ReactElement
  /** View title. */
  title: ReactNode
  /** Hide the title, icon and subtitle; retain explicit actions, tabs and below content. */
  hideHeader?: boolean
  /** Inline context next to the title: counts, timestamps, badges. */
  context?: ReactNode
  /** Muted line under the title row. */
  subtitle?: ReactNode
  /** Right side: page actions. */
  actions?: ReactNode
  /** Full-width row under the title (stats strip, pills). For tabs use the tabs prop. */
  below?: ReactNode
  /** Standard tab row pinned to the header's bottom edge, after `below`. */
  tabs?: WorkspaceViewHeaderTabs
  /**
   * Per-tab extra action icons, keyed by tab value. The active tab's extras
   * render left of `actions` (tertiary-left-of-pair rule). To change the base
   * pair itself per tab (refresh handler), compute `actions`
   * from the tab instead — see WorkflowCapabilitiesPanel.
   */
  tabActions?: Partial<Record<string, ReactNode>>
  /** Sticky variant for scroll views (triggers). */
  sticky?: boolean
  /**
   * Bare variant: title row without the bordered header wrapper, for views
   * whose shell owns the row (Costs, Execution Logs via InspectorShell).
   * Visually identical to the standard header.
   */
  bare?: boolean
  /** Full-screen tools keep their controls inside the expanded surface. */
  local?: boolean
  className?: string
}

/**
 * The one header for every right-side workspace view — workflow inspectors,
 * capability sections, the automation hub, and Crew panes. Hosted headings
 * and controls render in the panel shell; standalone views render locally.
 *
 * The fixed standard, enforced by this component:
 * - icon: h-8 tile (h-4 glyph) spanning title + subtitle; custom elements
 *   render as-is at the same h-8 footprint
 * - title: text-sm font-semibold in the shared compact heading
 * - subtitle: text-xs text-muted-foreground, rendered here
 * - actions: page operations, with Back only for nested details
 */
/** The panels refresh themselves now (owner, 2026-10-20), so the header's own Refresh icons are dropped. */
function isPlainRefresh(node: ReactNode): boolean {
  return isValidElement<{ label?: string; icon?: unknown }>(node) && node.type === WorkspaceViewIconButton && node.props.icon == null && /^refresh/i.test(node.props.label ?? '')
}

function withoutRefresh(actions: ReactNode): ReactNode {
  if (isValidElement<WorkspaceViewActionsProps>(actions) && actions.type === WorkspaceViewActions) return cloneElement(actions, { hideRefresh: true })
  if (isPlainRefresh(actions)) return null
  if (isValidElement<{ children?: ReactNode }>(actions) && actions.type === Fragment) return <>{Children.toArray(actions.props.children).filter(child => !isPlainRefresh(child))}</>
  return actions
}

export function WorkspaceViewHeader({
  icon,
  title,
  hideHeader = false,
  context,
  subtitle,
  actions: rawActions,
  below,
  tabs,
  sticky = false,
  bare = false,
  local = false,
  className = '',
  tabActions,
}: WorkspaceViewHeaderProps) {
  const actions = withoutRefresh(rawActions)
  const hasSubtitle = typeof subtitle === 'string' ? subtitle.trim().length > 0 : Boolean(subtitle)
  const tabExtra = tabs ? tabActions?.[tabs.value] : undefined
  const slots = useWorkspacePanelHeaderSlots()
  const iconNode = isValidElement(icon) ? icon : icon ? createElement(icon as ComponentType<{ className?: string }>, { className: 'h-4 w-4' }) : null
  const heading = <div data-workspace-panel-title className="flex min-w-0 items-center gap-2.5">
    {iconNode && <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-muted/50 text-muted-foreground">{iconNode}</span>}
    <div className="min-w-0 flex-1">
      <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5">
        <h2 className="truncate text-sm font-semibold">{title}</h2>
        {context}
      </div>
      {hasSubtitle && <div className="mt-0.5 text-xs text-muted-foreground">{subtitle}</div>}
    </div>
  </div>
  const controls = <div className="flex max-w-full flex-wrap items-center justify-end gap-1.5">{tabExtra}{actions}</div>
  const contentControls = <>{below}{tabs && <WorkspaceViewTabs value={tabs.value} onChange={tabs.onChange} options={tabs.options} ariaLabel={tabs.ariaLabel} inset={!bare} />}</>

  // Portals retain each view's state and callbacks. Only its heading/actions
  // move; search, filters and tabs remain content below the one fixed header.
  if (!local && slots?.heading && slots.actions) return <>
    {createPortal(heading, slots.heading)}
    {createPortal(controls, slots.actions)}
    {(below || tabs) && <div className={`shrink-0 border-b border-border px-3 py-2 ${className}`}>{contentControls}</div>}
  </>

  // Dialogs, full-screen tools and embedded views without an outer header keep
  // the same compact header locally.
  if (hideHeader && !actions && !tabExtra && !below && !tabs) return null
  const body = <>
    {(!hideHeader || actions || tabExtra) && <div className="flex flex-wrap items-center justify-between gap-3">
      {!hideHeader && heading}
      {controls}
    </div>}
    {contentControls}
  </>
  if (bare) return <>{body}</>
  return <header className={`${sticky ? 'sticky top-0 z-10 bg-background/95 backdrop-blur' : 'shrink-0'} border-b border-border px-3 py-2 ${className}`}>{body}</header>
}
