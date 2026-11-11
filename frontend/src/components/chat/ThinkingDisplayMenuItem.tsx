import * as Menu from '@radix-ui/react-dropdown-menu'
import { Brain, Check, Wrench } from 'lucide-react'
import { useChatDisplayStore } from '../../stores/useChatDisplayStore'

export function ThinkingDisplayMenuItem({ className }: { className: string }) {
  const show = useChatDisplayStore(state => state.showThinking)
  const setShow = useChatDisplayStore(state => state.setShowThinking)
  return <Menu.CheckboxItem className={className} checked={show} onCheckedChange={setShow} onSelect={event => event.preventDefault()} title="Show the thinking text supplied by your coding agent directly in chat, when available.">
    <Brain aria-hidden="true" className="h-3.5 w-3.5 text-muted-foreground" />
    <span className="flex-1">Show thinking</span>
    <Menu.ItemIndicator><Check aria-hidden="true" className="h-3.5 w-3.5" /></Menu.ItemIndicator>
  </Menu.CheckboxItem>
}

export function ToolsDisplayMenuItem({ className }: { className: string }) {
  const show = useChatDisplayStore(state => state.showTools)
  const setShow = useChatDisplayStore(state => state.setShowTools)
  return <Menu.CheckboxItem className={className} checked={show} onCheckedChange={setShow} onSelect={event => event.preventDefault()} title="Show individual tools in chat. Off shows a compact count you can open when needed.">
    <Wrench aria-hidden="true" className="h-3.5 w-3.5 text-muted-foreground" />
    <span className="flex-1">Show tools</span>
    <Menu.ItemIndicator><Check aria-hidden="true" className="h-3.5 w-3.5" /></Menu.ItemIndicator>
  </Menu.CheckboxItem>
}
