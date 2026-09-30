import { ChevronDown, ChevronUp, Loader2, Maximize2, MessageSquare, Minimize2, Paperclip, Wand2 } from 'lucide-react'
import { Button, type ButtonProps } from './ui/Button'
import { Tooltip, TooltipContent, TooltipTrigger } from './ui/tooltip'
import type { Ref } from 'react'

interface NativeTerminalToolbarProps {
  className?: string
  expanded: boolean
  uploading: boolean
  composerId: string
  commandsOpen?: boolean
  commandListId?: string
  commandButtonRef?: Ref<HTMLButtonElement>
  focused?: boolean
  onToggleFocus?: () => void
  onCommands: () => void
  onAttach: () => void
  onToggleComposer: () => void
  onReturnToChat: () => void
}

function TerminalToolButton({ label, buttonRef, children, ...props }: ButtonProps & { label: string; buttonRef?: Ref<HTMLButtonElement> }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button ref={buttonRef} type="button" variant="ghost" size="icon" className="h-8 w-8" aria-label={label} {...props}>
          {children}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  )
}

export function NativeTerminalToolbar({ className = '', expanded, uploading, composerId, commandsOpen = false, commandListId, commandButtonRef, focused = false, onToggleFocus, onCommands, onAttach, onToggleComposer, onReturnToChat }: NativeTerminalToolbarProps) {
  return (
    <div className={`flex flex-wrap items-center gap-2 py-2 text-xs text-muted-foreground ${className}`} data-testid="native-terminal-toolbar">
      <TerminalToolButton label="Return to chat" variant="outline" className="h-10 w-10 shrink-0 [&_svg]:size-5" onClick={onReturnToChat}>
        <MessageSquare className="h-5 w-5" />
      </TerminalToolButton>
      <span className="mr-auto">Type directly in the terminal</span>
      <TerminalToolButton label="Browse commands" buttonRef={commandButtonRef} variant={commandsOpen ? 'secondary' : 'ghost'} onClick={onCommands}
        disabled={uploading} aria-haspopup="listbox" aria-expanded={commandsOpen}
        aria-controls={commandsOpen ? commandListId : undefined}>
        <Wand2 className="h-4 w-4" />
      </TerminalToolButton>
      <TerminalToolButton label={uploading ? 'Uploading files…' : 'Attach files'} onClick={onAttach} disabled={uploading}>
        {uploading ? <Loader2 className="h-4 w-4 animate-spin" /> : <Paperclip className="h-4 w-4" />}
      </TerminalToolButton>
      <Button type="button" variant="ghost" size="icon" className="h-7 w-7" onClick={onToggleComposer}
        aria-expanded={expanded} aria-controls={composerId}
        aria-label={expanded ? 'Hide message composer' : 'Show message composer'}>
        {expanded ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronUp className="h-3.5 w-3.5" />}
      </Button>
      {onToggleFocus && (
        <TerminalToolButton label={focused ? 'Exit focus mode' : 'Enter focus mode'} variant={focused ? 'secondary' : 'ghost'} onClick={onToggleFocus} aria-pressed={focused}>
          {focused ? <Minimize2 className="h-4 w-4" /> : <Maximize2 className="h-4 w-4" />}
        </TerminalToolButton>
      )}
    </div>
  )
}
