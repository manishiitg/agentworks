import { useEffect, useState } from 'react'
import { Copy, Plug } from 'lucide-react'
import { authApi, type PersonalAccessToken } from '../../services/api'
import { SettingsCard } from '../ui/SettingsCard'
import { Button } from '../ui/Button'

/** Local clients use the existing scoped token API; hosted setup retains OAuth. */
export function LocalMcpTokenPanel({ endpoint }: { endpoint: string }) {
  const [name, setName] = useState('Local agent')
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
      const result = await authApi.createAccessToken({ name: name.trim(), local_full_access: true, scopes: [], workflow_ids: [], all_workflows: true })
      setIssued({ id: result.access_token.id, token: result.token })
      // The server may replace a previous legacy (non-KB) token.
      try { setTokens((await authApi.listAccessTokens()).tokens) }
      catch { setError('Token created, but the token list could not refresh. Copy your new token and reopen Connect to reload the list.') }
    } catch { setError('Could not create the access token. Try again.') }
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
  const config = JSON.stringify({ mcpServers: { agentworks: { url: endpoint, headers: { Authorization: 'Bearer <ACCESS_TOKEN>' } } } }, null, 2)
  const activeTokens = tokens.filter(token => !token.revoked_at && (token.non_expiring || new Date(token.expires_at).getTime() > Date.now()))
  return <div className="space-y-5">
    <div><h3 className="text-base font-semibold">Connect a local AI agent</h3><p className="mt-1 text-sm text-muted-foreground">Create an access token and use it with your agent’s HTTP MCP connection. No browser sign-in is needed.</p></div>
    <SettingsCard icon={<Plug className="h-4 w-4 text-primary" />} title="Local access token" description="Valid until you remove it. Includes all access available to your local account. Your current folder, workflow and Crew permissions still apply.">
      <div className="space-y-3">
        <label className="block text-xs">Name<input aria-label="Access token name" maxLength={80} value={name} disabled={busy} onChange={event => setName(event.target.value)} className="mt-1 w-full rounded-md border border-border bg-background p-2" /></label>

        <Button disabled={busy || !name.trim()} onClick={() => void create()}>{busy ? 'Working…' : 'Create access token'}</Button>
        {issued && <div className="space-y-2 rounded-md border border-border p-3"><p className="text-xs">Copy this token now. It is shown only for this session.</p><input aria-label="New access token" type="password" readOnly value={issued.token} className="w-full rounded border border-border bg-background p-2 text-xs" /><Button variant="outline" size="sm" onClick={() => void navigator.clipboard.writeText(issued.token).then(() => setCopied(true)).catch(() => setError('Could not copy the token.'))}><Copy className="mr-1 h-3 w-3" />{copied ? 'Copied' : 'Copy token'}</Button></div>}
        {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
      </div>
    </SettingsCard>
    <SettingsCard title="MCP client configuration" description="Works with HTTP MCP clients that support Authorization headers.">
      <pre className="overflow-x-auto rounded-md border border-border bg-muted/40 p-3 text-xs" aria-label="Local MCP client config">{config}</pre>
      <p className="mt-2 text-xs text-muted-foreground">Replace &lt;ACCESS_TOKEN&gt; with your copied token. This localhost endpoint is for agents on this computer.</p>
    </SettingsCard>
    {activeTokens.length > 0 && <SettingsCard title="Local access tokens" description="Revoke a token to disconnect its agent."><div className="space-y-2">{activeTokens.map(token => <div key={token.id} className="flex items-center justify-between gap-2 text-xs"><div><p>{token.name}</p><p className="text-muted-foreground">{token.non_expiring ? 'Valid until removed' : `Expires ${new Date(token.expires_at).toLocaleDateString()}`}</p></div><Button variant="outline" size="sm" disabled={busy} onClick={() => void revoke(token.id)}>Revoke</Button></div>)}</div></SettingsCard>}
  </div>
}
