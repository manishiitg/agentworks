import { useCallback, useEffect, useState } from 'react'
import { Copy, ExternalLink } from 'lucide-react'
import api from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import { Button } from '../ui/Button'
import { Input } from '../ui/Input'
import { SecretField } from '../ui/SecretField'
import { slackErrorText } from '../workflow/bots/SlackSlugsSection'

type SignInApp = { key: string; configured: boolean; client_id?: string; scopes?: string[] }
type Setup = { apps: SignInApp[]; redirect_uri: string }

/** The existing administrator API, shared with sign_in_apps in chat/MCP. */
export function SlackSignInSetup({ onSaved, ready }: { onSaved: () => Promise<void>; ready?: boolean }) {
  const admin = useAuthStore(state => state.user?.is_admin === true)
  const [setup, setSetup] = useState<Setup | null>(null)
  const [clientID, setClientID] = useState('')
  const [secret, setSecret] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const load = useCallback(async () => {
    try {
      const { data } = await api.get<Setup>('/api/admin/mcp-apps')
      setSetup(data)
      setClientID(data.apps.find(app => app.key === 'slack')?.client_id || '')
      setError('')
    } catch (cause) { setError(slackErrorText(cause, 'Could not load Slack sign-in setup.')) }
  }, [])
  useEffect(() => { if (admin) void load() }, [admin, load])
  if (!admin) return null
  const app = setup?.apps.find(item => item.key === 'slack')
  const copyCallback = async () => {
    if (!setup?.redirect_uri) return
    try { await navigator.clipboard.writeText(setup.redirect_uri); setNotice('Redirect URL copied.') }
    catch { setError('Could not copy. Select the redirect URL below and copy it.') }
  }
  const save = async () => {
    if (!app || busy || !clientID.trim() || !secret.trim()) return
    setBusy(true); setError(''); setNotice('')
    try {
      await api.put('/api/admin/mcp-apps/slack', { client_id: clientID.trim(), client_secret: secret.trim() })
      setSecret('')
      await load()
      await onSaved()
      setNotice('Sign-in setup saved. Choose Connect my Slack, then approve access in Slack.')
    } catch (cause) { setError(slackErrorText(cause, 'Could not save Slack sign-in setup.')) }
    finally { setBusy(false) }
  }
  return <section aria-label="Slack sign-in setup" className="space-y-3">
    {error && <p role="alert" className="text-xs text-destructive">{error} <button type="button" className="underline" onClick={() => void load()}>Retry</button></p>}
    {notice && <p role="status" className="text-xs">{notice}</p>}
    {!setup && !error && <p className="text-xs text-muted-foreground">Loading sign-in setup…</p>}
    {setup && !app && ready !== true && <p className="text-xs text-muted-foreground">There are no Slack app settings to edit here. Ask in chat to check this server’s Slack sign-in setup.</p>}
    {app && setup && <details open={!app.configured} className="rounded-lg border border-border p-4">
      <summary className="cursor-pointer text-sm font-medium">{app.configured ? 'Slack sign-in app · Ready' : 'Set up Slack sign-in'}</summary>
      <form className="mt-4 space-y-4 text-sm" aria-label="Slack sign-in app" onSubmit={event => { event.preventDefault(); void save() }}>
        <p className="text-xs text-muted-foreground">You can reuse the Slack app you created for your bot. These settings enable your personal sign-in; they keep the bot unchanged.</p>
        <a href="https://api.slack.com/apps" target="_blank" rel="noreferrer" className="inline-flex items-center gap-1.5 text-xs text-primary">Open Slack app settings<ExternalLink className="h-3.5 w-3.5"/></a>
        <div className="space-y-2 text-xs">
          <p>In <b>OAuth &amp; Permissions → Redirect URLs</b>, add this address:</p>
          <div className="flex items-start gap-2 rounded-md border border-border p-2"><code className="min-w-0 flex-1 break-all">{setup.redirect_uri}</code><Button type="button" variant="ghost" size="xs" aria-label="Copy Slack redirect URL" onClick={() => void copyCallback()}><Copy className="h-3.5 w-3.5"/>Copy</Button></div>
          <p>Under <b>Agents</b>, enable <b>Slack MCP Server</b>. Add its <b>User Token Scopes</b> and install the app in your workspace. <a href="https://docs.slack.dev/ai/slack-mcp-server/#oauth-scopes-needed-on-user-token-for-different-tools" target="_blank" rel="noreferrer" className="text-primary underline">Slack’s setup guide</a></p>
          {!!app.scopes?.length && <details><summary className="cursor-pointer text-muted-foreground">Required user permissions</summary><div className="mt-2 flex flex-wrap gap-1.5">{app.scopes.map(scope => <code key={scope} className="rounded bg-muted px-1.5 py-1 text-[11px]">{scope}</code>)}</div></details>}
        </div>
        <label className="block text-xs font-medium">Client ID<Input aria-label="Slack client ID" value={clientID} disabled={busy} onChange={event => setClientID(event.target.value)} autoComplete="off"/></label>
        <SecretField label="Client secret" value={secret} onChange={setSecret} disabled={busy} placeholder={app.configured ? 'Already saved; enter to update' : 'From App Credentials'} hint={<>Find both values in your Slack app’s <b>Basic Information → App Credentials</b>. These are different from the bot and app tokens.</>}/>
        <Button type="submit" size="sm" disabled={busy || !clientID.trim() || !secret.trim()}>{busy ? 'Saving…' : 'Save sign-in setup'}</Button>
      </form>
    </details>}
  </section>
}
