import { Children, Fragment, cloneElement, isValidElement, type ComponentType, type ReactElement, type ReactNode } from 'react'
import { WorkspaceViewTabs, type WorkspaceViewTabOption } from './WorkspaceViewTabs'
import { WorkspacePanelGuideButton } from './WorkspacePanelGuideButton'
import { panelGuideAskFromNode, type PanelGuideAsk } from './workspacePanelGuideAsk'
import { WorkspaceViewActions, type WorkspaceViewActionsProps } from './WorkspaceViewActions'
import { WorkspaceViewIconButton } from './WorkspaceViewIconButton'
import { useWorkspacePanelAssistant, useWorkspacePanelHosted } from '../workspace/WorkspacePanelNavigation'

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
  /** Hide the title, icon and subtitle; retain explicit actions, tabs and below content. */
  hideHeader?: boolean
  /** Guide topic when the visible title is dynamic or omitted. */
  helpTopic?: string
  /** A parent toolbar can provide the walkthrough inside its own action pair. */
  showWalkthrough?: boolean
  /** Inline context next to the title: counts, timestamps, badges. */
  context?: ReactNode
  /** Muted line under the title row. */
  subtitle?: ReactNode
  /** Right side: page actions and walkthrough. Contextual questions go to the shared toolbar when hosted. */
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
 * - title: text-sm with a subtitle, text-lg without one; font-semibold
 * - subtitle: text-xs text-muted-foreground, rendered here
 * - actions: page operations and walkthrough; contextual questions live in the outer toolbar
 */
function isRefreshAction(node: ReactNode): boolean {
  if (!isValidElement<{ label?: string; icon?: unknown; 'aria-label'?: string; children?: ReactNode }>(node)) return false
  if (node.type === WorkspaceViewIconButton) return node.props.icon == null && /^refresh/i.test(node.props.label ?? '')
  const label = node.props['aria-label']
  if (typeof label === 'string' && /^refresh/i.test(label)) return true
  return Children.toArray(node.props.children).some(isRefreshAction)
}

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

function withAsk(walkthrough: ReactNode, ask: PanelGuideAsk | undefined): ReactNode {
  if (!ask || !isValidElement<{ ask?: PanelGuideAsk }>(walkthrough)) return walkthrough
  return cloneElement(walkthrough, { ask })
}

function insertWalkthrough(actions: ReactNode, walkthrough: ReactNode, assistantInHeader = false): ReactNode {
  if (!walkthrough) return actions
  if (isValidElement<WorkspaceViewActionsProps>(actions) && actions.type === WorkspaceViewActions) {
    const { workspacePath, message, onAsk } = actions.props
    return cloneElement(actions, { walkthrough: assistantInHeader ? walkthrough : withAsk(walkthrough, { workspacePath, message, onAsk }) })
  }
  // A lone Ask AI button is the whole row: it moves into the popup and the
  // row keeps just the walkthrough.
  const loneAsk = panelGuideAskFromNode(actions)
  if (loneAsk) return assistantInHeader ? walkthrough : withAsk(walkthrough, loneAsk)
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
    const guided = assistantInHeader ? walkthrough : withAsk(walkthrough, ask)
    const refreshIndex = rest.findIndex(isRefreshAction)
    if (refreshIndex >= 0) rest.splice(refreshIndex, 0, guided)
    else rest.push(guided)
    return <>{rest}</>
  }
  return <>{actions}{walkthrough}</>
}

function headerAsk(actions: ReactNode): PanelGuideAsk | undefined {
  const direct = panelGuideAskFromNode(actions)
  if (direct) return direct
  if (isValidElement<WorkspaceViewActionsProps>(actions) && actions.type === WorkspaceViewActions) return { workspacePath: actions.props.workspacePath, message: actions.props.message, onAsk: actions.props.onAsk }
  if (isValidElement<{ children?: ReactNode }>(actions)) {
    for (const child of Children.toArray(actions.props.children)) {
      const found = headerAsk(child)
      if (found) return found
    }
  }
}

/** Portaled help and assistance need no extra row inside the panel. */
function hasInlineAction(node: ReactNode): boolean {
  if (node == null || typeof node === 'boolean') return false
  if (Array.isArray(node)) return node.some(hasInlineAction)
  if (!isValidElement<{ children?: ReactNode; walkthrough?: ReactNode; hideRefresh?: boolean }>(node)) return true
  if (node.type === WorkspacePanelGuideButton) return false
  if (node.type === Fragment) return Children.toArray(node.props.children).some(hasInlineAction)
  if (node.type === WorkspaceViewActions) return !node.props.hideRefresh || hasInlineAction(node.props.children) || hasInlineAction(node.props.walkthrough)
  return true
}

export function WorkspaceViewHeader({
  title,
  hideHeader = false,
  helpTopic,
  showWalkthrough = true,
  context,
  subtitle,
  actions: rawActions,
  below,
  tabs,
  sticky = false,
  bare = false,
  className = '',
  tabActions,
}: WorkspaceViewHeaderProps) {
  const assistantInHeader = useWorkspacePanelAssistant(headerAsk(rawActions))
  const hosted = useWorkspacePanelHosted()
  const actions = withoutRefresh(rawActions)
  const hasSubtitle = typeof subtitle === 'string' ? subtitle.trim().length > 0 : Boolean(subtitle)
  const tabExtra = tabs ? tabActions?.[tabs.value] : undefined
  const guideTopic = helpTopic ?? (typeof title === 'string' ? title : '')
  const walkthrough = (!hideHeader || hosted) && showWalkthrough && guideTopic ? <WorkspacePanelGuideButton key="walkthrough" topic={guideTopic} /> : null
  const headerActions = insertWalkthrough(actions, walkthrough, assistantInHeader)
  if (hideHeader && !actions && !tabExtra && !below && !tabs) return hosted ? walkthrough : null
  if (hosted && !hasSubtitle && !context && !below && !tabs && !tabExtra && !hasInlineAction(headerActions)) {
    return <>{!hideHeader && <h2 className="sr-only">{title}</h2>}{headerActions}</>
  }
  const body = (
    <>
      {(!hideHeader || actions || tabExtra) && <div className={`flex ${hasSubtitle ? 'items-start' : 'items-center'} ${hideHeader ? 'justify-end' : 'justify-between'} gap-3`}>
        {!hideHeader && <div className={`flex min-w-0 flex-1 ${hasSubtitle ? 'items-start' : 'items-center'} gap-2`}>
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2">
              {/* The side menu already names the panel; the title stays for screen readers only. */}
              <h2 className="sr-only">{title}</h2>
              {context}
            </div>
            {hasSubtitle && <p className="mt-0.5 truncate text-xs text-muted-foreground">{subtitle}</p>}
          </div>
        </div>}
        {(actions || tabExtra || walkthrough) && <div className="flex shrink-0 items-center gap-2">{tabExtra}{headerActions}</div>}
      </div>}
      {below}
      {tabs && (
        <div className={bare ? 'pt-1' : '-mb-2 pt-1'}>
          <WorkspaceViewTabs value={tabs.value} onChange={tabs.onChange} options={tabs.options} ariaLabel={tabs.ariaLabel} inset={!bare} />
        </div>
      )}
    </>
  )
  if (bare) return <>{body}</>
  // Standalone views retain a light local action row. Hosted help lives in
  // the panel navigation header; only page-specific controls stay here.
  if (!hasSubtitle && !context && !below && !tabs && !tabExtra) {
    return <div className={`shrink-0 px-3 pt-2 ${sticky ? 'sticky top-0 z-10 bg-background/95 backdrop-blur' : ''} ${className}`}>{body}</div>
  }
  return (
    <header className={`${sticky ? 'sticky top-0 z-10 border-b border-border bg-background/95 backdrop-blur' : 'shrink-0 border-b border-border'} px-3 py-2 ${className}`}>
      {body}
    </header>
  )
}
