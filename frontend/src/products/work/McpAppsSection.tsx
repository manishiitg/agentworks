import { useCallback, useEffect, useRef, useState } from 'react'
import { CheckCircle2, ChevronDown, ChevronRight, Copy, KeyRound, Loader2, Upload } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { Input } from '../../components/ui/Input'
import { mcpAppsApi, type McpAppGroup } from '../../api/mcpApps'
import { parseOAuthClientJson } from './oauthClientJson'

const errorText = (cause: unknown, fallback: string) => {
  const response = (cause as { response?: { data?: { error?: string } } })?.response
  return response?.data?.error || (cause instanceof Error ? cause.message : fallback)
}

/**
 * Sign-in apps, for admins (docs/design/code_private_mcp.md, "Sign-in apps").
 * Google, GitHub, Slack and a few others have no automatic app registration,
 * so people are asked for an OAuth client ID and secret. An admin sets one app
 * up per provider here, once; everyone's Connect then goes straight to the
 * provider's consent screen and signs in as themselves.
 */
export function McpAppsSection() {
  const [apps, setApps] = useState<McpAppGroup[]>([])
  const [redirectUri, setRedirectUri] = useState('')
  const [loading, setLoading] = useState(true)
  const [open, setOpen] = useState<string | null>(null)
  const [busy, setBusy] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [clientId, setClientId] = useState('')
  const [clientSecret, setClientSecret] = useState('')
  const fileInput = useRef<HTMLInputElement>(null)

  const refresh = useCallback(async () => {
    try {
      const result = await mcpAppsApi.list()
      setApps(result.apps)
      setRedirectUri(result.redirectUri)
      setError(null)
    } catch (cause) {
      setError(errorText(cause, 'Could not load the sign-in apps.'))
    } finally {
      setLoading(false)
    }
  }, [])
  useEffect(() => { void refresh() }, [refresh])

  const openGroup = (key: string) => {
    setOpen(current => (current === key ? null : key))
    setClientId(''); setClientSecret(''); setNotice(null); setError(null)
  }

  const loadClientFile = async (file: File | undefined) => {
    if (!file) return
    setNotice(null); setError(null)
    const parsed = parseOAuthClientJson(await file.text())
    if (!parsed) {
      setError('That file is not a Google OAuth client file (client_secret_….json).')
      return
    }
    setClientId(parsed.clientId)
    setClientSecret(parsed.clientSecret)
    if (redirectUri && parsed.redirectUris.length > 0 && !parsed.redirectUris.includes(redirectUri)) {
      setNotice(`This client does not list ${redirectUri} as a redirect URI, so sign-in will fail until you add it in Google Cloud.`)
    }
  }

  const save = async (key: string) => {
    setBusy(key); setError(null)
    try {
      await mcpAppsApi.save(key, clientId.trim(), clientSecret.trim())
      setClientId(''); setClientSecret(''); setOpen(null)
      await refresh()
    } catch (cause) {
      setError(errorText(cause, 'Could not save the app.'))
    } finally { setBusy(null) }
  }

  const remove = async (key: string) => {
    setBusy(key); setError(null)
    try {
      await mcpAppsApi.remove(key)
      await refresh()
    } catch (cause) {
      setError(errorText(cause, 'Could not remove the app.'))
    } finally { setBusy(null) }
  }

  if (loading) return null
  if (apps.length === 0) return null

  return (
    <div className="mt-3 rounded-lg border border-border bg-muted/40 p-3" data-testid="mcp-apps-section">
      <div className="flex items-center gap-2">
        <KeyRound className="h-4 w-4 text-primary" />
        <h4 className="text-sm font-semibold text-foreground">Sign-in apps <span className="ml-1 rounded bg-primary/10 px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide text-primary">Admin</span></h4>
      </div>
      <p className="mt-1 text-xs leading-5 text-muted-foreground">
        These providers need an OAuth app before anyone can connect. Set one up per provider once, and everyone just clicks Connect and signs in with their own account. The app only identifies this server to the provider; the secret is stored encrypted and never shown again.
      </p>
      {error && <p role="alert" className="mt-2 text-xs text-destructive">{error}</p>}
      <div className="mt-2 space-y-2">
        {apps.map(app => (
          <div key={app.key} className="rounded-md border border-border bg-background">
            <button type="button" className="flex w-full items-center gap-2 p-2 text-left text-sm" onClick={() => openGroup(app.key)} aria-expanded={open === app.key}>
              {open === app.key ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
              <span className="font-medium text-foreground">{app.label}</span>
              <span className="min-w-0 flex-1 truncate text-xs text-muted-foreground">{app.servers.join(', ')}</span>
              {app.configured
                ? <span className="flex items-center gap-1 text-xs text-emerald-600"><CheckCircle2 className="h-3.5 w-3.5" />Set up</span>
                : <span className="text-xs text-amber-600">Not set up</span>}
            </button>
            {open === app.key && (
              <div className="space-y-2 border-t border-border p-3">
                {app.configured && <p className="text-xs text-muted-foreground">Current client ID: <code className="break-all text-foreground">{app.client_id}</code>. Saving replaces it for everyone.</p>}
                <div className="rounded-md bg-muted/50 p-2 text-xs leading-5 text-muted-foreground">
                  {app.key === 'google' ? (
                    <ol className="list-decimal space-y-0.5 pl-4">
                      <li>In <a className="text-primary underline" href="https://console.cloud.google.com/apis/credentials" target="_blank" rel="noreferrer">Google Cloud → Credentials</a>, use a project of your Workspace and turn on the Google Workspace MCP services you want (Gmail, Drive, Docs, Sheets, Slides, Calendar, Chat, People).</li>
                      <li>Set the OAuth consent screen to <b>Internal</b> (your own Workspace only, no Google review).</li>
                      <li>Create credentials → OAuth client ID → <b>Web application</b>, with the redirect URI below.</li>
                      <li>Download the client JSON and upload it here, or paste the client ID and secret.</li>
                    </ol>
                  ) : (
                    <p>Create an OAuth app with {app.label}, register the redirect URI below, then paste its client ID and secret.</p>
                  )}
                  {redirectUri && (
                    <p className="mt-1 flex flex-wrap items-center gap-1">Redirect URI:
                      <code className="break-all text-foreground">{redirectUri}</code>
                      <button type="button" className="inline-flex items-center gap-1 rounded px-1 text-primary hover:bg-primary/10" onClick={() => { void navigator.clipboard?.writeText(redirectUri) }} aria-label="Copy the redirect URI"><Copy className="h-3 w-3" />Copy</button>
                    </p>
                  )}
                </div>
                <input ref={fileInput} type="file" accept="application/json,.json" className="hidden" aria-label="Upload the client JSON" onChange={event => { void loadClientFile(event.target.files?.[0]); event.target.value = '' }} />
                <Button variant="outline" size="sm" onClick={() => fileInput.current?.click()}><Upload className="mr-1 h-3.5 w-3.5" />Upload client_secret.json</Button>
                <div className="grid gap-2 sm:grid-cols-2">
                  <Input value={clientId} onChange={event => setClientId(event.target.value)} placeholder="Client ID" aria-label={`${app.label} client ID`} />
                  <Input type="password" autoComplete="off" value={clientSecret} onChange={event => setClientSecret(event.target.value)} placeholder="Client secret" aria-label={`${app.label} client secret`} />
                </div>
                {notice && <p className="text-xs text-amber-600">{notice}</p>}
                <div className="flex justify-end gap-2">
                  {app.configured && <Button variant="ghost" size="sm" className="text-destructive" disabled={busy !== null} onClick={() => { void remove(app.key) }}>Remove</Button>}
                  <Button size="sm" disabled={busy !== null || !clientId.trim() || !clientSecret.trim()} onClick={() => { void save(app.key) }}>
                    {busy === app.key ? <Loader2 className="mr-1 h-3.5 w-3.5 animate-spin" /> : null}Save app
                  </Button>
                </div>
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  )
}
