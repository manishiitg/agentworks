import { useCallback, useEffect, useState } from 'react'
import { Check, Copy, KeyRound, Plug, Trash2 } from 'lucide-react'
import { authApi, getApiBaseUrl, type PersonalAccessToken } from '../../services/api'
import { knowledgebaseError, type KnowledgeIdentity } from '../../services/knowledgebaseApi'
import { SecretField } from '../../components/ui/SecretField'
import { Button } from '../../components/ui/Button'
import ConfirmationDialog from '../../components/ui/ConfirmationDialog'

const field = 'w-full rounded-md border border-border bg-background px-3 py-2 text-sm'
export function knowledgebaseMcpURL(origin: string): string { return `${origin.replace(/\/+$/, '')}/api/external/v1/mcp` }
export function knowledgebaseTokenCaps(mode: 'folder' | 'grants' | 'none', folder: string, write: boolean) {
  return mode === 'grants' ? null : mode === 'none' ? [] : [{ folder_path: folder, role: write ? 'editor' as const : 'reader' as const }]
}
function CopyControl({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = useState(false)
  const [error, setError] = useState(false)
  return <button type="button" aria-label={`Copy ${label}`} title={copied ? 'Copied' : `Copy ${label}`} className="rounded-md p-2 text-muted-foreground hover:bg-muted" onClick={() => { void navigator.clipboard.writeText(value).then(() => { setCopied(true); setError(false) }).catch(() => setError(true)) }}>{copied ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}{error && <span className="ml-1 text-xs">Select and copy manually</span>}</button>
}

export function KnowledgebaseConnectPanel({ folder, isAdmin, identities, onAsk }: { folder: string; isAdmin: boolean; identities: KnowledgeIdentity[]; onAsk: () => void }) {
  const [tokens, setTokens] = useState<PersonalAccessToken[]>([])
  const [name, setName] = useState('')
  const [write, setWrite] = useState(false)
  const [scopeMode, setScopeMode] = useState<'folder' | 'grants' | 'none'>('folder')
  const [scopeFolder, setScopeFolder] = useState(folder)
  const [identityId, setIdentityId] = useState('')
  const [days, setDays] = useState(30)
  const [secret, setSecret] = useState('')
  const [createdId, setCreatedId] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [revokeTarget, setRevokeTarget] = useState<PersonalAccessToken | null>(null)
  const endpoint = knowledgebaseMcpURL(getApiBaseUrl() || window.location.origin)
  const services = identities.filter(identity => (identity.type === 'service_account' || identity.type === 'service') && !identity.disabled)
  const refresh = useCallback(async () => {
    const result = await authApi.listAccessTokens()
    setTokens((result.tokens || []).filter(token => token.scopes?.some(scope => scope.startsWith('knowledgebase:'))))
  }, [])
  useEffect(() => { let cancelled = false; void refresh().catch(error => { if (!cancelled) setError(knowledgebaseError(error)) }); return () => { cancelled = true } }, [refresh])
  useEffect(() => { setScopeFolder(folder) }, [folder])
  useEffect(() => { if (identityId && !services.some(identity => identity.id === identityId)) setIdentityId('') }, [identityId, services])
  async function create() {
    setError(''); setBusy(true); setSecret(''); setCreatedId('')
    try {
      const result = await authApi.createAccessToken({ name: name.trim(), scopes: write ? ['knowledgebase:read', 'knowledgebase:write'] : ['knowledgebase:read'], workflow_ids: [], all_workflows: false, expires_in_days: days, knowledgebase_folders: knowledgebaseTokenCaps(scopeMode, scopeFolder.trim(), write), ...(identityId ? { knowledgebase_identity_id: identityId } : {}) })
      setSecret(result.token); setCreatedId(result.access_token.id); setName(''); await refresh()
    } catch (error) { setError(knowledgebaseError(error)) }
    finally { setBusy(false) }
  }
  async function revoke() {
    if (!revokeTarget) return
    setBusy(true); setError('')
    try { await authApi.revokeAccessToken(revokeTarget.id); if (createdId === revokeTarget.id) setSecret(''); setRevokeTarget(null); await refresh() }
    catch (error) { setError(knowledgebaseError(error)) }
    finally { setBusy(false) }
  }
  const config = JSON.stringify({ mcpServers: { knowledgebase: { type: 'http', url: endpoint, headers: { Authorization: 'Bearer YOUR_TOKEN' } } } }, null, 2)
  return <section className="mx-auto max-w-4xl space-y-6 p-4 sm:p-7">
    <div className="flex items-center gap-3"><Plug className="h-5 w-5 text-primary" /><h1 className="text-xl font-semibold">Connect agents</h1></div>
    <p className="text-sm text-muted-foreground">Use this MCP connection from Crew, Code, workflows, or a local agent. Folder permissions are checked on every call.</p>
    <div className="rounded-xl border border-border p-4"><h2 className="text-sm font-semibold">MCP endpoint</h2><div className="mt-2 flex items-center gap-2"><code className="min-w-0 flex-1 overflow-x-auto text-xs">{endpoint}</code><CopyControl value={endpoint} label="MCP endpoint" /></div><p className="mt-2 text-xs text-muted-foreground">Use HTTP transport with an Authorization header. For Claude Code, merge this connection into your MCP configuration.</p><div className="mt-3 flex items-start gap-1"><pre className="min-w-0 flex-1 overflow-x-auto rounded-lg bg-muted p-3 text-xs">{config}</pre><CopyControl value={config} label="MCP configuration" /></div><p className="mt-3 text-xs leading-5 text-muted-foreground">Discover operations with <code>get_api_spec(names=["read_knowledgebase", "update_knowledgebase"])</code>, then invoke <code>call_tool(name="read_knowledgebase", arguments=&#123;…&#125;)</code>. Saves are immediately visible. Writers explicitly call <code>commit_knowledgebase</code> and <code>push_knowledgebase</code> for Git backup.</p></div>
    <form className="space-y-4 rounded-xl border border-border p-4" onSubmit={event => { event.preventDefault(); void create() }}>
      <div className="flex items-center gap-2"><KeyRound className="h-4 w-4 text-primary" /><h2 className="text-sm font-semibold">Create a revocable token</h2></div>
      <div className="grid gap-4 sm:grid-cols-2"><label className="space-y-1 text-xs">Connection name<input className={field} value={name} maxLength={80} required onChange={event => setName(event.target.value)} placeholder="Claude Code · Payments" /></label><label className="space-y-1 text-xs">Permission<select className={field} value={write ? 'write' : 'read'} onChange={event => setWrite(event.target.value === 'write')}><option value="read">Read</option><option value="write">Read and write</option></select></label></div>
      <label className="block space-y-1 text-xs">Folder restriction<select className={field} value={scopeMode} onChange={event => setScopeMode(event.target.value as typeof scopeMode)}><option value="folder">One folder and its descendants</option><option value="grants">All folders allowed by this identity’s live grants</option><option value="none">No folders</option></select></label>
      {scopeMode === 'folder' && <label className="block space-y-1 text-xs">Folder path<input className={field} value={scopeFolder} maxLength={1024} onChange={event => setScopeFolder(event.target.value)} placeholder="Blank for organization root" /></label>}
      <div className="grid gap-4 sm:grid-cols-2"><label className="space-y-1 text-xs">Expires in days<input type="number" min={1} max={90} className={field} value={days} required onChange={event => setDays(Number(event.target.value))} /></label>{isAdmin && <label className="space-y-1 text-xs">Act as<select className={field} value={identityId} onChange={event => setIdentityId(event.target.value)}><option value="">My user account</option>{services.map(identity => <option key={identity.id} value={identity.id}>{identity.name}</option>)}</select></label>}</div>
      {isAdmin && <p className="text-xs text-muted-foreground">Service accounts have their own folder grants. <button type="button" onClick={onAsk} className="text-primary hover:underline">Create or disable a service account in access chat.</button></p>}
      <p className="text-xs text-muted-foreground">A token can only narrow access. Read and write still require the chosen identity’s live folder grants.</p>
      <Button type="submit" size="sm" disabled={busy || !name.trim() || !Number.isInteger(days) || days < 1 || days > 90}>{busy ? 'Working…' : 'Create token'}</Button>
    </form>
    {secret && <div className="space-y-3 rounded-xl border border-primary/40 bg-primary/5 p-4"><SecretField label="New connection token" value={secret} onChange={() => {}} hint="Copy now. This token is only shown once and is cleared when this view closes." /><div className="flex items-center gap-2"><CopyControl value={secret} label="connection token" /><Button variant="outline" size="sm" onClick={() => setSecret('')}>Dismiss token</Button></div></div>}
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    <div className="rounded-xl border border-border p-4"><h2 className="mb-3 text-sm font-semibold">Knowledge Base connections</h2>{!tokens.length ? <p className="text-xs text-muted-foreground">No tokens yet.</p> : <ul className="divide-y divide-border">{tokens.map(token => <li key={token.id} className="flex items-center justify-between gap-3 py-3"><div className="min-w-0"><p className="truncate text-sm font-medium">{token.name}</p><p className="mt-1 text-xs text-muted-foreground">{token.scopes.includes('knowledgebase:write') ? 'Read and write' : 'Read'}{token.knowledgebase_identity_id ? ` · ${token.knowledgebase_identity_id}` : ''} · {token.revoked_at ? 'Revoked' : new Date(token.expires_at).getTime() <= Date.now() ? 'Expired' : `Expires ${new Date(token.expires_at).toLocaleDateString()}`}</p></div>{!token.revoked_at && <Button variant="outline" size="sm" disabled={busy} onClick={() => setRevokeTarget(token)}><Trash2 className="mr-1 h-3.5 w-3.5" />Revoke</Button>}</li>)}</ul>}</div>
    <ConfirmationDialog isOpen={!!revokeTarget} onClose={() => setRevokeTarget(null)} onConfirm={() => void revoke()} title="Revoke connection?" message={`Agents using “${revokeTarget?.name || ''}” will lose access immediately.`} confirmText="Revoke" isLoading={busy} loadingText="Revoking…" />
  </section>
}
