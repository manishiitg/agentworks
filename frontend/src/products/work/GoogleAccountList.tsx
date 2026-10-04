import { useEffect, useRef, useState } from 'react'
import { MoreHorizontal } from 'lucide-react'
import ConnectionIcon from '../../components/connectors/ConnectionIcon'
import type { GmailConnection } from '../../services/api-types'
import { changeGoogleAccountAccess, GOOGLE_ACCESS_SERVICES, googleAccessLabel, googleAccessLevels, googleGrantedAccess } from './googleAccountAccess'

/**
 * Google accounts across products: identity, service icons and saved agent access.
 * Change access opens the same form, retaining the existing connection.
 * Less common actions sit in a More menu.
 */
export function GoogleAccountList({ connections, busyId, readOnly, canRemove, onSendTest, onToggle, onReconnect, onRemove, onSetDefault, workspacePath }: {
  connections: GmailConnection[]
  busyId: string | null
  readOnly?: boolean
  canRemove?: (conn: GmailConnection) => boolean
  onSendTest: (conn: GmailConnection) => void
  onToggle: (conn: GmailConnection) => void
  onReconnect: (conn: GmailConnection) => void
  onRemove: (conn: GmailConnection) => void
  onSetDefault?: (conn: GmailConnection) => void
  workspacePath?: string | null
}) {
  return (
    <ul className="space-y-2" data-testid="google-account-list">
      {connections.map(conn => {
        const ready = conn.ready !== false && conn.enabled
        const busy = busyId === conn.id
        const removalAllowed = canRemove ? canRemove(conn) : !readOnly
        const access = googleAccessLevels(conn)
        const scopes = conn.auth?.scopes
        const granted = scopes ? googleGrantedAccess(scopes) : null
        return (
          <li key={conn.id} className="min-w-0 rounded-lg border border-border p-3">
            <div className="flex flex-wrap items-center gap-2">
              <ConnectionIcon icon="google" name="Google" size="sm" />
              <div className="min-w-0 flex-1 basis-40">
                <div className="break-all text-sm font-medium text-foreground">{conn.email || conn.display_name}{conn.is_default && <span className="ml-2 text-xs font-normal text-muted-foreground">Default</span>}</div>
                <div className="mt-1 flex items-center gap-1.5 text-xs text-muted-foreground">
                  <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${ready ? 'bg-green-500' : 'bg-amber-500'}`} />
                  {!conn.enabled ? 'Turned off' : conn.ready === false ? 'Needs sign-in' : 'Connected'}
                  {conn.client_name && <span className="break-all">· Google app: {conn.client_name === 'platform' ? 'Company app' : conn.client_name}</span>}
                </div>
              </div>
              <button type="button" disabled={readOnly || busy} onClick={() => changeGoogleAccountAccess(conn, workspacePath)}
                className="rounded-md border border-border px-3 py-1.5 text-xs font-medium text-foreground hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50">
                {conn.ready === false ? 'Sign in again' : 'Change access'}
              </button>
              <MoreMenu disabled={busy || (readOnly && !removalAllowed)} label={`More for ${conn.email || conn.display_name}`} items={[
                { label: 'Send a test email', disabled: readOnly || busy, onSelect: () => onSendTest(conn) },
                ...(onSetDefault ? [{ label: 'Make default', disabled: readOnly || busy || conn.is_default || !conn.enabled, onSelect: () => onSetDefault(conn) }] : []),
                { label: 'Reconnect', disabled: readOnly || busy, onSelect: () => onReconnect(conn) },
                { label: conn.enabled ? 'Turn off' : 'Turn on', disabled: readOnly || busy, onSelect: () => onToggle(conn) },
                { label: 'Remove', disabled: !removalAllowed || busy, danger: true, onSelect: () => onRemove(conn) },
              ]} />
            </div>
            <div className="mt-3 border-t border-border pt-3">
              <p className="mb-2 text-xs text-muted-foreground">{ready ? 'AgentWorks settings' : 'Saved AgentWorks settings · available after sign-in and enabling this account'}</p>
              <div className="flex flex-wrap gap-1.5">
                {GOOGLE_ACCESS_SERVICES.map(service => {
                  const level = service.key === 'gmail' ? access.gmail : access.levels[service.key] ?? 'off'
                  const googleLevel = granted ? service.key === 'gmail' ? granted.gmail : granted.levels[service.key] ?? 'off' : null
                  return <span key={service.key} className={`inline-flex items-center gap-1.5 rounded-md border border-border py-1 pl-1 pr-2 text-xs ${level === 'off' ? 'text-muted-foreground' : 'bg-muted/40 text-foreground'}`}>
                    <ConnectionIcon icon={service.icon} name={service.label} size="xs" />
                    <span><span className="block">{service.label}: {googleAccessLabel(service.key, level)}</span>
                      <span className="block text-[10px] text-muted-foreground">Google: {googleLevel === null ? 'Not checked' : service.key === 'gmail' && granted ? [granted.gmailRead && 'Read', granted.gmailWrite && 'Drafts', granted.gmailSend && 'Send'].filter(Boolean).join(', ') || 'No access' : googleLevel === 'off' ? 'No access' : googleAccessLabel(service.key, googleLevel)}</span></span>
                  </span>
                })}
              </div>
              {granted && access.gmail === 'off' && granted.gmailRead && <p className="mt-2 text-xs text-amber-600 dark:text-amber-400">Gmail reading is granted by Google but disabled in AgentWorks. Use Change access to enable it.</p>}
              {granted && GOOGLE_ACCESS_SERVICES.some(service => {
                const requested = service.key === 'gmail' ? access.gmail : access.levels[service.key] ?? 'off'
                const actual = service.key === 'gmail' ? granted.gmail : granted.levels[service.key] ?? 'off'
                if (service.key === 'gmail') return (conn.allow_read_access && !granted.gmailRead) || (conn.allow_agent_write_access && !granted.gmailWrite)
                return requested !== 'off' && (actual === 'off' || (requested === 'write' && actual !== 'write'))
              }) && <p className="mt-2 text-xs text-amber-600 dark:text-amber-400">Some selected access has not been granted by Google. Use Change access and complete sign-in.</p>}
            </div>
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
