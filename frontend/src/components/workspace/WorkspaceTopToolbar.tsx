import { useCallback, useRef, type HTMLAttributes } from 'react'
import { useProductNavigationToolbarStore } from '../../stores/useProductNavigationToolbarStore'
import { useAppStore } from '../../stores/useAppStore'
import { useLLMStore } from '../../stores/useLLMStore'
import { cn } from '../../lib/utils'
import { WorkspacePanelToggle } from './WorkspacePanelToggle'
import { MobileWorkspaceNavigation } from './MobileWorkspaceNavigation'
import { WORKSPACE_PAGE_GUTTER } from './WorkspacePageContent'

/** Shared AgentWorks toolbar row used above chat/workspace split surfaces. */
export function WorkspaceTopToolbar({ className = '', children, navigationKind = 'workspace', navigationActive = true, ...props }: HTMLAttributes<HTMLDivElement> & { navigationKind?: 'workspace' | 'page'; navigationActive?: boolean }) {
  const globalPage = useAppStore(state => state.showWorkflowsOverview || state.showSchedulesOverview || state.adminPage !== null)
  const providers = useLLMStore(state => state.showLLMModal)
  const ownSlot = useRef<HTMLDivElement | null>(null)
  const register = useCallback((node: HTMLDivElement | null) => {
    const previous = ownSlot.current
    ownSlot.current = node
    if (node || useProductNavigationToolbarStore.getState().target === previous) {
      useProductNavigationToolbarStore.setState({ target: node, targetKind: node ? navigationKind : null })
    }
  }, [navigationKind])
  const ownLeadingSlot = useRef<HTMLDivElement | null>(null)
  const registerLeading = useCallback((node: HTMLDivElement | null) => {
    const previous = ownLeadingSlot.current
    ownLeadingSlot.current = node
    if (node || useProductNavigationToolbarStore.getState().leadingTarget === previous) {
      useProductNavigationToolbarStore.setState({ leadingTarget: node })
    }
  }, [])
  const showNavigation = navigationActive && (navigationKind === 'page' || (!globalPage && !providers))
  return (
    <div
      {...props}
      data-terminal-focus-chrome="toolbar"
      // Above the canvas and resize rail (z-30), below the chat sidebar (z-40)
      // and dialogs (z-50). Dropdowns must receive clicks over the split rail.
      // Below md the tabs and toolbar controls wrap onto a second row instead
      // of overflowing over the tabs (a 390 px phone hid the project tab and
      // covered "+ New chat" with Dashboard). Desktop keeps one nowrap row.
      className={cn('relative z-[35] flex min-h-10 min-w-0 flex-wrap items-center gap-x-1 gap-y-1.5 overflow-visible md:gap-x-3 border-b border-border bg-background py-1.5 md:flex-nowrap', WORKSPACE_PAGE_GUTTER, className)}
    >
      {/* Display contents prevents a hidden mobile launcher reserving a flex gap on desktop. */}
      {showNavigation && <div ref={registerLeading} data-product-navigation-leading-slot className="contents empty:hidden" />}
      {children}
      {showNavigation && <div ref={register} data-product-navigation-slot className="ml-auto flex min-w-0 max-w-full flex-wrap items-center empty:hidden" />}
      {showNavigation && <div data-workspace-panel-control className="order-last ml-auto flex shrink-0 items-center empty:hidden">
        <WorkspacePanelToggle />
        <MobileWorkspaceNavigation />
      </div>}
    </div>
  )
}
