import { useEffect, useState } from 'react'
import { Check, Copy, Loader2, PlugZap, Unplug } from 'lucide-react'
import api from '../../services/api'
import { SettingsCard } from '../../components/ui/SettingsCard'
import { Button } from '../../components/ui/Button'

type Connection = { id: string; client_name: string }

export function GatewayConnectPanel({ base: _base }: { base: string }) {
  const [endpoint, setEndpoint] = useState('')
  const [connections, setConnections] = useState<Connection[]>([])
  const [error, setError] = useState('')
  const [copied, setCopied] = useState(false)
  const [testing, setTesting] = useState(false)
  const [result, setResult] = useState<{ ok: boolean; text: string } | null>(null)
  const [revoking, setRevoking] = useState('')

  useEffect(() => {
    let active = true
    void Promise.all([
      api.get<{ endpoint: string }>('/api/vault/connection'),
      api.get<{ connections: Connection[] }>('/api/oauth/vault/connections'),
    ]).then(([config, grants]) => {
      if (active) { setEndpoint(config.data.endpoint); setConnections(grants.data.connections ?? []) }
    }).catch(() => { if (active) setError('Could not load the Vault connection. Check the server configuration.') })
    return () => { active = false }
  }, [])

  async function onCopy() {
    setCopied(false)
    try {
      if (!navigator.clipboard?.writeText) throw new Error('Clipboard unavailable')
      await navigator.clipboard.writeText(endpoint)
      setCopied(true)
    } catch { setError('Could not copy. Select the endpoint URL and copy it manually.') }
  }

  async function onTest() {
    setTesting(true)
    setResult(null)
    try {
      const resp = await fetch(endpoint, { credentials: 'omit', headers: { Accept: 'application/json' } })
      const challenge = resp.headers.get('WWW-Authenticate') ?? ''
      setResult(resp.status === 401 && challenge.includes('oauth-protected-resource')
        ? { ok: true, text: 'Endpoint reachable — sign-in required.' }
        : { ok: false, text: `Unexpected answer (HTTP ${resp.status}).` })
    } catch { setResult({ ok: false, text: 'Endpoint unreachable. Check the server connection.' }) }
    finally { setTesting(false) }
  }

  async function revoke(id: string) {
    setRevoking(id)
    try {
      await api.delete(`/api/oauth/vault/connections/${encodeURIComponent(id)}`)
      setConnections(current => current.filter(connection => connection.id !== id))
    } catch { setError('Could not disconnect this client. Try again.') }
    finally { setRevoking('') }
  }

  return <div className="space-y-4" data-testid="gateway-connect">
    <SettingsCard icon={<PlugZap className="h-4 w-4 text-primary" />} title="Vault MCP endpoint" description="Sign in with your platform account.">
      <p className="flex flex-wrap items-center gap-2">
        <code className="break-all rounded bg-muted px-2 py-1 font-mono text-xs" data-testid="gateway-connect-url">{endpoint || 'Loading…'}</code>
        <Button variant="outline" size="xs" disabled={!endpoint} onClick={() => void onCopy()} data-testid="gateway-connect-copy">
          {copied ? <Check /> : <Copy />}{copied ? 'Copied' : 'Copy'}
        </Button>
      </p>
      <div className="flex flex-wrap items-center gap-2">
        <Button variant="outline" size="xs" disabled={!endpoint || testing} onClick={() => void onTest()} data-testid="gateway-connect-test">
          {testing && <Loader2 className="animate-spin" />}Send test request
        </Button>
        {result && <span role="status" className={result.ok ? 'text-emerald-600 dark:text-emerald-400' : 'text-destructive'}>{result.text}</span>}
      </div>
    </SettingsCard>
    <SettingsCard title="Connect Claude" description="OAuth sign-in.">
      <ol className="list-decimal space-y-1 pl-5 text-sm text-muted-foreground">
        <li>Add the endpoint URL as an MCP connection in your client.</li>
        <li>Sign in with your platform account and allow access.</li>
        <li>Your current group permissions control the tools you can use.</li>
      </ol>
    </SettingsCard>
    {connections.length > 0 && <SettingsCard title="Connected clients">
      {connections.map(connection => <div key={connection.id} className="flex items-center justify-between gap-3 py-2 text-sm">
        <span>{connection.client_name}</span>
        <Button variant="outline" size="xs" disabled={revoking === connection.id} onClick={() => void revoke(connection.id)}><Unplug />Disconnect</Button>
      </div>)}
    </SettingsCard>}
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
  </div>
}
