import { useCallback, useEffect, useState } from 'react'
import { Loader2 } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { googleAppApi } from '../../api/googleApp'

type Level = 'off' | 'read' | 'write'

// Google Workspace services beyond Gmail (Gmail has its own switches). Keys are what the server
// calls them (services.GoogleServiceCatalog).
const SERVICES: { key: string; label: string }[] = [
  { key: 'drive', label: 'Drive' },
  { key: 'calendar', label: 'Calendar' },
  { key: 'docs', label: 'Docs' },
  { key: 'sheets', label: 'Sheets' },
  { key: 'slides', label: 'Slides' },
]

const errorText = (cause: unknown, fallback: string) => {
  const response = (cause as { response?: { data?: unknown } })?.response
  if (typeof response?.data === 'string' && response.data.trim()) return response.data.trim()
  return cause instanceof Error ? cause.message : fallback
}

/**
 * Connect your own Google account to this Code through the server's Google app: pick what the
 * agent may use, sign in with Google, done. Nothing is uploaded and there is no Google Cloud
 * project to make. The account is private to this Code, and the server (not the agent's shell)
 * holds the token and runs the Google tools, refusing changes the person did not allow.
 * Rendered only when the server has a Google app.
 */
export function GoogleAccountConnect({ workspacePath, onChanged }: { workspacePath: string; onChanged?: () => void }) {
  const [configured, setConfigured] = useState<boolean | null>(null)
  const [gmail, setGmail] = useState<Level>('read')
  const [levels, setLevels] = useState<Record<string, Level>>({ drive: 'read', calendar: 'read' })
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [opened, setOpened] = useState(false)

  useEffect(() => {
    let cancelled = false
    void googleAppApi.status().then(status => { if (!cancelled) setConfigured(status.configured) }).catch(() => { if (!cancelled) setConfigured(false) })
    return () => { cancelled = true }
  }, [])

  const connect = useCallback(async () => {
    setBusy(true); setError(null); setOpened(false)
    try {
      const services = SERVICES.filter(s => (levels[s.key] ?? 'off') !== 'off').map(s => ({ service: s.key, write: levels[s.key] === 'write' }))
      const result = await googleAppApi.connect({
        workspace_path: workspacePath,
        services,
        allow_read_access: gmail !== 'off',
        allow_agent_write_access: gmail === 'write',
      })
      window.open(result.auth_url, '_blank', 'noopener')
      setOpened(true)
      // The account list refreshes when the person comes back from Google, not before.
      const onFocus = () => { window.removeEventListener('focus', onFocus); onChanged?.() }
      window.addEventListener('focus', onFocus)
    } catch (cause) {
      setError(errorText(cause, 'Could not start the Google sign-in.'))
    } finally {
      setBusy(false)
    }
  }, [workspacePath, levels, gmail, onChanged])

  if (!configured) return null

  const select = (value: Level, onChange: (level: Level) => void, label: string, writeLabel: string) => (
    <select
      value={value}
      aria-label={label}
      onChange={event => onChange(event.target.value as Level)}
      className="h-8 rounded-md border border-border bg-background px-2 text-sm"
    >
      <option value="off">Not used</option>
      <option value="read">Read only</option>
      <option value="write">{writeLabel}</option>
    </select>
  )

  return (
    <section data-testid="google-account-connect" className="mb-4 rounded-md border border-border p-3">
      <div className="text-sm font-medium">Connect your Google account</div>
      <p className="mt-1 text-xs leading-5 text-muted-foreground">
        Sign in with your own Google account, personal or work. It stays in this Code and works only for you. The agent uses it through the server and never sees your password or token.
      </p>
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
        <Button size="sm" disabled={busy} onClick={() => { void connect() }}>
          {busy ? <Loader2 className="mr-1 h-3.5 w-3.5 animate-spin" /> : null}Connect Google account
        </Button>
      </div>
    </section>
  )
}
