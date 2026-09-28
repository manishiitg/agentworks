import { Children, Fragment, cloneElement, isValidElement, type ComponentType, type ReactElement, type ReactNode } from 'react'
import { WorkspaceViewTabs, type WorkspaceViewTabOption } from './WorkspaceViewTabs'
import { WorkspacePanelGuideButton } from './WorkspacePanelGuideButton'
import { panelGuideAskFromNode, type PanelGuideAsk } from './workspacePanelGuideAsk'
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
   * the standard h-9 tile spanning the title + subtitle block. A custom
   * element renders as-is at the same footprint.
   */
  icon?: ComponentType<{ className?: string }> | ReactElement
  /** View title. */
  title: ReactNode
  /** Guide topic when the visible title is dynamic or omitted. */
  helpTopic?: string
  /** A parent toolbar can provide the walkthrough inside its own action pair. */
  showWalkthrough?: boolean
  /** Inline context next to the title: counts, timestamps, badges. */
  context?: ReactNode
  /** Muted line under the title row. */
  subtitle?: ReactNode
  /** Right side. The walkthrough is placed before Refresh; an icon-only Ask AI child moves into the walkthrough popup. */
  actions?: ReactNode
  /** Full-width row under the title (stats strip, pills). For tabs use the tabs prop. */
  below?: ReactNode
  /** Standard tab row pinned to the header's bottom edge, after `below`. */
  tabs?: WorkspaceViewHeaderTabs
  /**
   * Per-tab extra action icons, keyed by tab value. The active tab's extras
   * render left of `actions` (tertiary-left-of-pair rule). To change the base
   * pair itself per tab (Ask AI message, refresh handler), compute `actions`
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
  className?: string
}

/**
 * The one header for every right-side workspace view — workflow inspectors,
 * capability sections, the automation hub, and Crew panes. Plan and Report
 * are the only views without one (canvas overlays instead of headers).
 *
 * The fixed standard, enforced by this component:
 * - icon: h-9 tile (h-4 glyph) spanning title + subtitle; custom elements
 *   render as-is at the same h-9 footprint
 * - title: text-sm font-semibold, rendered here — never override the size
 * - subtitle: text-xs text-muted-foreground, rendered here
 * - actions: walkthrough, Refresh; Ask AI lives inside the walkthrough popup
 */
function isRefreshAction(node: ReactNode): boolean {
  if (!isValidElement<{ label?: string; icon?: unknown; 'aria-label'?: string; children?: ReactNode }>(node)) return false
  if (node.type === WorkspaceViewIconButton) return node.props.icon == null && /^refresh/i.test(node.props.label ?? '')
  const label = node.props['aria-label']
  if (typeof label === 'string' && /^refresh/i.test(label)) return true
  return Children.toArray(node.props.children).some(isRefreshAction)
}

function withAsk(walkthrough: ReactNode, ask: PanelGuideAsk | undefined): ReactNode {
  if (!ask || !isValidElement<{ ask?: PanelGuideAsk }>(walkthrough)) return walkthrough
  return cloneElement(walkthrough, { ask })
}

function insertWalkthrough(actions: ReactNode, walkthrough: ReactNode): ReactNode {
  if (!walkthrough) return actions
  if (isValidElement<WorkspaceViewActionsProps>(actions) && actions.type === WorkspaceViewActions) {
    const { workspacePath, message, onAsk } = actions.props
    return cloneElement(actions, { walkthrough: withAsk(walkthrough, { workspacePath, message, onAsk }) })
  }
  // A lone Ask AI button is the whole row: it moves into the popup and the
  // row keeps just the walkthrough.
  const loneAsk = panelGuideAskFromNode(actions)
  if (loneAsk) return withAsk(walkthrough, loneAsk)
  if (isValidElement<{ children?: ReactNode }>(actions) && actions.type === Fragment) {
    const children: ReactNode[] = Children.toArray(actions.props.children)
    let ask: PanelGuideAsk | undefined
    const rest = children.filter(child => {
      if (ask) return true
      const found = panelGuideAskFromNode(child)
      if (!found) return true
      ask = found
      return false
    })
    const guided = withAsk(walkthrough, ask)
    const refreshIndex = rest.findIndex(isRefreshAction)
    if (refreshIndex >= 0) rest.splice(refreshIndex, 0, guided)
    else rest.push(guided)
    return <>{rest}</>
  }
  return <>{actions}{walkthrough}</>
}

export function WorkspaceViewHeader({
  icon,
  title,
  helpTopic,
  showWalkthrough = true,
  context,
  subtitle,
  actions,
  below,
  tabs,
  sticky = false,
  bare = false,
  className = '',
  tabActions,
}: WorkspaceViewHeaderProps) {
  const tabExtra = tabs ? tabActions?.[tabs.value] : undefined
  const guideTopic = helpTopic ?? (typeof title === 'string' ? title : '')
  const walkthrough = showWalkthrough && guideTopic ? <WorkspacePanelGuideButton key="walkthrough" topic={guideTopic} /> : null
  const renderIcon = () => {
    if (!icon) return null
    if (isValidElement(icon)) return icon
    const Glyph = icon
    return (
      <span className="flex h-9 w-9 items-center justify-center rounded-md border border-primary/25 bg-primary/10 text-primary">
        <Glyph className="h-4 w-4" />
      </span>
    )
  }
  const body = (
    <>
      <div className="flex items-start justify-between gap-3">
        <div className="flex min-w-0 flex-1 gap-2">
          {icon && <div className="shrink-0">{renderIcon()}</div>}
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2">
              <h2 className="truncate text-sm font-semibold text-foreground">{title}</h2>
              {context}
            </div>
            {subtitle && <p className="mt-0.5 truncate text-xs text-muted-foreground">{subtitle}</p>}
          </div>
        </div>
        {(actions || tabExtra || walkthrough) && <div className="flex shrink-0 items-center gap-2">{tabExtra}{insertWalkthrough(actions, walkthrough)}</div>}
      </div>
      {below}
      {tabs && (
        <div className={bare ? 'pt-1' : '-mb-2 pt-1'}>
          <WorkspaceViewTabs value={tabs.value} onChange={tabs.onChange} options={tabs.options} ariaLabel={tabs.ariaLabel} />
        </div>
      )}
    </>
  )
  if (bare) return <>{body}</>
  return (
    <header className={`${sticky ? 'sticky top-0 z-10 border-b border-border bg-background/95 backdrop-blur' : 'shrink-0 border-b border-border'} px-3 py-2 ${className}`}>
      {body}
    </header>
  )
}
