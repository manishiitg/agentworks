import { knowledgebaseApi, knowledgebaseError, type KnowledgeAccess } from '../../services/knowledgebaseApi'
import { ShieldCheck, CornerUpLeft, UsersRound } from 'lucide-react'
import { useState, type FormEvent } from 'react'
import { WorkspacePageContent } from '../../components/workspace/WorkspacePageContent'

const roleLabel = (role?: string) => ({ reader: 'Can open', editor: 'Can edit', owner: 'Owner' } as Record<string, string>)[role || ''] || role || 'No folder access'

export function KnowledgebaseAccessPanel({ access, onFolder, onPrepared, isAdmin = false }: { access: KnowledgeAccess; onPrepared: () => void; isAdmin?: boolean; onFolder: (path: string) => void }) {
  const [askError, setAskError] = useState('')
  const [identity, setIdentity] = useState('')
  const [role, setRole] = useState('Reader')
  const [serviceName, setServiceName] = useState('')
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState('')
  async function prepare(action: string, extra: Record<string, unknown>) {
    if (busy || !canManage) return
    setBusy(true); setAskError(''); setNotice('')
    try {
      await knowledgebaseApi.prepareAccess({ action, folder_path: access.folder_path, request_id: crypto.randomUUID(), ...(action === 'grant' || action === 'revoke' ? { expected_acl_version: access.acl_version } : {}), ...extra })
      setNotice('Review and approve the pending change above. Access has not changed yet.'); onPrepared()
    } catch (cause) { setAskError(knowledgebaseError(cause)) }
    finally { setBusy(false) }
  }
  const names = new Map((access.identities || []).map(identity => [identity.id, identity.name]))
  const canManage = access.effective_role === 'owner'
  return <WorkspacePageContent as="section" className="max-w-4xl space-y-5">
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div className="flex items-center gap-2"><ShieldCheck className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" /><p className="break-all text-sm font-medium">{access.folder_path || 'Organization · all Brain folders'}</p></div>
      <p className="rounded-full bg-muted px-2.5 py-1 text-xs text-muted-foreground">Your access: <strong className="text-foreground">{roleLabel(access.effective_role)}</strong></p>
    </div>
    <div className="flex flex-wrap items-center justify-between gap-3"><h2 className="flex items-center gap-2 text-sm font-semibold"><UsersRound className="h-4 w-4 text-muted-foreground" aria-hidden="true" />Who has access</h2></div>
    {askError && <p role="alert" className="text-xs text-destructive">{askError}</p>}
    {!access.grants?.length ? <div className="rounded-lg border border-border p-5"><p className="text-sm text-muted-foreground">{canManage ? 'No shared access is listed for this folder.' : 'Only folder owners can view the full access list.'}</p>{canManage && <p className="mt-1 text-xs text-muted-foreground">Choose a person or service account below to share this folder.</p>}</div> : <div className="overflow-x-auto rounded-xl border border-border"><table className="w-full text-left text-sm"><thead className="bg-muted/40 text-xs text-muted-foreground"><tr><th className="p-3 font-medium">Person or service account</th><th className="p-3 font-medium">Access</th><th className="p-3 font-medium">Shared from</th>{canManage && <th className="p-3 font-medium"><span className="sr-only">Actions</span></th>}</tr></thead><tbody>{access.grants.map(grant => <tr key={`${grant.folder_path}:${grant.identity_id}`} className="border-t border-border"><td className="p-3"><span title={grant.identity_id}>{names.get(grant.identity_id) || grant.identity_id}</span></td><td className="p-3">{roleLabel(grant.role)}</td><td className="p-3"><button type="button" onClick={() => onFolder(grant.folder_path)} className="break-all text-primary hover:underline">{grant.folder_path || 'Organization · all Brain folders'}</button>{grant.inherited && <span className="mt-1 flex items-center gap-1 text-xs text-muted-foreground"><CornerUpLeft className="h-3 w-3" aria-hidden="true" />From a parent folder</span>}</td>{canManage && <td className="p-3">{!grant.inherited && <button type="button" disabled={busy} onClick={() => void prepare('revoke', { identity_id: grant.identity_id })} className="text-xs text-destructive hover:underline disabled:opacity-50">Remove</button>}</td>}</tr>)}</tbody></table></div>}
    {canManage && <form onSubmit={(event: FormEvent) => { event.preventDefault(); if (identity) void prepare('grant', { identity_id: identity, role }) }} className="space-y-3 rounded-xl border border-border p-4">
      <h3 className="text-sm font-medium">Share this folder</h3>
      <label className="block space-y-1 text-xs">Person or service account<select required value={identity} onChange={event => setIdentity(event.target.value)} className="w-full rounded-md border border-border bg-background p-2"><option value="">Choose someone</option>{access.identities.filter(item => !item.disabled).map(item => <option key={item.id} value={item.id}>{item.name} ({item.type})</option>)}</select></label>
      <label className="block space-y-1 text-xs">Access<select value={role} onChange={event => setRole(event.target.value)} className="w-full rounded-md border border-border bg-background p-2"><option value="Reader">Can open</option><option value="Editor">Can edit</option><option value="Owner">Owner</option></select></label>
      <p className="text-xs text-muted-foreground">{role === 'Owner' ? 'Owners can also manage who has access to this folder and its nested folders.' : role === 'Editor' ? 'Can save changes to this folder and its nested folders.' : 'Can open files in this folder and its nested folders.'}</p>
      <button disabled={busy || !identity} type="submit" className="rounded-md border border-border px-3 py-2 text-xs hover:bg-muted disabled:opacity-50">Review access change</button>
    </form>}
    {canManage && isAdmin && <details className="rounded-lg border border-border p-3 text-xs"><summary className="w-fit cursor-pointer">Service accounts</summary><form onSubmit={event => { event.preventDefault(); if (serviceName.trim()) void prepare('create_service_account', { name: serviceName.trim() }) }} className="mt-3 flex gap-2"><input aria-label="Service account name" required value={serviceName} onChange={event => setServiceName(event.target.value)} placeholder="Account name" className="min-w-0 flex-1 rounded border border-border bg-background p-2" /><button type="submit" disabled={busy} className="rounded border border-border px-2">Review new account</button></form>{access.identities.filter(item => item.type === 'service' && !item.disabled).map(item => <div key={item.id} className="mt-2 flex justify-between gap-2"><span>{item.name}</span><button disabled={busy} type="button" onClick={() => void prepare('disable_service_account', { identity_id: item.id })} className="text-destructive">Disable</button></div>)}</details>}
    {notice && <p role="status" className="text-xs text-muted-foreground">{notice}</p>}
    <details className="text-xs leading-5 text-muted-foreground"><summary className="w-fit cursor-pointer font-medium">About folder permissions</summary><div className="mt-2 space-y-2"><p><strong>Can open:</strong> read files. <strong>Can edit:</strong> save content and back it up through MCP. <strong>Owner:</strong> also manage folder access.</p><p>Access applies to nested folders. Access inherited from a parent is changed at that parent folder.</p><p>Review each change before approving it. Permissions are checked again when it is applied.</p></div></details>
  </WorkspacePageContent>
}
