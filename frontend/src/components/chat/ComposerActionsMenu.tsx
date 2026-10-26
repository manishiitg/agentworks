import { useId, useRef } from 'react'
import * as Menu from '@radix-ui/react-dropdown-menu'
import { Loader2, Paperclip, Plus, Terminal, Wand2 } from 'lucide-react'
import { ReachableAgentsMenu } from './ReachableAgentsMenu'
import { ChatSendOptions, type ChatSendOptionsProps } from './ChatSendModeButton'

/** Presentation only: every action keeps its existing handler and admission. */
export function ComposerActionsMenu({ uploading, onAttach, onOpenCommands, onOpenLiveView, liveViewTitle, showAgents, sendOptions, onOpenChange }: {
  uploading: boolean
  onAttach: () => void
  onOpenCommands?: () => void
  onOpenLiveView?: () => void
  liveViewTitle?: string
  showAgents: boolean
  sendOptions?: ChatSendOptionsProps
  onOpenChange?: (open: boolean) => void
}) {
  const afterClose = useRef<(() => void) | null>(null)
  const activityId = useId()
  const runningCount = sendOptions?.backgroundWork?.items.length ?? 0
  const rowClass = 'flex cursor-pointer items-center gap-2.5 rounded-md px-2.5 py-2 text-sm outline-none data-[highlighted]:bg-muted data-[disabled]:pointer-events-none data-[disabled]:opacity-50'
  return <Menu.Root modal={false} onOpenChange={onOpenChange}>
    <Menu.Trigger type="button" aria-label="Add files and actions" title="Add files and actions" aria-describedby={runningCount ? activityId : undefined}
      className="relative inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-full text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring data-[state=open]:bg-muted">
      {uploading ? <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" /> : <Plus className="h-4 w-4" aria-hidden="true" />}
      {runningCount > 0 && <span data-testid="chat-background-activity" className="absolute -right-0.5 -top-0.5 h-1.5 w-1.5 rounded-full bg-blue-500 ring-2 ring-background" aria-hidden="true" />}
      {runningCount > 0 && <span id={activityId} className="sr-only">{runningCount} background tasks running</span>}
    </Menu.Trigger>
    <Menu.Portal><Menu.Content side="top" align="start" sideOffset={8} collisionPadding={12}
      onCloseAutoFocus={event => {
        const action = afterClose.current
        afterClose.current = null
        if (action) { event.preventDefault(); action() }
      }}
      className="z-[100] max-h-[min(22rem,60dvh)] w-[min(320px,calc(100vw-24px))] overflow-y-auto rounded-xl border border-border bg-popover p-1.5 text-popover-foreground shadow-lg">
      <Menu.Item disabled={uploading} onSelect={onAttach} data-testid="chat-upload-button" className={rowClass}>
        <Paperclip className="h-4 w-4" aria-hidden="true" />{uploading ? 'Uploading files…' : 'Attach files'}
      </Menu.Item>
      {onOpenCommands && <Menu.Item data-testid="chat-command-menu-button" onSelect={() => { afterClose.current = onOpenCommands }} className={rowClass}>
        <Wand2 className="h-4 w-4" aria-hidden="true" />Slash commands
      </Menu.Item>}
      {showAgents && <ReachableAgentsMenu submenu />}
      {onOpenLiveView && <>
        <Menu.Separator className="my-1 h-px bg-border" />
        <Menu.Item onSelect={() => { afterClose.current = onOpenLiveView }} title={liveViewTitle} className={rowClass}>
          <Terminal className="h-4 w-4" aria-hidden="true" />Open live view
        </Menu.Item>
      </>}
      {sendOptions && <>
        <Menu.Separator className="my-1.5 h-px bg-border" />
        <ChatSendOptions {...sendOptions} deferAction={action => { afterClose.current = action }} />
      </>}
    </Menu.Content></Menu.Portal>
  </Menu.Root>
}
