import { useCallback, useEffect, useId, useRef, useState } from 'react'
import { ArrowRight, Check, ChevronDown, Loader2, Minus, Plus, ShieldCheck } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import ConnectionIcon from '../../components/connectors/ConnectionIcon'
import { GmailSetupGuide } from '../../components/workflow/bots/GmailSetupGuide'
import { googleAppApi } from '../../api/googleApp'
import { CHANGE_GOOGLE_ACCESS_EVENT, GOOGLE_ACCESS_SERVICES, GOOGLE_SERVICES, googleAccessLabel, type GoogleAccessLevel } from './googleAccountAccess'

type Level = GoogleAccessLevel
const SERVICES = GOOGLE_SERVICES

const errorText = (cause: unknown, fallback: string) => {
  const response = (cause as { response?: { data?: unknown } })?.response
  if (typeof response?.data === 'string' && response.data.trim()) return response.data.trim()
  return cause instanceof Error ? cause.message : fallback
}

/** One permission form for company OAuth apps, named JSON uploads and existing accounts. */
export function GoogleAccountConnect({ workspacePath, onChanged, privateAccount = true, readOnly = false }: { workspacePath: string; onChanged?: () => void; privateAccount?: boolean; readOnly?: boolean }) {
  // Members saw a disabled button with no reason in Goals, Relay and Crew (Confida 2026-10-07).
  const lockedReason = privateAccount
    ? "Only this Code's owner can connect or change its Google accounts."
    : 'Google accounts for Crews, workflows and Relays are shared, so only an administrator can connect one. Ask an admin, or connect your own Google account in Code.'
  const [configured, setConfigured] = useState<boolean | null>(null)
  const [clients, setClients] = useState<{ name: string }[]>([])
  const [source, setSource] = useState('')
  const [sourceError, setSourceError] = useState<string | null>(null)
  const [clientName, setClientName] = useState('')
  const [clientFile, setClientFile] = useState<File | null>(null)
  const [gmail, setGmail] = useState<Level>('read')
  const [levels, setLevels] = useState<Record<string, Level>>({ drive: 'read', calendar: 'read' })
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [opened, setOpened] = useState(false)
  const [authUrl, setAuthUrl] = useState('')
  const [copied, setCopied] = useState(false)
  const [expanded, setExpanded] = useState(true)
  const contentId = useId()
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
      setExpanded(true)
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
    setConfigured(null); setSource(''); setSourceError(null)
    void Promise.allSettled([googleAppApi.status(), googleAppApi.clients()]).then(([app, named]) => {
      if (cancelled) return
      const company = app.status === 'fulfilled' && app.value.configured
      const available = named.status === 'fulfilled' ? named.value.filter(client => client.name !== 'platform') : []
      setConfigured(company)
      setClients(available)
      setSource(company ? 'company' : available.length ? `client:${available[0].name}` : 'upload')
      if (app.status === 'rejected' || named.status === 'rejected') {
        setSourceError('Could not check all Google sign-in apps. Existing accounts can still reconnect; available options are shown below. Reopen this panel to retry.')
      }
    })
    return () => { cancelled = true }
  }, [workspacePath])

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
      let result: { id: string; auth_url: string }
      if (changing) {
        // Reauthorization retains this connection's original client, even without a company app.
        result = await googleAppApi.reconnect(changing.id, request)
      } else if (source === 'company' && configured) {
        result = await googleAppApi.connect(request)
      } else {
        let name = source.startsWith('client:') ? source.slice(7) : ''
        if (source === 'upload') {
          name = clientName.trim()
          if (name === 'platform') throw new Error('Choose a different name; platform is reserved for the company Google app.')
          if (!/^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$/.test(name)) throw new Error('Name the Google app using lowercase letters, numbers and hyphens, up to 64 characters.')
          if (!clientFile) throw new Error('Choose the OAuth client JSON downloaded from Google Cloud.')
          let json: unknown
          try { json = JSON.parse(await clientFile.text()) } catch { throw new Error('That file is not valid JSON. Upload the original OAuth client JSON from Google Cloud.') }
          await googleAppApi.registerClient(name, json)
          setClients(existing => [...existing, { name }])
          setSource(`client:${name}`); setClientFile(null)
        }
        if (!name) throw new Error('Choose a Google sign-in app first.')
        result = await googleAppApi.connectWithClient(name, request)
      }
      window.open(result.auth_url, '_blank', 'noopener')
      setOpened(true); setAuthUrl(result.auth_url); setCopied(false)
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
  }, [workspacePath, levels, gmail, onChanged, changing, readOnly, busy, source, configured, clientName, clientFile])


  const changedCount = changing ? GOOGLE_ACCESS_SERVICES.filter(service => {
    const current = service.key === 'gmail' ? changing.gmail : changing.levels[service.key] ?? 'off'
    const selected = service.key === 'gmail' ? gmail : levels[service.key] ?? 'off'
    return current !== selected
  }).length : 0
  const selectedCount = GOOGLE_ACCESS_SERVICES.filter(service => (service.key === 'gmail' ? gmail : levels[service.key] ?? 'off') !== 'off').length
  const cancel = () => {
    setChanging(null); setGmail('read'); setLevels({ drive: 'read', calendar: 'read' }); setError(null); setOpened(false)
    setExpanded(false)
  }

  return (
    <section ref={sectionRef} data-testid="google-account-connect" className={`mb-4 min-w-0 rounded-lg border p-4 ${changing ? 'border-primary' : 'border-border'}`}>
      <div className="flex items-start gap-3">
        <button type="button" aria-expanded={expanded} aria-controls={contentId} onClick={() => setExpanded(value => !value)} className="flex min-w-0 flex-1 items-start gap-3 rounded-md text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
          <ConnectionIcon icon="google" name="Google" size="sm" />
          <span className="min-w-0 flex-1">
            <span className="block break-words text-sm font-medium">{changing ? `Change access for ${changing.email}` : 'Connect a Google account'}</span>
            <span className="mt-1 block text-xs text-muted-foreground">{!expanded && changedCount ? `${changedCount} unsaved ${changedCount === 1 ? 'change' : 'changes'}` : readOnly ? lockedReason : 'Choose which services your agent can use.'}</span>
          </span>
          <ChevronDown className={`mt-1 h-4 w-4 shrink-0 text-muted-foreground transition-transform ${expanded ? 'rotate-180' : ''}`} aria-hidden="true" />
        </button>
        {changing && <button type="button" disabled={busy} className="rounded px-1 py-1 text-xs font-medium text-muted-foreground hover:text-foreground disabled:opacity-50" onClick={cancel}>Cancel</button>}
      </div>
      <div id={contentId} hidden={!expanded}>
      <p className="mt-3 text-xs leading-5 text-muted-foreground">
        {privateAccount ? 'Sign in with your own Google account, personal or work. It stays in this Code and works only for you.' : 'An administrator connects Google accounts shared by Crews and workflows on this installation.'} The agent uses it through the server and never sees your password or token.
      </p>
      {readOnly && <p role="note" className="mt-3 rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs leading-5 text-amber-800 dark:text-amber-200">{lockedReason}</p>}
      {sourceError && <p role="alert" className="mt-2 text-xs text-amber-600 dark:text-amber-400">{sourceError}</p>}
      {!changing && <div className="mt-3 space-y-2 rounded-md border border-border p-3">
        <label className="block text-xs font-medium">Google sign-in app
          <select aria-label="Google sign-in app" value={source} disabled={readOnly || busy || configured === null}
            onChange={event => { setSource(event.target.value); setError(null) }} className="mt-1 block w-full rounded-md border border-border bg-background p-2 text-xs">
            {configured === null && <option value="">Checking Google apps…</option>}
            {configured && <option value="company">Company Google app · configured by administrator</option>}
            {clients.map(client => <option key={client.name} value={`client:${client.name}`}>Saved app: {client.name}</option>)}
            <option value="upload">Use my own OAuth JSON</option>
          </select>
        </label>
        <p className="text-xs text-muted-foreground">{source === 'company' ? 'Use the app configured for this installation. No JSON upload needed.' : source === 'upload' ? 'Give this app a unique name. Uploading creates a separate app and keeps existing accounts unchanged.' : 'Reuse this saved Google app to connect another account.'}</p>
        {source === 'upload' && <>
          <label className="block text-xs">Name this Google app
            <input aria-label="Google app name" value={clientName} onChange={event => setClientName(event.target.value)} disabled={readOnly || busy} placeholder="my-google-app"
              className="mt-1 block w-full rounded-md border border-border bg-background p-2 text-xs" />
          </label>
          <label className="block text-xs">OAuth client JSON
            <input aria-label="Google Cloud client file" type="file" accept=".json,application/json" disabled={readOnly || busy} onChange={event => setClientFile(event.target.files?.[0] || null)} className="mt-1 block w-full text-xs" />
          </label>
          <GmailSetupGuide backend="gog" />
        </>}
      </div>}
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
      {opened && <div className="mt-2 text-xs text-muted-foreground">
        <p role="status">Finish signing in on the Google tab that opened. This list updates when you come back.</p>
        <button type="button" className="mt-1 text-primary underline" onClick={() => { void navigator.clipboard.writeText(authUrl).then(() => setCopied(true)).catch(() => setError('Could not copy the sign-in link.')) }}>{copied ? 'Link copied' : 'Copy sign-in link'}</button>
        <p className="mt-1">Use this link if the account is in a different browser profile.</p>
      </div>}
      <div className="mt-4 flex flex-wrap items-center justify-between gap-3 border-t border-border pt-3">
        <div className="flex min-w-0 items-start gap-2 text-xs text-muted-foreground">
          <ShieldCheck className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
          <div>
            <div role="status" className="font-medium text-foreground">{changing ? changedCount ? `${changedCount} unsaved ${changedCount === 1 ? 'change' : 'changes'}` : 'Access is up to date' : `${selectedCount} services selected`}</div>
            <div className="mt-1 max-w-72 leading-4">{changing ? 'Changes are saved when you continue. Approve new permissions with Google.' : 'Review and approve these permissions with Google.'}</div>
          </div>
        </div>
        <Button size="sm" disabled={busy || readOnly || (!changing && (!source || configured === null || (source === 'upload' && (!clientName.trim() || !clientFile))))} onClick={() => { void connect() }}>
          {busy ? <Loader2 className="mr-1 h-3.5 w-3.5 animate-spin" aria-hidden="true" /> : null}{readOnly ? (privateAccount ? 'Only the owner can connect' : 'Only an admin can connect') : changing ? 'Sign in again with Google' : 'Connect Google account'}<ArrowRight className="ml-1.5 h-3.5 w-3.5" aria-hidden="true" />
        </Button>
      </div>
      </div>
    </section>
  )
}
