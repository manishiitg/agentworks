import { useEffect, useId, useRef, useState } from 'react'
import { ChevronDown, MoreHorizontal } from 'lucide-react'
import ConnectionIcon from '../../components/connectors/ConnectionIcon'
import type { GmailConnection } from '../../services/api-types'
import { changeGoogleAccountAccess, GOOGLE_ACCESS_SERVICES, googleAccessLabel, googleAccessLevels, googleGrantedAccess } from './googleAccountAccess'

/**
 * Google accounts across products: compact summaries, with one permission comparison open at a time.
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
  const [expandedId, setExpandedId] = useState<string | null>(null)
  const listId = useId()
  return (
    <ul className="space-y-2" data-testid="google-account-list">
      {connections.map(conn => {
        const ready = conn.ready !== false && conn.enabled
        const busy = busyId === conn.id
        const removalAllowed = canRemove ? canRemove(conn) : !readOnly
        const access = googleAccessLevels(conn)
        const scopes = conn.auth?.scopes
        const granted = scopes ? googleGrantedAccess(scopes) : null
        const expanded = expandedId === conn.id
        const permissionsId = `${listId}-${conn.id}-permissions`
        const missingGrant = granted && GOOGLE_ACCESS_SERVICES.some(service => {
          const requested = service.key === 'gmail' ? access.gmail : access.levels[service.key] ?? 'off'
          const actual = service.key === 'gmail' ? granted.gmail : granted.levels[service.key] ?? 'off'
          if (service.key === 'gmail') return (conn.allow_read_access && !granted.gmailRead) || (conn.allow_agent_write_access && !granted.gmailWrite)
          return requested !== 'off' && (actual === 'off' || (requested === 'write' && actual !== 'write'))
        })
        return (
          <li key={conn.id} className="min-w-0 rounded-lg border border-border p-3">
            <div className="flex flex-wrap items-center gap-2">
              <ConnectionIcon icon="google" name="Google" size="sm" />
              <div className="min-w-0 flex-1 basis-64">
                <div className="break-all text-sm font-medium text-foreground">{conn.email || conn.display_name}{conn.is_default && <span className="ml-2 inline-block rounded bg-muted px-1.5 py-0.5 text-[10px] font-normal text-muted-foreground">Default</span>}</div>
                <div className="mt-1 flex items-center gap-1.5 text-xs text-muted-foreground">
                  <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${ready ? 'bg-green-500' : 'bg-amber-500'}`} />
                  {!conn.enabled ? 'Turned off' : conn.ready === false ? 'Needs sign-in' : 'Connected'}
                </div>
              </div>
              <div className="ml-auto flex shrink-0 items-center gap-2">
                <button type="button" disabled={readOnly || busy} onClick={() => { setExpandedId(null); changeGoogleAccountAccess(conn, workspacePath) }}
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
            </div>
            <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1.5" aria-label="Saved AgentWorks access">
              {GOOGLE_ACCESS_SERVICES.filter(service => service.key === 'gmail' || access.levels[service.key] && access.levels[service.key] !== 'off').map(service => {
                const level = service.key === 'gmail' ? access.gmail : access.levels[service.key] ?? 'off'
                return <span key={service.key} className="inline-flex items-center gap-1 text-xs text-muted-foreground">
                  <ConnectionIcon icon={service.icon} name={service.label} size="xs" />
                  <span>{service.label}: {googleAccessLabel(service.key, level)}</span>
                </span>
              })}
              <button type="button" aria-label={`${expanded ? 'Hide' : 'Show'} permissions for ${conn.email || conn.display_name}`} aria-expanded={expanded} aria-controls={permissionsId}
                onClick={() => setExpandedId(expanded ? null : conn.id)}
                className="ml-auto inline-flex items-center gap-1 rounded px-1 py-1 text-xs text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                Permissions <ChevronDown aria-hidden="true" className={`h-3.5 w-3.5 transition-transform ${expanded ? 'rotate-180' : ''}`} />
              </button>
            </div>
            {granted && access.gmail === 'off' && granted.gmailRead && <p className="mt-2 text-xs text-amber-600 dark:text-amber-400">Gmail reading is granted by Google but disabled in AgentWorks. Use Change access to enable it.</p>}
            {missingGrant && <p className="mt-2 text-xs text-amber-600 dark:text-amber-400">Some selected access has not been granted by Google. Use Change access and complete sign-in.</p>}
            {expanded && <div id={permissionsId} className="mt-3 border-t border-border pt-3">
              {!ready && <p className="mb-2 text-xs text-muted-foreground">Saved settings · available after sign-in and enabling this account</p>}
              <table className="w-full table-fixed text-left text-xs">
                <caption className="sr-only">Permissions for {conn.email || conn.display_name}</caption>
                <thead><tr className="text-muted-foreground">
                  <th scope="col" className="w-[30%] pb-2 font-normal">App</th>
                  <th scope="col" className="pb-2 pr-2 font-normal">AgentWorks</th>
                  <th scope="col" className="pb-2 font-normal">Google granted</th>
                </tr></thead>
                <tbody>{GOOGLE_ACCESS_SERVICES.map(service => {
                  const level = service.key === 'gmail' ? access.gmail : access.levels[service.key] ?? 'off'
                  const googleLevel = granted ? service.key === 'gmail' ? granted.gmail : granted.levels[service.key] ?? 'off' : null
                  return <tr key={service.key} className="border-t border-border/50">
                    <th scope="row" className="py-2 pr-2 font-normal text-foreground"><span className="flex items-center gap-1.5"><ConnectionIcon icon={service.icon} name={service.label} size="xs" />{service.label}</span></th>
                    <td className={`py-2 pr-2 ${level === 'off' ? 'text-muted-foreground' : 'text-foreground'}`}>{googleAccessLabel(service.key, level)}</td>
                    <td className="py-2 text-muted-foreground">{googleLevel === null ? 'Not checked' : service.key === 'gmail' && granted ? [granted.gmailRead && 'Read', granted.gmailWrite && 'Drafts', granted.gmailSend && 'Send'].filter(Boolean).join(', ') || 'No access' : googleLevel === 'off' ? 'No access' : googleAccessLabel(service.key, googleLevel)}</td>
                  </tr>
                })}</tbody>
              </table>
              {conn.client_name && <p className="mt-2 break-all text-[10px] text-muted-foreground">Google sign-in app: {conn.client_name === 'platform' ? 'Company app' : conn.client_name}</p>}
            </div>}
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
