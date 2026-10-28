import { useEffect, useRef, useState } from 'react'
import { MessageSquarePlus } from 'lucide-react'
import { Button } from './Button'

interface NewChatControlProps {
  /** Engine ids offered for a fresh chat, in order. */
  engines: { id: string; label: string }[]
  onStart: (engineId: string) => void
  disabled?: boolean
}

/**
 * "New chat" for a product surface. With one engine it starts immediately;
 * with more than one it asks first — a fresh chat is the one moment the
 * engine can be chosen at all (locked for the rest of that chat's life), so
 * asking here rather than defaulting silently is the whole point.
 */
export default function NewChatControl({ engines, onStart, disabled }: NewChatControlProps) {
  const [open, setOpen] = useState(false)
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const onMouseDown = (event: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) setOpen(false)
    }
    const onKeyDown = (event: KeyboardEvent) => { if (event.key === 'Escape') setOpen(false) }
    document.addEventListener('mousedown', onMouseDown)
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('mousedown', onMouseDown)
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [open])

  if (engines.length === 0) return null

  return (
    <div ref={containerRef} className="relative inline-block">
      <Button
        type="button"
        variant="ghost"
        size="sm"
        className="h-7 w-7 p-0"
        aria-label="New chat"
        title="Start a new chat — this one stays in history"
        disabled={disabled}
        onClick={() => (engines.length > 1 ? setOpen((v) => !v) : onStart(engines[0].id))}
      >
        <MessageSquarePlus className="w-3.5 h-3.5" />
      </Button>
      {open && (
        // Explicit arbitrary colors, not the ambient bg-popover/text-foreground
        // tokens: those follow <html>'s theme class, which a product surface's
        // own chat area does not follow for its own elements (its CSS renders
        // light regardless via its own variables), so a genuinely-dark <html>
        // class produced a black panel with illegible hover text over an
        // otherwise light chat. Same class of bug as MicButton's earlier fix.
        <div className="new-chat-panel absolute bottom-full left-0 mb-2 w-48 rounded-xl border border-[#d7e0ec] dark:border-border bg-white dark:bg-popover p-2 shadow-lg z-50">
          <div className="text-[11px] font-medium text-[#5f708d] dark:text-[#a2a7b0] px-1 pb-1">Start a new chat with</div>
          {engines.map((engine) => (
            <button
              key={engine.id}
              type="button"
              className="w-full text-left rounded-md px-2 py-1.5 text-sm text-[#1f2937] dark:text-[#e4e7ec] hover:bg-[#f3f6fb] dark:hover:bg-[#1c1f24]"
              onClick={() => { setOpen(false); onStart(engine.id) }}
            >
              {engine.label}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
