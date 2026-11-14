import { useCallback, useEffect, useRef, useState } from 'react'
import { Plus, X } from 'lucide-react'
import { McpConnectionsPanel } from './McpConnectionsPanel'
import { useVaultMcpConnections } from './useVaultMcpConnections'
import { vaultConnectionsApi, type VaultCatalogApp, type VaultConnectionResult, type VaultConnectionRow } from '../../api/vaultConnections'
import { Button } from '../ui/Button'
import { Input } from '../ui/Input'
import ModalPortal from '../ui/ModalPortal'
import ConfirmationDialog from '../ui/ConfirmationDialog'

const errorText = (cause: unknown) => {
  const message = (cause as { response?: { data?: { error?: string } } })?.response?.data?.error
  return message || 'Could not update this connection. Try again.'
}
const safeSignIn = (url?: string) => {
  try { const parsed = new URL(url || ''); return parsed.protocol === 'https:' || parsed.protocol === 'http:' ? parsed.href : undefined }
  catch { return undefined }
}
type FormTarget = { server?: VaultConnectionRow; provider?: string }

/** Saved account management only; the browser never supplies a Crew or Vault owner. */
export default function AccountMcpPanel() {
  const vault = useVaultMcpConnections()
  const [catalog, setCatalog] = useState<VaultCatalogApp[]>([])
  const [catalogError, setCatalogError] = useState('')
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [result, setResult] = useState<VaultConnectionResult>()
  const [target, setTarget] = useState<FormTarget>()
  const [removing, setRemoving] = useState<VaultConnectionRow>()
  const [busy, setBusy] = useState(false)
  const alive = useRef(true)
  const loadCatalog = useCallback(async () => {
    try { const items = await vaultConnectionsApi.catalog(); if (alive.current) { setCatalog(items); setCatalogError('') } }
    catch { if (alive.current) setCatalogError('Could not load available servers.') }
  }, [])
  useEffect(() => { alive.current = true; void loadCatalog(); return () => { alive.current = false } }, [loadCatalog])
  const refresh = () => { void vault.refresh(); void loadCatalog() }
  async function operate(operation: Parameters<typeof vaultConnectionsApi.operate>[0], fields: Record<string, string>) {
    setBusy(true); setError(''); setMessage('')
    try {
      const next = await vaultConnectionsApi.operate(operation, fields)
      if (!alive.current) return
      setResult(next)
      setMessage(next.message || (operation === 'remove_connection' ? 'Connection removed from Vault.' : operation === 'sync' ? 'Connection checked and tools refreshed.' : 'Connection saved. Check its status below.'))
      setTarget(undefined); setRemoving(undefined)
      await vault.refresh()
    } catch (cause) { if (alive.current) setError(errorText(cause)) }
    finally { if (alive.current) setBusy(false) }
  }
  const authUrl = safeSignIn(result?.auth_url)
  return <>
      {busy && <p role="status" className="text-sm text-muted-foreground">Updating connection…</p>}
      {message && <div role="status" className="space-y-2 rounded-md border border-border p-3 text-sm"><p>{message}</p>
        {authUrl && <a href={authUrl} target="_blank" rel="noopener noreferrer" className="inline-block underline">Open sign-in page</a>}
        {result?.setup && <div className="space-y-2"><p>This service needs OAuth app details before sign-in can start.</p>{result.setup.redirect_uri && <p className="break-all text-xs text-muted-foreground">Register callback: {result.setup.redirect_uri}</p>}<Button size="sm" variant="outline" onClick={() => setTarget({ server: { id: result.connection_id!, label: 'Saved connection', provider: '', status: 'authentication_required', can_manage: true, tools: [] } })}>Enter app details</Button></div>}
        <p className="text-xs text-muted-foreground">After signing in, refresh to check discovery. Saving is not proof of a successful connection.</p>
      </div>}
    <McpConnectionsPanel density="directory"
      description="Manage your saved MCP connections here. Choose what each Crew uses in that Crew’s settings."
      headerActions={vault.canConnect && <Button size="sm" onClick={() => { setError(''); setTarget({}) }} disabled={busy}><Plus className="h-4 w-4" />Add MCP server</Button>}
      servers={vault.servers.map(server => {
        const saved = vault.items.find(item => 'vault_' + item.id === server.id)!
        return { ...server, source: 'Remote · ' + (server.source || 'Saved in Vault'),
          actions: saved.can_manage ? [
            { label: 'Test connection', disabled: busy, run: () => operate('sync', { connection_id: saved.id }) },
            { label: 'Sign in', disabled: busy, run: () => operate('sign_in', { connection_id: saved.id }) },
            { label: 'Edit connection', disabled: busy, run: () => { setError(''); setTarget({ server: saved }) } },
            { label: 'Remove connection', destructive: true, disabled: busy, run: () => { setError(''); setRemoving(saved) } },
          ] : [],
          controls: !saved.can_manage ? <span className="text-xs text-muted-foreground">Managed by owner</span>
            : saved.status === 'authentication_required' || saved.status === 'pending' ? <Button size="xs" variant="outline" disabled={busy} onClick={() => void operate('sign_in', { connection_id: saved.id })}>Sign in</Button> : undefined,
        }
      })}
      catalog={catalog.map(app => ({ id: app.name, name: app.name, description: app.needs_admin_setup ? 'OAuth app setup required' : app.oauth ? 'Sign in with your account' : 'Connect an account',
        connect: vault.canConnect ? { label: 'Connect', disabled: busy, run: () => { setError(''); setTarget({ provider: app.name }) } } : undefined,
      }))}
      loading={vault.loading} refresh={refresh}
      notices={[...vault.notices, ...(catalogError ? [{ message: catalogError, retry: refresh }] : []), ...(error && !target && !removing ? [{ message: error }] : [])]}
    >

    </McpConnectionsPanel>
    {target && <McpAccountForm target={target} busy={busy} error={error} close={() => setTarget(undefined)}
      submit={fields => operate(target.server ? 'edit_connection' : 'connect', fields)} />}
    <ConfirmationDialog isOpen={Boolean(removing)} onClose={() => { if (!busy) setRemoving(undefined) }} onConfirm={() => { if (removing) void operate('remove_connection', { connection_id: removing.id }) }}
      title={'Remove ' + (removing?.label || 'connection') + '?'} message={error || 'Delete this saved connection and its stored sign-in from Vault. Crews using it will lose access. This does not revoke permission at the external service.'}
      confirmText="Remove connection" isLoading={busy} loadingText="Removing…" />
  </>
}

function McpAccountForm({ target, busy, error, close, submit }: {
  target: FormTarget; busy: boolean; error: string; close: () => void; submit: (fields: Record<string, string>) => Promise<void>
}) {
  const [label, setLabel] = useState(target.server?.label || target.provider || '')
  const [url, setUrl] = useState('')
  const [auth, setAuth] = useState('auto')
  const [key, setKey] = useState('')
  const [header, setHeader] = useState('Authorization')
  const [format, setFormat] = useState('Bearer {}')
  const [clientID, setClientID] = useState('')
  const [clientSecret, setClientSecret] = useState('')
  const input = useRef<HTMLInputElement>(null)
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null
    input.current?.focus()
    return () => previous?.focus()
  }, [])
  useEffect(() => {
    const listener = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !busy) close()
      if (event.key === 'Tab') {
        const elements = [...(input.current?.closest('[role="dialog"]')?.querySelectorAll<HTMLElement>('button:not(:disabled),input:not(:disabled),select:not(:disabled)') ?? [])]
        const first = elements[0], last = elements.at(-1)
        if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus() }
        else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
      }
    }
    document.addEventListener('keydown', listener); return () => document.removeEventListener('keydown', listener)
  }, [busy, close])
  return <ModalPortal><div className="fixed inset-0 z-[100] flex items-center justify-center bg-black/60 p-4">
    <section role="dialog" aria-modal="true" aria-labelledby="mcp-account-title" className="max-h-[90vh] w-full max-w-lg overflow-y-auto rounded-xl border border-border bg-background p-6 shadow-xl">
      <div className="flex items-center justify-between"><h2 id="mcp-account-title" className="text-lg font-semibold">{target.server ? 'Edit MCP connection' : 'Add MCP server'}</h2><Button variant="ghost" size="icon" aria-label="Close connection form" disabled={busy} onClick={close}><X className="h-4 w-4" /></Button></div>
      <p className="mt-2 text-sm text-muted-foreground">Saved in Vault. No Crew selection is changed.</p>
      <form className="mt-5 space-y-4" onSubmit={event => { event.preventDefault(); const fields: Record<string, string> = { label: label.trim() }
        if (target.server) fields.connection_id = target.server.id
        else if (target.provider) fields.provider = target.provider
        else fields.name = label.trim()
        if (url.trim()) fields.url = url.trim()
        if (auth === 'key') { fields.api_key = key; fields.api_key_header = header.trim(); fields.api_key_format = format }
        if (auth === 'app') { fields.client_id = clientID.trim(); if (clientSecret) fields.client_secret = clientSecret }
        void submit(fields)
      }}>
        <label className="block space-y-1 text-xs">Connection name<Input ref={input} required maxLength={100} value={label} onChange={event => setLabel(event.target.value)} disabled={busy} /></label>
        {(!target.provider || target.server) && <label className="block space-y-1 text-xs">MCP server URL<Input type="url" required={!target.server} placeholder={target.server ? 'Leave blank to keep the saved address' : 'https://example.com/mcp'} value={url} onChange={event => setUrl(event.target.value)} disabled={busy} /><span className="text-muted-foreground">Remote HTTP/SSE servers. Local command setup is not offered here.</span></label>}
        <label className="block space-y-1 text-xs">Authentication<select aria-label="Authentication" className="h-9 w-full rounded-md border border-input bg-background px-2" value={auth} disabled={busy} onChange={event => setAuth(event.target.value)}>
          <option value="auto">{target.server ? 'Keep existing sign-in' : 'Use service sign-in if required'}</option><option value="key">API key</option><option value="app">OAuth app details</option>
        </select></label>
        {auth === 'key' && <><label className="block space-y-1 text-xs">API key<Input type="password" autoComplete="off" required value={key} disabled={busy} onChange={event => setKey(event.target.value)} /></label>
          <details><summary className="cursor-pointer text-xs text-muted-foreground">Header settings</summary><div className="mt-3 space-y-3"><label className="block text-xs">Header name<Input required value={header} onChange={event => setHeader(event.target.value)} /></label><label className="block text-xs">Format<Input required value={format} onChange={event => setFormat(event.target.value)} /><span className="text-muted-foreground">Use {} for the key, for example Bearer {}.</span></label></div></details></>}
        {auth === 'app' && <><label className="block space-y-1 text-xs">OAuth client ID<Input required value={clientID} disabled={busy} onChange={event => setClientID(event.target.value)} /></label><label className="block space-y-1 text-xs">OAuth client secret<Input type="password" autoComplete="off" value={clientSecret} disabled={busy} onChange={event => setClientSecret(event.target.value)} /></label></>}
        {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
        <div className="flex justify-end gap-2"><Button type="button" variant="outline" onClick={close} disabled={busy}>Cancel</Button><Button type="submit" disabled={busy}>{busy ? 'Saving…' : 'Save connection'}</Button></div>
      </form>
    </section>
  </div></ModalPortal>
}
