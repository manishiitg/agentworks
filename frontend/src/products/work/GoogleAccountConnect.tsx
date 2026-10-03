import { useCallback, useEffect, useRef, useState } from 'react'
import { Loader2 } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { googleAppApi } from '../../api/googleApp'
import { CHANGE_GOOGLE_ACCESS_EVENT, GOOGLE_SERVICES, type GoogleAccessLevel } from './googleAccountAccess'

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
  const [changing, setChanging] = useState<{ id: string; email: string } | null>(null)
  const sectionRef = useRef<HTMLElement>(null)
  const focusListener = useRef<(() => void) | null>(null)
  useEffect(() => {
    const open = (event: Event) => {
      const detail = (event as CustomEvent<{ id: string; workspacePath?: string; email: string; gmail: Level; levels: Record<string, Level> }>).detail
      if (readOnly || !detail?.id || detail.workspacePath !== workspacePath) return
      setGmail(detail.gmail)
      setLevels(detail.levels)
      setChanging({ id: detail.id, email: detail.email })
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

  const select = (value: Level, onChange: (level: Level) => void, label: string, writeLabel: string) => (
    <select
      value={value}
      aria-label={label}
      disabled={readOnly || busy}
      onChange={event => onChange(event.target.value as Level)}
      className="h-8 rounded-md border border-border bg-background px-2 text-sm"
    >
      <option value="off">Not used</option>
      <option value="read">Read only</option>
      <option value="write">{writeLabel}</option>
    </select>
  )

  return (
    <section ref={sectionRef} data-testid="google-account-connect" className={`mb-4 rounded-md border p-3 ${changing ? 'border-primary' : 'border-border'}`}>
      <div className="text-sm font-medium">{changing ? `Change access for ${changing.email}` : 'Connect a Google account'}</div>
      {changing && <p className="mt-1 text-xs text-muted-foreground">Pick what the agent may use, then sign in again as {changing.email}. The existing account keeps its connections and triggers. <button type="button" disabled={busy} className="font-medium text-primary hover:underline" onClick={() => setChanging(null)}>Cancel</button></p>}
      <p className="mt-1 text-xs leading-5 text-muted-foreground">
        {privateAccount ? 'Sign in with your own Google account, personal or work. It stays in this Code and works only for you.' : 'An administrator connects Google accounts shared by Crews and workflows on this installation.'} The agent uses it through the server and never sees your password or token.
      </p>
      {readOnly && <p className="mt-2 text-xs text-muted-foreground">{privateAccount ? "Only this Code's owner can manage its Google accounts." : 'An administrator manages shared Google accounts.'}</p>}
      <div className="mt-3 grid gap-2 sm:grid-cols-2">
        <label className="flex items-center justify-between gap-2 text-sm">Gmail
          {select(gmail, setGmail, 'Gmail access', 'Read, draft and send')}
        </label>
        {SERVICES.map(service => (
          <label key={service.key} className="flex items-center justify-between gap-2 text-sm">{service.label}
            {select(levels[service.key] ?? 'off', level => setLevels(current => ({ ...current, [service.key]: level })), `${service.label} access`, 'Read and edit')}
          </label>
        ))}
      </div>
      {error && <p role="alert" className="mt-2 text-xs text-destructive">{error}</p>}
      {opened && <p className="mt-2 text-xs text-muted-foreground">Finish signing in on the Google tab that opened. This list updates when you come back.</p>}
      <div className="mt-3 flex justify-end">
        <Button size="sm" disabled={busy || readOnly} onClick={() => { void connect() }}>
          {busy ? <Loader2 className="mr-1 h-3.5 w-3.5 animate-spin" /> : null}{changing ? 'Sign in again with Google' : 'Connect Google account'}
        </Button>
      </div>
    </section>
  )
}
