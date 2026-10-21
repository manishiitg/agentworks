import { useEffect, useState } from 'react'
import { Copy } from 'lucide-react'
import { authApi, type PersonalAccessToken } from '../../services/api'
import { SettingsCard } from '../ui/SettingsCard'
import { Button } from '../ui/Button'
import { HostedAiAppGuide } from './HostedAiAppGuide'
import { AI_APPS, AiAppPicker, isHostedAiApp, type AiApp } from './AiAppPicker'
import { CommandRow, CopyButton } from './LocalAgentSetupSteps'

/** Local clients keep the existing full-account token API; hosted apps use browser sign-in. */
export function LocalMcpTokenPanel({ endpoint, origin = new URL(endpoint, window.location.origin).origin }: { endpoint: string; origin?: string }) {
  const [app, setApp] = useState<AiApp | null>(null)
  const [expiry, setExpiry] = useState(0)
  const [tokens, setTokens] = useState<PersonalAccessToken[]>([])
  const [issued, setIssued] = useState<{ id: string; token: string } | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState(false)
  useEffect(() => {
    let active = true
    authApi.listAccessTokens().then(data => { if (active) setTokens(data.tokens) })
      .catch(() => { if (active) setError('Could not load local access tokens.') })
    return () => { active = false }
  }, [])
  async function create() {
    setBusy(true); setError(''); setIssued(null); setCopied(false)
    try {
      const result = await authApi.createAccessToken({ name: 'agentworks-local', local_full_access: true, expires_in_days: expiry, scopes: [], workflow_ids: [], all_workflows: true })
      setIssued({ id: result.access_token.id, token: result.token })
      // Local preset tokens coexist with the other agents on this account.
      try { setTokens((await authApi.listAccessTokens()).tokens) }
      catch { setError('Token created, but the token list could not refresh. Copy your new token and reopen Connect to reload the list.') }
    } catch (cause) {
      const failure = cause as { response?: { data?: { error?: { code?: string; message?: string } } } }
      setError(failure.response?.data?.error?.code === 'local_token_limit'
        ? 'You have 32 active local access tokens. Revoke an unused token below before creating another.'
        : 'Could not create the access token. Try again.')
    }
    finally { setBusy(false) }
  }
  async function revoke(id: string) {
    setBusy(true); setError('')
    try {
      await authApi.revokeAccessToken(id)
      setTokens(current => current.filter(token => token.id !== id))
      if (issued?.id === id) setIssued(null)
    } catch { setError('Could not revoke the access token.') }
    finally { setBusy(false) }
  }
  const json = JSON.stringify({ ...(app === 'muse' ? { schema_version: 1 } : {}), mcpServers: { agentworks: { ...(app === 'claude-code' ? { type: 'http' } : {}), url: endpoint, headers: { Authorization: 'Bearer <ACCESS_TOKEN>' } } } }, null, 2)
  const config = app === 'codex' ? `[mcp_servers.agentworks]\nurl = ${JSON.stringify(endpoint)}\nhttp_headers = { Authorization = "Bearer <ACCESS_TOKEN>" }` : json
  const name = AI_APPS.find(item => item.id === app)?.name
  const file = app === 'codex' ? '~/.codex/config.toml' : app === 'cursor' ? '~/.cursor/mcp.json' : app === 'muse' ? '~/.config/muse/settings.json' : null
  const activeTokens = tokens.filter(token => !token.revoked_at && (token.non_expiring || new Date(token.expires_at).getTime() > Date.now()))
  return <div className="space-y-5">
    <AiAppPicker value={app} onChange={setApp} localOnly />
    {isHostedAiApp(app) && <HostedAiAppGuide app={app} endpoint={endpoint} localServer />}
    {app && !isHostedAiApp(app) && <section aria-label={`Connect ${name}`} className="space-y-5 rounded-xl border border-border p-4 sm:p-5">
      <h3 className="text-sm font-semibold">Connect {name}</h3>
      <div className="space-y-3">
        <h4 className="text-sm font-medium">1. Use a connection key</h4>
        <p className="text-xs text-muted-foreground">Create a key once, then keep using it until it expires or you revoke it. This lets {name} use AgentWorks with the same permissions as your local account. Keep the key private.</p>
        {activeTokens.length > 0 && !issued && <p className="rounded-md bg-muted/40 p-3 text-xs">You already have {activeTokens.length} active {activeTokens.length === 1 ? 'key' : 'keys'}. A working app stays connected. To set up another app, reuse a key you saved; existing keys cannot be shown again.</p>}
        <details open={activeTokens.length === 0 ? true : undefined} className="text-xs text-muted-foreground"><summary className="w-fit cursor-pointer">Need a new key?</summary><div className="mt-3 space-y-3"><label className="block">Expires after<select aria-label="Local token expiry" value={expiry} disabled={busy} onChange={event => setExpiry(Number(event.target.value))} className="ml-2 rounded border border-border bg-background p-2"><option value={0}>Until revoked</option><option value={7}>7 days</option><option value={30}>30 days</option><option value={90}>90 days</option></select></label><Button disabled={busy} onClick={() => void create()}>{busy ? 'Working…' : issued ? 'Create another key' : 'Create connection key'}</Button></div></details>
        {issued && <div className="space-y-2 rounded-md border border-border p-3"><p className="text-xs">Copy your key now. It is shown only for this session.</p><input aria-label="New access token" type="password" readOnly value={issued.token} className="w-full rounded border border-border bg-background p-2 text-xs" /><Button variant="outline" size="sm" onClick={() => void navigator.clipboard.writeText(issued.token).then(() => setCopied(true)).catch(() => setError('Could not copy the token.'))}><Copy className="mr-1 h-3 w-3" />{copied ? 'Copied' : 'Copy key'}</Button></div>}
      </div>
      <div className="space-y-3 border-t border-border pt-4">
        <h4 className="text-sm font-medium">2. Add AgentWorks to {name}</h4>
        {app === 'claude-code' ? <><p className="text-xs text-muted-foreground">In your terminal, replace &lt;ACCESS_TOKEN&gt; with your key, then run:</p><CommandRow label="Claude Code setup" command={`claude mcp add --transport http agentworks '${endpoint.replace(/'/g, "'\\''")}' --header 'Authorization: Bearer <ACCESS_TOKEN>'`} /><p className="text-xs text-muted-foreground">In Claude Code, use /mcp to check the connection.</p></>
          : <><p className="text-xs text-muted-foreground">{file ? <>Open <code>{file}</code>. Add the settings below, keeping any existing connections.</> : 'In your app’s MCP settings, add this configuration.'} Replace &lt;ACCESS_TOKEN&gt; with your key.</p><div className="flex justify-end"><CopyButton text={config} label="MCP client configuration" /></div><pre className="overflow-x-auto rounded-md border border-border bg-muted/40 p-3 text-xs" aria-label="Local MCP client config">{config}</pre><p className="text-xs text-muted-foreground">Reopen your app or reload its MCP connections after saving.</p></>}
        <details className="text-xs text-muted-foreground"><summary className="w-fit cursor-pointer">Manual connection details</summary><div className="mt-2 space-y-2"><CommandRow label="MCP server URL" command={endpoint} /><p>Authorization header: <code>Bearer &lt;ACCESS_TOKEN&gt;</code></p>{app === 'claude-code' && <pre className="overflow-x-auto rounded-md border border-border p-3" aria-label="Local MCP client config">{config}</pre>}</div></details>
      </div>
      <div className="space-y-2 border-t border-border pt-4"><h4 className="text-sm font-medium">3. Try it in {name}</h4><p className="text-xs text-muted-foreground">Start a chat and ask:</p><CommandRow label="Try your connection" command="What can I do with AgentWorks?" /></div>
    </section>}
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
    {activeTokens.length > 0 && <details className="rounded-xl border border-border p-4 text-sm"><summary className="cursor-pointer font-medium focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">Manage connection keys ({activeTokens.length})</summary><div className="mt-3"><SettingsCard title="Connection keys" description="Revoking a key disconnects every app using it."><div className="space-y-2">{activeTokens.map(token => <div key={token.id} className="flex items-center justify-between gap-2 text-xs"><div><p>{token.name}</p><p className="text-muted-foreground">{token.non_expiring ? 'Valid until removed' : `Expires ${new Date(token.expires_at).toLocaleDateString()}`}</p></div><Button variant="outline" size="sm" disabled={busy} onClick={() => void revoke(token.id)}>Revoke</Button></div>)}</div></SettingsCard></div></details>}
  </div>
}
