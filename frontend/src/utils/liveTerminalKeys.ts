export type LiveTerminalControlKey = 'Up' | 'Down' | 'Enter' | 'Escape'

type KeyboardEventLike = {
  key: string
  shiftKey?: boolean
  ctrlKey?: boolean
  metaKey?: boolean
  altKey?: boolean
}

const LIVE_TERMINAL_KEYS: Record<string, LiveTerminalControlKey> = {
  ArrowUp: 'Up',
  ArrowDown: 'Down',
  Enter: 'Enter',
  Escape: 'Escape',
}

// Empty chat input acts as a keyboard bridge for an attached coding CLI.
// Text or attachments keep normal composer semantics: Enter submits it and arrow keys
// move within it. Modified keys remain owned by the browser/composer.
export function liveTerminalControlKey(
  event: KeyboardEventLike,
  input: string | null | undefined,
  hasAttachments = false,
): LiveTerminalControlKey | null {
  if (event.shiftKey || event.ctrlKey || event.metaKey || event.altKey) return null
  // Escape controls/interrupts the CLI regardless of a draft in the composer.
  // Enter and arrows retain normal editing/submission behavior once text exists.
  if (event.key === 'Escape') return 'Escape'
  if (input?.trim() || hasAttachments) return null
  return LIVE_TERMINAL_KEYS[event.key] ?? null
}
