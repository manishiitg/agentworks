import { ChevronDown, ChevronUp, Loader2, Maximize2, Minimize2, Paperclip, Wand2 } from 'lucide-react'
import { Button } from './ui/Button'

interface NativeTerminalToolbarProps {
  className?: string
  expanded: boolean
  uploading: boolean
  composerId: string
  focused?: boolean
  onToggleFocus?: () => void
  onCommands: () => void
  onAttach: () => void
  onToggleComposer: () => void
  onReturnToChat: () => void
}

export function NativeTerminalToolbar({ className = '', expanded, uploading, composerId, focused = false, onToggleFocus, onCommands, onAttach, onToggleComposer, onReturnToChat }: NativeTerminalToolbarProps) {
  return (
    <div className={`flex flex-wrap items-center gap-2 py-2 text-xs text-muted-foreground ${className}`} data-testid="native-terminal-toolbar">
      <span className="mr-auto">Type directly in the terminal</span>
      <Button type="button" variant="ghost" size="sm" onClick={onCommands} disabled={uploading} aria-label="Browse commands">
        <Wand2 className="h-3.5 w-3.5" />
        Commands
      </Button>
      <Button type="button" variant="ghost" size="sm" onClick={onAttach} disabled={uploading} aria-label="Attach files">
        {uploading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Paperclip className="h-3.5 w-3.5" />}
        {uploading ? 'Uploading…' : 'Attach'}
      </Button>
      <Button type="button" variant="ghost" size="icon" className="h-7 w-7" onClick={onToggleComposer}
        aria-expanded={expanded} aria-controls={composerId}
        aria-label={expanded ? 'Hide message composer' : 'Show message composer'}>
        {expanded ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronUp className="h-3.5 w-3.5" />}
      </Button>
      {onToggleFocus && (
        <Button type="button" variant={focused ? 'secondary' : 'ghost'} size="sm" onClick={onToggleFocus} aria-pressed={focused}
          aria-label={focused ? 'Exit focus mode' : 'Enter focus mode'}
          title={focused ? 'Restore the top header and toolbar' : 'Hide the top header and toolbar; keep both panes visible'}>
          {focused ? <Minimize2 className="h-3.5 w-3.5" /> : <Maximize2 className="h-3.5 w-3.5" />}
          {focused ? 'Exit focus' : 'Focus mode'}
        </Button>
      )}
      <Button type="button" variant="outline" size="sm" onClick={onReturnToChat}>Return to chat</Button>
    </div>
  )
}
