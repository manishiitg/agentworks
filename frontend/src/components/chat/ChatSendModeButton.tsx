import { useId, useRef } from 'react'
import * as Menu from '@radix-ui/react-dropdown-menu'
import { ArrowUp, Check, ChevronDown, ChevronRight, Clock, RotateCcw, Square, Wand2 } from 'lucide-react'
import { Button } from '../ui/Button'
import { BackgroundWorkList } from '../BackgroundWorkPill'
import type { useSessionBackgroundWork } from '../../hooks/useSessionBackgroundWork'

export type ChatDeliveryMode = 'steer' | 'queue'

export type ChatSendOptionsProps = {
  mode: ChatDeliveryMode
  busy: boolean
  onModeChange: (mode: ChatDeliveryMode) => void
  onOpenCommands?: () => void
  onNewChat?: () => void
  onStop?: () => void
  backgroundWork?: ReturnType<typeof useSessionBackgroundWork>
}

export function ChatSendOptions({ mode, busy, onModeChange, onOpenCommands, onNewChat, onStop, backgroundWork, deferAction }: ChatSendOptionsProps & { deferAction: (action: () => void) => void }) {
  const runningCount = backgroundWork?.items.length ?? 0
  const label = busy ? (mode === 'queue' ? 'Queue next' : 'Steer') : 'Send'
  return <>
    <Menu.Label className="px-2.5 pb-2 pt-1 text-xs text-muted-foreground">Send while agent is working</Menu.Label>
    <Menu.RadioGroup value={mode} onValueChange={value => onModeChange(value as ChatDeliveryMode)}>
      {([
        ['steer', 'Steer', 'Add this instruction to the current task', ArrowUp],
        ['queue', 'Queue next', 'Finish this task, then answer separately', Clock],
      ] as const).map(([value, title, description, RowIcon]) => <Menu.RadioItem key={value} value={value}
        className="relative cursor-pointer rounded-md px-2.5 py-2 outline-none data-[state=checked]:bg-primary/15 data-[highlighted]:bg-accent">
        <div className="flex items-center gap-2 text-sm">
          <RowIcon className="h-4 w-4 shrink-0" /><span>{title}</span>
          {value === 'steer' && <span className="ml-auto text-[10px] text-muted-foreground">Default</span>}
          <Menu.ItemIndicator><Check className="h-4 w-4 text-primary" /></Menu.ItemIndicator>
        </div>
        <p className="ml-6 mt-0.5 text-xs text-muted-foreground">{description}</p>
      </Menu.RadioItem>)}
    </Menu.RadioGroup>
    {(onNewChat || onOpenCommands || onStop) && <>
      <Menu.Separator className="my-1.5 h-px bg-border" />
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
          <Menu.SubContent sideOffset={6} collisionPadding={12} className="z-[101] w-[min(22rem,calc(100vw-24px))] rounded-lg border border-border bg-popover p-1.5 text-popover-foreground shadow-lg">
            <Menu.Label className="px-2 pb-2 pt-1 text-xs text-muted-foreground">Background work</Menu.Label>
            <BackgroundWorkList work={backgroundWork} renderAction={(button, options) =>
              <Menu.Item asChild disabled={options?.disabled} onSelect={options?.keepOpen ? event => event.preventDefault() : undefined}>{button}</Menu.Item>} />
          </Menu.SubContent>
        </Menu.Portal>
      </Menu.Sub>
    </>}
    <p className="mt-1.5 border-t border-border px-2.5 pb-1 pt-2 text-[10px] text-muted-foreground">
      Enter to {label.toLowerCase()} · Shift+Enter for a new line
    </p>
  </>
}

/** Selection expresses intent only; the server chooses delivery using live state. */
export function ChatSendModeButton({ mode, busy, disabled, onModeChange, onSend, onOpenCommands, onNewChat, onStop, backgroundWork, onOpenChange, showOptions = true }: {
  mode: ChatDeliveryMode
  busy: boolean
  disabled: boolean
  onModeChange: (mode: ChatDeliveryMode) => void
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
  const label = busy ? (mode === 'queue' ? 'Queue next' : 'Steer') : 'Send'
  const modeLabel = mode === 'queue' ? 'Queue next' : 'Steer'
  const Icon = mode === 'queue' ? Clock : ArrowUp
  return <div className="inline-flex shrink-0 rounded-md border border-border bg-transparent">
    <Button type="button" variant="ghost" size="xs" onClick={onSend} disabled={disabled} aria-label={label}
      title={busy ? label : `Send · ${modeLabel} while agent is working`} data-testid="chat-submit-button"
      className={`w-7 px-0 text-primary hover:bg-muted [&_svg]:size-3.5 ${showOptions ? 'rounded-r-none' : ''}`}>
      <Icon className="h-3.5 w-3.5" aria-hidden="true" />
    </Button>
    {showOptions && <Menu.Root onOpenChange={onOpenChange}>
      <Menu.Trigger aria-label={`Send options: ${modeLabel}`}
        aria-describedby={runningCount > 0 ? activityId : undefined}
        title={`Delivery mode: ${modeLabel}${runningCount > 0 ? ` · ${runningCount} background tasks running` : ''}`}
        className="relative flex h-6 w-5 items-center justify-center rounded-r-md border-l border-border text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <ChevronDown className="h-3 w-3" aria-hidden="true" />
        {runningCount > 0 && <span data-testid="chat-background-activity" className="absolute -right-0.5 -top-0.5 h-1.5 w-1.5 rounded-full bg-blue-500 ring-2 ring-background" aria-hidden="true" />}
        {runningCount > 0 && <span id={activityId} className="sr-only">{runningCount} background tasks running</span>}
      </Menu.Trigger>
      <Menu.Portal>
        <Menu.Content side="top" align="end" sideOffset={8}
          onCloseAutoFocus={event => {
            const action = afterClose.current
            afterClose.current = null
            if (!action) return
            event.preventDefault()
            action()
          }}
          className="z-[100] w-[min(320px,calc(100vw-24px))] rounded-lg border border-border bg-popover p-1.5 text-popover-foreground shadow-lg">
            <ChatSendOptions mode={mode} busy={busy} onModeChange={onModeChange} onOpenCommands={onOpenCommands}
              onNewChat={onNewChat} onStop={onStop} backgroundWork={backgroundWork}
              deferAction={action => { afterClose.current = action }} />
        </Menu.Content>
      </Menu.Portal>
    </Menu.Root>}
  </div>
}
