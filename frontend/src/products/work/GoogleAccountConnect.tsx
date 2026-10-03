import { useCallback, useEffect, useRef, useState } from 'react'
import { ArrowRight, Check, Loader2, Minus, Plus, ShieldCheck } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import ConnectionIcon from '../../components/connectors/ConnectionIcon'
import { googleAppApi } from '../../api/googleApp'
import { CHANGE_GOOGLE_ACCESS_EVENT, GOOGLE_ACCESS_SERVICES, GOOGLE_SERVICES, googleAccessLabel, type GoogleAccessLevel } from './googleAccountAccess'

type Level = GoogleAccessLevel
const SERVICES = GOOGLE_SERVICES

const errorText = (cause: unknown, fallback: string) => {
  const response = (cause as { response?: { data?: unknown } })?.response
  if (typeof response?.data === 'string' && response.data.trim()) return response.data.trim()
  return cause instanceof Error ? cause.message : fallback
}

/**
 * Connect Google accounts through the server's Google app: pick what the
 * agent may use, sign in with Google, done. Nothing is uploaded and there is no Google Cloud
 * project to make. Code accounts stay private; shared accounts are admin-managed. The server
 * holds the token and runs the Google tools, refusing changes the person did not allow.
 * Rendered only when the server has a Google app.
 */
export function GoogleAccountConnect({ workspacePath, onChanged, privateAccount = true, readOnly = false }: { workspacePath: string; onChanged?: () => void; privateAccount?: boolean; readOnly?: boolean }) {
  const [configured, setConfigured] = useState<boolean | null>(null)
  const [gmail, setGmail] = useState<Level>('read')
  const [levels, setLevels] = useState<Record<string, Level>>({ drive: 'read', calendar: 'read' })
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [opened, setOpened] = useState(false)
  const [changing, setChanging] = useState<{ id: string; email: string; gmail: Level; levels: Record<string, Level> } | null>(null)
  const sectionRef = useRef<HTMLElement>(null)
  const focusListener = useRef<(() => void) | null>(null)
  useEffect(() => {
    const open = (event: Event) => {
      const detail = (event as CustomEvent<{ id: string; workspacePath?: string; email: string; gmail: Level; levels: Record<string, Level> }>).detail
      if (readOnly || !detail?.id || detail.workspacePath !== workspacePath) return
      setGmail(detail.gmail)
      setLevels(detail.levels)
      setChanging({ id: detail.id, email: detail.email, gmail: detail.gmail, levels: { ...detail.levels } })
      setError(null)
      setOpened(false)
      sectionRef.current?.scrollIntoView?.({ behavior: 'smooth', block: 'center' })
    }
    window.addEventListener(CHANGE_GOOGLE_ACCESS_EVENT, open)
    return () => window.removeEventListener(CHANGE_GOOGLE_ACCESS_EVENT, open)
  }, [workspacePath, readOnly])

  useEffect(() => () => {
    if (focusListener.current) window.removeEventListener('focus', focusListener.current)
  }, [])

  useEffect(() => {
    let cancelled = false
    void googleAppApi.status().then(status => { if (!cancelled) setConfigured(status.configured) }).catch(() => { if (!cancelled) setConfigured(false) })
    return () => { cancelled = true }
  }, [])

  const connect = useCallback(async () => {
    if (readOnly || busy) return
    setBusy(true); setError(null); setOpened(false)
    try {
      const services = SERVICES.filter(s => (levels[s.key] ?? 'off') !== 'off').map(s => ({ service: s.key, write: levels[s.key] === 'write' }))
      const request = {
        workspace_path: workspacePath,
        services,
        allow_read_access: gmail !== 'off',
        allow_agent_write_access: gmail === 'write',
      }
      const result = changing ? await googleAppApi.reconnect(changing.id, request) : await googleAppApi.connect(request)
      window.open(result.auth_url, '_blank', 'noopener')
      setOpened(true)
      if (changing) setChanging({ ...changing, gmail, levels: { ...levels } })
      // The account list refreshes when the person comes back from Google, not before.
      if (focusListener.current) window.removeEventListener('focus', focusListener.current)
      const onFocus = () => { window.removeEventListener('focus', onFocus); focusListener.current = null; onChanged?.() }
      focusListener.current = onFocus
      window.addEventListener('focus', onFocus)
    } catch (cause) {
      setError(errorText(cause, 'Could not start the Google sign-in.'))
    } finally {
      setBusy(false)
    }
  }, [workspacePath, levels, gmail, onChanged, changing, readOnly, busy])

  if (!configured) return null

  const changedCount = changing ? GOOGLE_ACCESS_SERVICES.filter(service => {
    const current = service.key === 'gmail' ? changing.gmail : changing.levels[service.key] ?? 'off'
    const selected = service.key === 'gmail' ? gmail : levels[service.key] ?? 'off'
    return current !== selected
  }).length : 0
  const selectedCount = GOOGLE_ACCESS_SERVICES.filter(service => (service.key === 'gmail' ? gmail : levels[service.key] ?? 'off') !== 'off').length
  const cancel = () => {
    setChanging(null); setGmail('read'); setLevels({ drive: 'read', calendar: 'read' }); setError(null); setOpened(false)
  }

  return (
    <section ref={sectionRef} data-testid="google-account-connect" className={`mb-4 min-w-0 rounded-lg border p-4 ${changing ? 'border-primary' : 'border-border'}`}>
      <div className="flex items-start gap-3">
        <ConnectionIcon icon="google" name="Google" size="sm" />
        <div className="min-w-0 flex-1">
          <h3 className="break-words text-sm font-medium">{changing ? `Change access for ${changing.email}` : 'Connect a Google account'}</h3>
          <p className="mt-1 text-xs text-muted-foreground">Choose which services your agent can use.</p>
        </div>
        {changing && <button type="button" disabled={busy} className="rounded px-1 py-1 text-xs font-medium text-muted-foreground hover:text-foreground disabled:opacity-50" onClick={cancel}>Cancel</button>}
      </div>
      <p className="mt-3 text-xs leading-5 text-muted-foreground">
        {privateAccount ? 'Sign in with your own Google account, personal or work. It stays in this Code and works only for you.' : 'An administrator connects Google accounts shared by Crews and workflows on this installation.'} The agent uses it through the server and never sees your password or token.
      </p>
      {readOnly && <p className="mt-2 text-xs text-muted-foreground">{privateAccount ? "Only this Code's owner can manage its Google accounts." : 'An administrator manages shared Google accounts.'}</p>}
      <div className="mt-4 grid grid-cols-[repeat(auto-fit,minmax(min(100%,250px),1fr))] gap-2">
        {GOOGLE_ACCESS_SERVICES.map(service => {
          const value = service.key === 'gmail' ? gmail : levels[service.key] ?? 'off'
          const current = changing ? (service.key === 'gmail' ? changing.gmail : changing.levels[service.key] ?? 'off') : null
          const pending = current !== null && current !== value
          const update = (level: Level) => service.key === 'gmail' ? setGmail(level) : setLevels(existing => ({ ...existing, [service.key]: level }))
          return (
            <div key={service.key} data-testid={`google-access-${service.key}`} className={`min-w-0 rounded-md border p-3 ${pending ? 'border-primary/50 bg-primary/5' : value !== 'off' ? 'border-border bg-muted/30' : 'border-border'}`}>
              <div className="flex items-center gap-2.5">
                <ConnectionIcon icon={service.icon} name={service.label} size="sm" />
                <div className="min-w-0 flex-1">
                  <div className="text-sm font-medium">{service.label}</div>
                  <div className="text-xs text-muted-foreground">{service.description}</div>
                </div>
                <button type="button" disabled={readOnly || busy} aria-label={`${value === 'off' ? 'Add' : 'Remove'} ${service.label} access`} onClick={() => update(value === 'off' ? 'read' : 'off')}
                  className="flex shrink-0 items-center gap-1 rounded-md px-2 py-1.5 text-xs font-medium text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50">
                  {value === 'off' ? <Plus className="h-3.5 w-3.5" aria-hidden="true" /> : <Minus className="h-3.5 w-3.5" aria-hidden="true" />}{value === 'off' ? 'Add' : 'Remove'}
                </button>
              </div>
              {current !== null && <div className="mt-2 text-xs text-muted-foreground">Current: {googleAccessLabel(service.key, current)}</div>}
              <label className="mt-2 flex items-center gap-2 text-xs">
                {value !== 'off' && <Check className="h-3.5 w-3.5 shrink-0 text-primary" aria-hidden="true" />}
                <span className="sr-only">{pending ? 'New' : 'Selected'} {service.label} access</span>
                <select value={value} aria-label={`${service.label} access`} disabled={readOnly || busy} onChange={event => update(event.target.value as Level)}
                  className="h-8 min-w-0 flex-1 rounded-md border border-border bg-background px-2 text-xs focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50">
                  <option value="off">{service.key === 'gmail' ? 'Notifications only' : 'No access'}</option>
                  <option value="read">Read only</option>
                  <option value="write">{googleAccessLabel(service.key, 'write')}</option>
                </select>
                {pending && <span className="text-xs font-medium text-primary">Changed</span>}
              </label>
            </div>
          )
        })}
      </div>
      <p className="mt-3 text-xs leading-5 text-muted-foreground">Gmail can still send workflow notifications when agent access is removed.</p>
      {error && <p role="alert" className="mt-2 text-xs text-destructive">{error}</p>}
      {opened && <p role="status" className="mt-2 text-xs text-muted-foreground">Finish signing in on the Google tab that opened. This list updates when you come back.</p>}
      <div className="mt-4 flex flex-wrap items-center justify-between gap-3 border-t border-border pt-3">
        <div className="flex min-w-0 items-start gap-2 text-xs text-muted-foreground">
          <ShieldCheck className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
          <div>
            <div role="status" className="font-medium text-foreground">{changing ? changedCount ? `${changedCount} unsaved ${changedCount === 1 ? 'change' : 'changes'}` : 'Access is up to date' : `${selectedCount} services selected`}</div>
            <div className="mt-1 max-w-72 leading-4">{changing ? 'Changes are saved when you continue. Approve new permissions with Google.' : 'Review and approve these permissions with Google.'}</div>
          </div>
        </div>
        <Button size="sm" disabled={busy || readOnly} onClick={() => { void connect() }}>
          {busy ? <Loader2 className="mr-1 h-3.5 w-3.5 animate-spin" aria-hidden="true" /> : null}{changing ? 'Sign in again with Google' : 'Connect Google account'}<ArrowRight className="ml-1.5 h-3.5 w-3.5" aria-hidden="true" />
        </Button>
      </div>
    </section>
  )
}
