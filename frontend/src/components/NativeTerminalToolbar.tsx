import { ArrowLeft, Loader2, Maximize2, Minimize2, Paperclip } from 'lucide-react'
import { Button, type ButtonProps } from './ui/Button'
import { Tooltip, TooltipContent, TooltipTrigger } from './ui/tooltip'
import type { Ref } from 'react'

interface NativeTerminalToolbarProps {
  className?: string
  uploading: boolean
  focused?: boolean
  onToggleFocus?: () => void
  onAttach: () => void
  onReturnToChat: () => void
}

function TerminalToolButton({ label, buttonRef, children, ...props }: ButtonProps & { label: string; buttonRef?: Ref<HTMLButtonElement> }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button ref={buttonRef} type="button" variant="ghost" size="icon" className="h-6 w-6" aria-label={label} {...props}>
          {children}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  )
}

export function NativeTerminalToolbar({ className = '', uploading, focused = false, onToggleFocus, onAttach, onReturnToChat }: NativeTerminalToolbarProps) {
  return (
    <div className={`flex flex-wrap items-center gap-1.5 py-1 text-xs text-muted-foreground ${className}`} data-testid="native-terminal-toolbar">
      <TerminalToolButton label="Return to chat" variant="outline" className="h-6 w-6 shrink-0" onClick={onReturnToChat}>
        <ArrowLeft className="h-3.5 w-3.5" />
      </TerminalToolButton>
      <span className="mr-auto" />
      <TerminalToolButton label={uploading ? 'Uploading files…' : 'Attach files'} onClick={onAttach} disabled={uploading}>
        {uploading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Paperclip className="h-3.5 w-3.5" />}
      </TerminalToolButton>
      {onToggleFocus && (
        <TerminalToolButton label={focused ? 'Exit focus mode' : 'Enter focus mode'} variant={focused ? 'secondary' : 'ghost'} onClick={onToggleFocus} aria-pressed={focused}>
          {focused ? <Minimize2 className="h-3.5 w-3.5" /> : <Maximize2 className="h-3.5 w-3.5" />}
        </TerminalToolButton>
      )}
    </div>
  )
}
