import type { ReactNode } from 'react'
import { Keyboard } from 'lucide-react'

const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/i.test(navigator.platform || navigator.userAgent || '')

function Key({ children }: { children: ReactNode }) {
  return <kbd className="rounded border border-border bg-background px-1 py-px font-mono text-[10px] text-foreground/80">{children}</kbd>
}

/**
 * The keys worth knowing, shown on the start card of a new Code project, Crew
 * or workflow (owner 2026-10-06). It disappears with the start card once the
 * chat has a message; the full list is in the account menu.
 */
export function ShortcutHint({ extra }: { extra?: ReactNode }) {
  return <p className="mt-4 flex flex-wrap items-center gap-x-1.5 gap-y-1 border-t border-border pt-3 text-xs leading-5 text-muted-foreground" data-testid="shortcut-hint">
    <Keyboard className="h-3.5 w-3.5 shrink-0" aria-hidden />
    <span><Key>{isMac ? '⌘K' : 'Ctrl+K'}</Key> jump to any product, project or chat</span>
    <span aria-hidden>·</span>
    <span><Key>Esc</Key> stop a running chat</span>
    <span aria-hidden>·</span>
    <span><Key>Shift+Enter</Key> new line</span>
    {extra && <><span aria-hidden>·</span>{extra}</>}
  </p>
}

export function NewTabHint() {
  return <span><Key>New tab</Key> another chat in this project</span>
}
