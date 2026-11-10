import { chatTabSwitchKeys } from '../../products/work/workTabs'
import { useId, useRef } from 'react'
import * as Menu from '@radix-ui/react-dropdown-menu'
import { ArrowUp, ChevronDown, ChevronRight, RotateCcw, Square, Wand2 } from 'lucide-react'
import { Button } from '../ui/Button'
import { BackgroundWorkList } from '../BackgroundWorkPill'
import type { useSessionBackgroundWork } from '../../hooks/useSessionBackgroundWork'

export type ChatSendOptionsProps = {
  busy: boolean
  onOpenCommands?: () => void
  onNewChat?: () => void
  onStop?: () => void
  backgroundWork?: ReturnType<typeof useSessionBackgroundWork>
}

/** While the agent works, Enter steers the current task and Cmd/Ctrl+Enter queues the message as the next one (owner, 10 Nov:
 * one default, no mode setting). */
export const SEND_SHORTCUTS_BUSY = 'Enter to steer · ⌘/Ctrl+Enter to queue next · Shift+Enter for a new line'
export const SEND_SHORTCUTS_IDLE = 'Enter to send · Shift+Enter for a new line'

export function ChatSendOptions({ busy, onOpenCommands, onNewChat, onStop, backgroundWork, deferAction }: ChatSendOptionsProps & { deferAction: (action: () => void) => void }) {
  const runningCount = backgroundWork?.items.length ?? 0
  return <>
    {(onNewChat || onOpenCommands || onStop) && <>
      {onNewChat && <Menu.Item data-testid="chat-new-chat" title={busy ? 'Stop this chat and start it over' : 'Start this chat over. Earlier messages stay in history'} onSelect={() => { deferAction(onNewChat) }}
        className="flex cursor-pointer items-center gap-2 rounded-md px-2.5 py-2 text-sm outline-none data-[highlighted]:bg-accent">
        <RotateCcw className="h-4 w-4" aria-hidden="true" />Clear chat
      </Menu.Item>}
      {onOpenCommands && <Menu.Item data-testid="chat-command-menu-button" onSelect={() => { deferAction(onOpenCommands) }}
        className="flex cursor-pointer items-center gap-2 rounded-md px-2.5 py-2 text-sm outline-none data-[highlighted]:bg-accent">
        <Wand2 className="h-4 w-4" aria-hidden="true" />Slash commands
      </Menu.Item>}
      {onStop && <Menu.Item data-testid="chat-stop-button" onSelect={onStop}
        className="flex cursor-pointer items-center gap-2 rounded-md px-2.5 py-2 text-sm outline-none data-[highlighted]:bg-destructive/10 data-[highlighted]:text-destructive">
        <Square className="h-3.5 w-3.5" fill="currentColor" aria-hidden="true" />Stop current turn
      </Menu.Item>}
    </>}
    {runningCount > 0 && backgroundWork && <>
      <Menu.Separator className="my-1.5 h-px bg-border" />
      <Menu.Sub>
        <Menu.SubTrigger className="flex cursor-pointer items-center gap-2 rounded-md px-2.5 py-2 text-sm outline-none data-[highlighted]:bg-accent data-[state=open]:bg-accent">
          <span className="h-1.5 w-1.5 rounded-full bg-blue-500" aria-hidden="true" />{runningCount} running
          <ChevronRight className="ml-auto h-3.5 w-3.5" aria-hidden="true" />
        </Menu.SubTrigger>
        <Menu.Portal>
          <Menu.SubContent sideOffset={6} collisionPadding={12} className="z-[101] max-h-[var(--radix-dropdown-menu-content-available-height)] w-[min(22rem,calc(100vw-24px))] overflow-y-auto overscroll-contain scroll-py-1.5 rounded-lg border border-border bg-popover p-1.5 text-popover-foreground shadow-lg">
            <Menu.Label className="px-2 pb-2 pt-1 text-xs text-muted-foreground">Background work</Menu.Label>
            <BackgroundWorkList work={backgroundWork} renderAction={(button, options) =>
              <Menu.Item asChild disabled={options?.disabled} onSelect={options?.keepOpen ? event => event.preventDefault() : undefined}>{button}</Menu.Item>} />
          </Menu.SubContent>
        </Menu.Portal>
      </Menu.Sub>
    </>}
    <ComposerKeyboardShortcuts />
  </>
}

/** Fixed reference, not a rotating hint or a usage tracker. */
function ComposerKeyboardShortcuts() {
  const mac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform)
  const command = mac ? '⌘' : 'Ctrl+'
  const tabs = chatTabSwitchKeys()
  const shortcuts = [
    ['Enter', 'Send · steer while working'],
    [`${command}Enter`, 'Queue next while working'],
    ['Shift+Enter', 'New line'],
    ['/', 'Commands'], ['@', 'Files'], ['#', 'Crew references'],
    [`${command}← / ${command}→`, 'Back / forward · outside text fields'],
    [mac ? tabs.mac : tabs.other, 'Switch agents in this project'],
    [`${command}K`, 'Search'],
    [mac ? '⌥⇧T' : 'Alt+Shift+T', 'New agent'],
  ]
  return <section aria-label="Keyboard shortcuts" className="mt-1.5 border-t border-border px-2.5 pb-1 pt-2 text-[10px] text-muted-foreground">
    <h3 className="mb-1 font-medium">Keyboard shortcuts</h3>
    <dl className="space-y-1">
      {shortcuts.map(([keys, label]) => <div key={keys} className="flex items-baseline justify-between gap-3">
        <dt><kbd className="whitespace-nowrap font-mono">{keys}</kbd></dt>
        <dd className="text-right">{label}</dd>
      </div>)}
    </dl>
  </section>
}

/** Selection expresses intent only; the server chooses delivery using live state. */
export function ChatSendModeButton({ busy, disabled, onSend, onOpenCommands, onNewChat, onStop, backgroundWork, onOpenChange, showOptions = true }: {
  busy: boolean
  disabled: boolean
  onSend: () => void
  onOpenCommands?: () => void
  onNewChat?: () => void
  onStop?: () => void
  backgroundWork?: ReturnType<typeof useSessionBackgroundWork>
  onOpenChange?: (open: boolean) => void
  showOptions?: boolean
}) {
  const afterClose = useRef<(() => void) | null>(null)
  const activityId = useId()
  const runningCount = backgroundWork?.items.length ?? 0
  const label = busy ? 'Steer' : 'Send'
  return <div className="inline-flex shrink-0 rounded-md border border-border bg-transparent">
    <Button type="button" variant="ghost" size="xs" onClick={onSend} disabled={disabled} aria-label={label}
      title={busy ? 'Steer · ⌘/Ctrl+Enter to queue next' : 'Send'} data-testid="chat-submit-button"
      className={`w-7 px-0 text-primary hover:bg-muted [&_svg]:size-3.5 ${showOptions ? 'rounded-r-none' : ''}`}>
      <ArrowUp className="h-3.5 w-3.5" aria-hidden="true" />
    </Button>
    {showOptions && <Menu.Root onOpenChange={onOpenChange}>
      <Menu.Trigger aria-label="Send options"
        aria-describedby={runningCount > 0 ? activityId : undefined}
        title={`Send options${runningCount > 0 ? ` · ${runningCount} background tasks running` : ''}`}
        className="relative flex h-6 w-5 items-center justify-center rounded-r-md border-l border-border text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <ChevronDown className="h-3 w-3" aria-hidden="true" />
        {runningCount > 0 && <span data-testid="chat-background-activity" className="absolute -right-0.5 -top-0.5 h-1.5 w-1.5 rounded-full bg-blue-500 ring-2 ring-background" aria-hidden="true" />}
        {runningCount > 0 && <span id={activityId} className="sr-only">{runningCount} background tasks running</span>}
      </Menu.Trigger>
      <Menu.Portal>
        <Menu.Content side="top" align="end" sideOffset={8} collisionPadding={12}
          onCloseAutoFocus={event => {
            const action = afterClose.current
            afterClose.current = null
            if (!action) return
            event.preventDefault()
            action()
          }}
          className="z-[100] max-h-[var(--radix-dropdown-menu-content-available-height)] w-[min(320px,calc(100vw-24px))] overflow-y-auto overscroll-contain scroll-py-1.5 rounded-lg border border-border bg-popover p-1.5 text-popover-foreground shadow-lg">
            <ChatSendOptions busy={busy} onOpenCommands={onOpenCommands}
              onNewChat={onNewChat} onStop={onStop} backgroundWork={backgroundWork}
              deferAction={action => { afterClose.current = action }} />
        </Menu.Content>
      </Menu.Portal>
    </Menu.Root>}
  </div>
}
