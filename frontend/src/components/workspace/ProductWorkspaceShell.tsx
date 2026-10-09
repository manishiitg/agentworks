import type { HTMLAttributes, ReactNode, Ref } from 'react'
import { PanelLeftOpen, PanelRightOpen } from 'lucide-react'
import { resolveWorkSurfaceLayout } from '../../products/work/workSurfaceLayoutResolver'
import { WorkspaceTopToolbar } from './WorkspaceTopToolbar'

/** Complete split workspace used by Crew, Code, Vault and Brain. Product surfaces
 * supply content; pane geometry, tabs row and reopen controls have one owner. */
export function ProductWorkspaceShell({
  chatOpen, panelOpen, splitRatio, mobilePreview = false, mobilePaneWidth, mobilePane, splitRef,
  onOpenChat, onOpenWorkspace, tabs, toolbar, chat, workspace, divider,
  chatProps, workspaceProps, testId,
}: {
  chatOpen: boolean; panelOpen: boolean; splitRatio: number; mobilePreview?: boolean; mobilePaneWidth?: number | null
  /** Products with a phone pane switch keep both panes mounted while hiding the inactive one. */
  mobilePane?: 'chat' | 'workspace'
  splitRef?: Ref<HTMLDivElement>
  onOpenChat: () => void; onOpenWorkspace: () => void
  tabs: ReactNode; toolbar: ReactNode; chat: ReactNode; workspace: ReactNode; divider: ReactNode
  chatProps?: HTMLAttributes<HTMLElement>; workspaceProps?: HTMLAttributes<HTMLElement>
  testId?: string
}) {
  const layout = resolveWorkSurfaceLayout({ chatOpen, panelOpen, splitRatio, mobilePreview, mobilePaneWidth })
  const chatClassName = mobilePane === 'workspace' && panelOpen
    ? `hidden md:flex ${layout.chatClassName.replace(/^flex /, '')}` : layout.chatClassName
  const workspaceClassName = mobilePane === 'chat' && chatOpen
    ? `hidden md:block ${layout.panelClassName}` : layout.panelClassName
  return <div className="relative h-full min-h-0 min-w-0" data-testid={testId}>
    {!panelOpen && <button type="button" onClick={onOpenWorkspace} title="Show workspace" aria-label="Show workspace"
      className="absolute right-0 top-1/2 z-30 hidden -translate-y-1/2 flex-col items-center gap-1.5 rounded-l-lg border border-r-0 border-border bg-background/95 py-3 pl-1.5 pr-1 text-muted-foreground shadow-md backdrop-blur-sm transition-colors hover:bg-muted hover:text-foreground md:flex">
      <PanelRightOpen className="h-4 w-4" /><span className="[writing-mode:vertical-rl] text-[10px] font-semibold uppercase tracking-wider">Workspace</span>
    </button>}
    {!chatOpen && <button type="button" onClick={onOpenChat} title="Show chat panel" aria-label="Show chat panel"
      className="absolute left-0 top-1/2 z-30 hidden -translate-y-1/2 flex-col items-center gap-1.5 rounded-r-lg border border-l-0 border-border bg-background/95 py-3 pl-1 pr-1.5 text-muted-foreground shadow-md backdrop-blur-sm transition-colors hover:bg-muted hover:text-foreground md:flex">
      <PanelLeftOpen className="h-4 w-4" /><span className="[writing-mode:vertical-rl] text-[10px] font-semibold uppercase tracking-wider">Chat</span>
    </button>}
    <div ref={splitRef} className={layout.gridClassName} style={layout.gridStyle}>
      <WorkspaceTopToolbar className={layout.toolbarClassName}>
        {tabs}{panelOpen ? toolbar : null}
      </WorkspaceTopToolbar>
      {layout.showChat && <main {...chatProps} className={chatClassName}>{chat}</main>}
      {layout.showDivider && divider}
      {layout.showPanel && <aside {...workspaceProps} className={workspaceClassName}>{workspace}</aside>}
    </div>
  </div>
}
