import { useRef } from 'react'
import * as Menu from '@radix-ui/react-dropdown-menu'
import { ArrowUp, ChevronDown, RotateCcw, Square, Wand2 } from 'lucide-react'
import { Button } from '../ui/Button'

export type ChatSendOptionsProps = {
  busy: boolean
  onOpenCommands?: () => void
  onNewChat?: () => void
  onStop?: () => void
}

/** Shown beside the action a key triggers, like a native menu. */
function MenuKey({ children }: { children: string }) {
  return <kbd className="ml-auto pl-3 font-mono text-xs text-muted-foreground">{children}</kbd>
}

export function ChatSendOptions({ busy, onOpenCommands, onNewChat, onStop, deferAction }: ChatSendOptionsProps & { deferAction: (action: () => void) => void }) {
  return <>
    {(onNewChat || onOpenCommands || onStop) && <>
      {onNewChat && <Menu.Item data-testid="chat-new-chat" title={busy ? 'Stop this chat and start it over' : 'Start this chat over. Earlier messages stay in history'} onSelect={() => { deferAction(onNewChat) }}
        className="flex cursor-pointer items-center gap-2 rounded-md px-2.5 py-2 text-sm outline-none data-[highlighted]:bg-accent">
        <RotateCcw className="h-4 w-4" aria-hidden="true" />Clear chat
      </Menu.Item>}
      {onOpenCommands && <Menu.Item data-testid="chat-command-menu-button" onSelect={() => { deferAction(onOpenCommands) }}
        className="flex cursor-pointer items-center gap-2 rounded-md px-2.5 py-2 text-sm outline-none data-[highlighted]:bg-accent">
        <Wand2 className="h-4 w-4" aria-hidden="true" />Slash commands<MenuKey>/</MenuKey>
      </Menu.Item>}
      {onStop && <Menu.Item data-testid="chat-stop-button" onSelect={onStop}
        className="flex cursor-pointer items-center gap-2 rounded-md px-2.5 py-2 text-sm outline-none data-[highlighted]:bg-destructive/10 data-[highlighted]:text-destructive">
        <Square className="h-3.5 w-3.5" fill="currentColor" aria-hidden="true" />Stop current turn<MenuKey>Esc</MenuKey>
      </Menu.Item>}
    </>}
  </>
}

/** The bottom lines of every composer menu: message-box keys that have no menu item of their own. Queue, recall and
 * Esc only while work is active (Stop is the button beside Send) (PLAT-746-849). */
export function ComposerSendKeys({ busy, separated = true }: { busy: boolean; separated?: boolean }) {
  const mac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform)
  const keys: Array<[string, string]> = [
    ['↵', busy ? 'steer' : 'send'],
    ...(busy ? [[mac ? '⌘↵' : 'Ctrl+↵', 'queue next'] as [string, string]] : []),
    [mac ? '⇧↵' : 'Shift+↵', 'new line'],
    ...(busy ? [['↑', 'edit queued'] as [string, string], ['Esc', 'stop'] as [string, string]] : []),
    ['@', 'add a file'],
    [mac ? '⌘← →' : 'Ctrl+← →', 'switch agent'],
    [mac ? '⌘K' : 'Ctrl+K', 'search everything'],
  ]
  return <p data-testid="composer-send-keys" className={`flex flex-wrap gap-x-3 gap-y-1 px-2.5 text-xs text-muted-foreground ${separated ? 'mt-1.5 border-t border-border pb-0.5 pt-2' : 'pb-2'}`}>
    {keys.map(([key, label]) => <span key={key} className="whitespace-nowrap"><kbd className="font-mono text-foreground/80">{key}</kbd> {label}</span>)}
  </p>
}

/** Selection expresses intent only; the server chooses delivery using live state. */
export function ChatSendModeButton({ busy, disabled, onSend, onOpenCommands, onNewChat, onStop, onOpenChange, showOptions = true }: {
  busy: boolean
  disabled: boolean
  onSend: () => void
  onOpenCommands?: () => void
  onNewChat?: () => void
  onStop?: () => void
  onOpenChange?: (open: boolean) => void
  showOptions?: boolean
}) {
  const afterClose = useRef<(() => void) | null>(null)
  const label = busy ? 'Steer' : 'Send'
  return <div className="inline-flex shrink-0 rounded-md border border-border bg-transparent">
    <Button type="button" variant="ghost" size="xs" onClick={onSend} disabled={disabled} aria-label={label}
      title={busy ? 'Steer · ⌘/Ctrl+Enter to queue next' : 'Send'} data-testid="chat-submit-button"
      className={`w-7 px-0 text-primary hover:bg-muted [&_svg]:size-3.5 ${showOptions ? 'rounded-r-none' : ''}`}>
      <ArrowUp className="h-3.5 w-3.5" aria-hidden="true" />
    </Button>
    {showOptions && <Menu.Root onOpenChange={onOpenChange}>
      <Menu.Trigger aria-label="Send options"
        title="Send options"
        className="relative flex h-6 w-5 items-center justify-center rounded-r-md border-l border-border text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <ChevronDown className="h-3 w-3" aria-hidden="true" />
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
              onNewChat={onNewChat} onStop={onStop}
              deferAction={action => { afterClose.current = action }} />
            <ComposerSendKeys busy={busy} />
        </Menu.Content>
      </Menu.Portal>
    </Menu.Root>}
  </div>
}
