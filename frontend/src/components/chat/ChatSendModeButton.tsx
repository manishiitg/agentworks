import { useRef } from 'react'
import * as Menu from '@radix-ui/react-dropdown-menu'
import { ArrowUp, Check, ChevronDown, Clock, Wand2 } from 'lucide-react'
import { Button } from '../ui/Button'

export type ChatDeliveryMode = 'steer' | 'queue'

/** Selection expresses intent only; the server chooses delivery using live state. */
export function ChatSendModeButton({ mode, busy, disabled, onModeChange, onSend, onOpenCommands }: {
  mode: ChatDeliveryMode
  busy: boolean
  disabled: boolean
  onModeChange: (mode: ChatDeliveryMode) => void
  onSend: () => void
  onOpenCommands?: () => void
}) {
  const openCommandsAfterClose = useRef(false)
  const label = busy ? (mode === 'queue' ? 'Queue next' : 'Steer') : 'Send'
  const modeLabel = mode === 'queue' ? 'Queue next' : 'Steer'
  const Icon = mode === 'queue' ? Clock : ArrowUp
  return <div className="inline-flex shrink-0 rounded-md border border-border bg-transparent">
    <Button type="button" variant="ghost" size="xs" onClick={onSend} disabled={disabled} aria-label={label}
      title={busy ? label : `Send · ${modeLabel} while agent is working`} data-testid="chat-submit-button"
      className="w-6 rounded-r-none px-0 text-primary hover:bg-muted [&_svg]:size-3.5">
      <Icon className="h-3.5 w-3.5" aria-hidden="true" />
    </Button>
    <Menu.Root>
      <Menu.Trigger aria-label={`Send options: ${modeLabel}`}
        title={`Delivery mode: ${modeLabel}`}
        className="flex h-6 w-5 items-center justify-center rounded-r-md border-l border-border text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <ChevronDown className="h-3 w-3" aria-hidden="true" />
      </Menu.Trigger>
      <Menu.Portal>
        <Menu.Content side="top" align="end" sideOffset={8}
          onCloseAutoFocus={event => {
            if (!openCommandsAfterClose.current) return
            openCommandsAfterClose.current = false
            event.preventDefault()
            onOpenCommands?.()
          }}
          className="z-[100] w-[min(320px,calc(100vw-24px))] rounded-lg border border-border bg-popover p-1.5 text-popover-foreground shadow-lg">
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
            {onOpenCommands && <>
              <Menu.Separator className="my-1.5 h-px bg-border" />
              <Menu.Item data-testid="chat-command-menu-button" onSelect={() => { openCommandsAfterClose.current = true }}
                className="flex cursor-pointer items-center gap-2 rounded-md px-2.5 py-2 text-sm outline-none data-[highlighted]:bg-accent">
                <Wand2 className="h-4 w-4" aria-hidden="true" />Slash commands
              </Menu.Item>
            </>}
            <p className="mt-1.5 border-t border-border px-2.5 pb-1 pt-2 text-[10px] text-muted-foreground">
              Enter to {label.toLowerCase()} · Shift+Enter for a new line
            </p>
        </Menu.Content>
      </Menu.Portal>
    </Menu.Root>
  </div>
}
