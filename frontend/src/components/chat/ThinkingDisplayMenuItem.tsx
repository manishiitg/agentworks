import * as Menu from '@radix-ui/react-dropdown-menu'
import { Brain, Check, Wrench } from 'lucide-react'
import { useChatDisplayStore } from '../../stores/useChatDisplayStore'

export function ThinkingDisplayMenuItem({ className, compact = false }: { className: string; compact?: boolean }) {
  const show = useChatDisplayStore(state => state.showThinking)
  const setShow = useChatDisplayStore(state => state.setShowThinking)
  return <Menu.CheckboxItem className={className} checked={show} onCheckedChange={setShow} onSelect={event => event.preventDefault()} title="Show the thinking text supplied by your coding agent directly in chat, when available.">
    {!compact && <Brain aria-hidden="true" className="h-3.5 w-3.5 text-muted-foreground" />}
    <span className="flex-1">{compact ? 'Thinking' : 'Show thinking'}</span>
    <span className={compact ? 'flex h-3.5 w-3.5 items-center justify-center rounded-sm border border-muted-foreground/50' : undefined}><Menu.ItemIndicator><Check aria-hidden="true" className="h-3 w-3" /></Menu.ItemIndicator></span>
  </Menu.CheckboxItem>
}

export function ToolsDisplayMenuItem({ className, compact = false }: { className: string; compact?: boolean }) {
  const show = useChatDisplayStore(state => state.showTools)
  const setShow = useChatDisplayStore(state => state.setShowTools)
  return <Menu.CheckboxItem className={className} checked={show} onCheckedChange={setShow} onSelect={event => event.preventDefault()} title="Allow tool calls to be opened. Groups start collapsed; off keeps only non-clickable progress and counts.">
    {!compact && <Wrench aria-hidden="true" className="h-3.5 w-3.5 text-muted-foreground" />}
    <span className="flex-1">{compact ? 'Tool calls' : 'Show tool calls'}</span>
    <span className={compact ? 'flex h-3.5 w-3.5 items-center justify-center rounded-sm border border-muted-foreground/50' : undefined}><Menu.ItemIndicator><Check aria-hidden="true" className="h-3 w-3" /></Menu.ItemIndicator></span>
  </Menu.CheckboxItem>
}
