import { useEffect, useRef, useState } from 'react'
import { MoreHorizontal } from 'lucide-react'
import type { GmailConnection } from '../../services/api-types'
import { changeGoogleAccountAccess, googleAccessSummary } from './googleAccountAccess'

/**
 * A Code's Google accounts, one plain row each: the email, what the agent may do in words, and
 * one button, Change access (the same Connect form, prefilled; signing in again replaces the old
 * connection). Less common actions sit in a More menu.
 */
export function GoogleAccountList({ connections, busyId, readOnly, canRemove, onSendTest, onToggle, onReconnect, onRemove }: {
  connections: GmailConnection[]
  busyId: string | null
  readOnly?: boolean
  canRemove?: (conn: GmailConnection) => boolean
  onSendTest: (conn: GmailConnection) => void
  onToggle: (conn: GmailConnection) => void
  onReconnect: (conn: GmailConnection) => void
  onRemove: (conn: GmailConnection) => void
}) {
  return (
    <ul className="space-y-2" data-testid="google-account-list">
      {connections.map(conn => {
        const ready = conn.ready !== false && conn.enabled
        const busy = busyId === conn.id
        const removalAllowed = canRemove ? canRemove(conn) : !readOnly
        return (
          <li key={conn.id} className="flex flex-wrap items-center gap-3 rounded-md border border-border p-3">
            <span className={`h-2 w-2 shrink-0 rounded-full ${ready ? 'bg-green-500' : 'bg-amber-500'}`} />
            <div className="min-w-0 flex-1">
              <div className="truncate text-sm font-medium text-foreground">{conn.email || conn.display_name}</div>
              <div className="text-xs text-muted-foreground">
                {!conn.enabled ? 'Turned off · ' : conn.ready === false ? 'Needs sign-in · ' : ''}
                {googleAccessSummary(conn)}
              </div>
            </div>
            <button type="button" disabled={readOnly || busy} onClick={() => changeGoogleAccountAccess(conn)}
              className="rounded-md border border-border px-3 py-1.5 text-xs font-medium text-foreground hover:bg-muted disabled:opacity-50">
              {conn.ready === false ? 'Sign in again' : 'Change access'}
            </button>
            <MoreMenu disabled={busy || (readOnly && !removalAllowed)} label={`More for ${conn.email || conn.display_name}`} items={[
              { label: 'Send a test email', disabled: readOnly || busy, onSelect: () => onSendTest(conn) },
              { label: 'Reconnect', disabled: readOnly || busy, onSelect: () => onReconnect(conn) },
              { label: conn.enabled ? 'Turn off' : 'Turn on', disabled: readOnly || busy, onSelect: () => onToggle(conn) },
              { label: 'Remove', disabled: !removalAllowed || busy, danger: true, onSelect: () => onRemove(conn) },
            ]} />
          </li>
        )
      })}
    </ul>
  )
}

function MoreMenu({ label, items, disabled }: { label: string; items: { label: string; onSelect: () => void; danger?: boolean; disabled?: boolean }[]; disabled?: boolean }) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    const close = (event: MouseEvent) => { if (!ref.current?.contains(event.target as Node)) setOpen(false) }
    document.addEventListener('mousedown', close)
    return () => document.removeEventListener('mousedown', close)
  }, [open])
  return (
    <div ref={ref} className="relative">
      <button type="button" disabled={disabled} aria-label={label} aria-expanded={open} onClick={() => setOpen(value => !value)} className="rounded-md p-1.5 text-muted-foreground hover:bg-muted disabled:opacity-50">
        <MoreHorizontal className="h-4 w-4" />
      </button>
      {open && (
        <div role="menu" className="absolute right-0 z-20 mt-1 min-w-44 overflow-hidden rounded-md border border-border bg-background py-1 shadow-lg">
          {items.map(item => (
            <button key={item.label} role="menuitem" type="button" disabled={disabled || item.disabled} onClick={() => { setOpen(false); item.onSelect() }}
              className={`block w-full px-3 py-2 text-left text-xs hover:bg-muted disabled:opacity-50 ${item.danger ? 'text-red-600 dark:text-red-400' : 'text-foreground'}`}>
              {item.label}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
