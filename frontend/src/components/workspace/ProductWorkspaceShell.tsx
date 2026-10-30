import type { HTMLAttributes, ReactNode, Ref } from 'react'
import { PanelLeftOpen } from 'lucide-react'
import { resolveWorkSurfaceLayout } from '../../products/work/workSurfaceLayoutResolver'
import { MobileWorkspaceNavigationContext, type MobileWorkspacePane } from './MobileWorkspaceNavigation'
import { WorkspaceTopToolbar } from './WorkspaceTopToolbar'
import { WorkspacePanelVisibilityContext } from './WorkspacePanelToggle'

/** Complete split workspace used by Crew, Code, Vault and Brain. Product surfaces
 * supply content; pane geometry, tabs row and reopen controls have one owner. */
export function ProductWorkspaceShell({
  chatOpen, panelOpen, splitRatio, mobilePreview = false, laptopPreview = false, mobilePaneWidth, mobilePane, onMobilePaneChange, splitRef,
  onOpenChat, onOpenWorkspace, onCloseWorkspace, tabs, toolbar, chat, workspace, divider,
  chatProps, workspaceProps, testId, compactContext = false, toolbarWhenPanelClosed = false, showToolbar = true, navigationActive = true, retainChat = false, retainWorkspace = false, showChatReopenControl = true,
}: {
  chatOpen: boolean; panelOpen: boolean; splitRatio: number; mobilePreview?: boolean; laptopPreview?: boolean; mobilePaneWidth?: number | null
  /** Products with a phone pane switch keep both panes mounted while hiding the inactive one. */
  mobilePane?: MobileWorkspacePane
  onMobilePaneChange?: (pane: MobileWorkspacePane) => void
  splitRef?: Ref<HTMLDivElement>
  onOpenChat: () => void; onOpenWorkspace: () => void
  onCloseWorkspace?: () => void
  tabs: ReactNode; toolbar: ReactNode; chat: ReactNode; workspace: ReactNode; divider: ReactNode
  chatProps?: HTMLAttributes<HTMLElement>; workspaceProps?: HTMLAttributes<HTMLElement>
  compactContext?: boolean; toolbarWhenPanelClosed?: boolean
  /** Shared dashboards supply their own header, without the Builder chat tabs. */
  showToolbar?: boolean
  /** Retained hidden workspaces release shared header controls to the visible page. */
  navigationActive?: boolean
  testId?: string
  /** Secondary assistants keep an unsent draft when closed. */
  retainChat?: boolean
  /** Secondary panels keep their draft and mounted content while closed. */
  retainWorkspace?: boolean
  /** Products with an explicit Assistant action do not need the edge tab. */
  showChatReopenControl?: boolean
}) {
  const layout = resolveWorkSurfaceLayout({ chatOpen, panelOpen, splitRatio, mobilePreview, laptopPreview, mobilePaneWidth, mobilePane, compactContext })
  const chatClassName = mobilePane === 'workspace' && panelOpen
    ? `hidden md:flex ${layout.chatClassName.replace(/^flex /, '')}` : layout.chatClassName
  const workspaceClassName = mobilePane === 'chat' && chatOpen
    ? `hidden md:block ${layout.panelClassName}` : layout.panelClassName
  return <WorkspacePanelVisibilityContext.Provider value={onCloseWorkspace && chatOpen ? { open: layout.showPanel, onToggle: layout.showPanel ? onCloseWorkspace : onOpenWorkspace } : null}><MobileWorkspaceNavigationContext.Provider value={mobilePane && onMobilePaneChange && showChatReopenControl ? { pane: mobilePane, onChange: onMobilePaneChange } : null}><div className="relative h-full min-h-0 min-w-0" data-testid={testId}>
    {!chatOpen && showChatReopenControl && <button type="button" onClick={onOpenChat} title="Show chat panel" aria-label="Show chat panel"
      className="absolute left-0 top-1/2 z-30 hidden -translate-y-1/2 flex-col items-center gap-1.5 rounded-r-lg border border-l-0 border-border bg-background/95 py-3 pl-1 pr-1.5 text-muted-foreground shadow-md backdrop-blur-sm transition-colors hover:bg-muted hover:text-foreground md:flex">
      <PanelLeftOpen className="h-4 w-4" /><span className="[writing-mode:vertical-rl] text-[10px] font-semibold uppercase tracking-wider">Chat</span>
    </button>}
    <div ref={splitRef} className={layout.gridClassName} style={layout.gridStyle}>
      {showToolbar && <WorkspaceTopToolbar className={layout.toolbarClassName} navigationActive={navigationActive}>
        {tabs}{layout.showPanel || toolbarWhenPanelClosed ? toolbar : null}
      </WorkspaceTopToolbar>}
      {(layout.showChat || retainChat) && <main {...chatProps} hidden={!layout.showChat} style={{ ...chatProps?.style, ...(!layout.showChat ? { display: 'none' } : {}) }} className={chatClassName}>{chat}</main>}
      {layout.showDivider && divider}
      {(layout.showPanel || retainWorkspace) && <aside {...workspaceProps} hidden={!layout.showPanel} style={{ ...workspaceProps?.style, ...(!layout.showPanel ? { display: 'none' } : {}) }} className={workspaceClassName}><div className="flex h-full min-h-0 min-w-0 flex-col">{workspace}</div></aside>}
    </div>
  </div></MobileWorkspaceNavigationContext.Provider></WorkspacePanelVisibilityContext.Provider>
}
