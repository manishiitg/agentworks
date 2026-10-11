import * as Select from '@radix-ui/react-select'
import { ArrowUp, Check, ChevronDown, Clock } from 'lucide-react'
import { Button } from '../ui/Button'

export type ChatDeliveryMode = 'steer' | 'queue'

/** Selection expresses intent only; the server chooses delivery using live state. */
export function ChatSendModeButton({ mode, busy, disabled, onModeChange, onSend }: {
  mode: ChatDeliveryMode
  busy: boolean
  disabled: boolean
  onModeChange: (mode: ChatDeliveryMode) => void
  onSend: () => void
}) {
  const label = busy ? (mode === 'queue' ? 'Queue next' : 'Steer') : 'Send'
  const Icon = busy && mode === 'queue' ? Clock : ArrowUp
  return <div className="inline-flex shrink-0 rounded-md bg-primary text-primary-foreground">
    <Button type="button" onClick={onSend} disabled={disabled} aria-label={label} data-testid="chat-submit-button"
      className="h-7 gap-1.5 rounded-r-none px-2.5 text-xs">
      <Icon className="h-3.5 w-3.5" />{label}
    </Button>
    <Select.Root value={mode} onValueChange={value => onModeChange(value as ChatDeliveryMode)}>
      <Select.Trigger aria-label={`Choose how to send while the agent is working: ${mode === 'queue' ? 'Queue next' : 'Steer'}`}
        className="flex h-7 items-center rounded-r-md border-l border-primary-foreground/25 px-1.5 hover:bg-primary/80 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <ChevronDown className="h-3.5 w-3.5" />
      </Select.Trigger>
      <Select.Portal>
        <Select.Content position="popper" side="top" align="end" sideOffset={8}
          className="z-[100] w-[min(320px,calc(100vw-24px))] rounded-lg border border-border bg-popover p-1.5 text-popover-foreground shadow-lg">
          <Select.Viewport>
            <Select.Group>
              <Select.Label className="px-2.5 pb-2 pt-1 text-xs text-muted-foreground">Send while agent is working</Select.Label>
              {([
                ['steer', 'Steer', 'Add this instruction to the current task', ArrowUp],
                ['queue', 'Queue next', 'Finish this task, then answer separately', Clock],
              ] as const).map(([value, title, description, RowIcon]) => <Select.Item key={value} value={value}
                className="relative cursor-pointer rounded-md px-2.5 py-2 outline-none data-[state=checked]:bg-primary/15 data-[highlighted]:bg-accent">
                <div className="flex items-center gap-2 text-sm">
                  <RowIcon className="h-4 w-4 shrink-0" /><Select.ItemText>{title}</Select.ItemText>
                  {value === 'steer' && <span className="ml-auto text-[10px] text-muted-foreground">Default</span>}
                  <Select.ItemIndicator><Check className="h-4 w-4 text-primary" /></Select.ItemIndicator>
                </div>
                <p className="ml-6 mt-0.5 text-xs text-muted-foreground">{description}</p>
              </Select.Item>)}
            </Select.Group>
          </Select.Viewport>
        </Select.Content>
      </Select.Portal>
    </Select.Root>
  </div>
}
