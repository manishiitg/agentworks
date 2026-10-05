import type { KnowledgeAccessProposal } from '../../services/knowledgebaseApi'
import { useState } from 'react'
import { SecretField } from '../../components/ui/SecretField'

const labels: Record<string, string> = {
  configure_backup: 'Configure Git backup',
  grant: 'Grant folder access', revoke: 'Remove folder grant',
  create_service_account: 'Create service account', disable_service_account: 'Disable service account',
  bind_project: 'Connect project to shared folder', unbind_project: 'Disconnect project from shared folder',
}
const fields: Array<[string, string]> = [
  ['remote_url', 'Repository'], ['username', 'Username'], ['branch', 'Branch'],
  ['folder_path', 'Folder'], ['folder_id', 'Folder ID'], ['identity_id', 'Identity ID'],
  ['role', 'Role'], ['name', 'Name'], ['workspace_path', 'Project'], ['alias', 'Alias'],
  ['access', 'Access'], ['replace_legacy_alias', 'Replace existing legacy alias'],
]
export function KnowledgebaseAccessConfirmation({ proposals, error, busy, onConfirm }: {
  proposals: KnowledgeAccessProposal[]; error: string; busy: boolean;
  onConfirm: (id: string, approve: boolean, pat?: string) => Promise<void>;
}) {
  const [pats, setPATs] = useState<Record<string, string>>({})
  async function confirm(id: string, approve: boolean, backup: boolean) {
    if (approve && backup && pats[id]) await onConfirm(id, approve, pats[id])
    else await onConfirm(id, approve)
    setPATs(current => { const next = { ...current }; delete next[id]; return next })
  }
  if (!proposals.length) return null
  return <section className="max-h-64 shrink-0 overflow-auto border-b border-border p-3" aria-label="Pending access changes">
    {proposals.map(proposal => <div key={proposal.id} className="mb-2 rounded border border-border p-3">
      <p className="text-sm font-medium">{labels[String(proposal.arguments.action)] || 'Confirm access change'}</p>
      <dl className="my-2 text-sm">{fields.filter(([key]) => key in proposal.arguments).map(([key, label]) =>
        <div key={key} className="flex gap-2"><dt className="text-muted-foreground">{label}:</dt><dd className="break-all whitespace-pre-wrap">{String(proposal.arguments[key] || (key === 'folder_path' ? 'Organization root' : 'false'))}</dd></div>)}</dl>
      <p className="mb-2 text-xs text-muted-foreground">Review the exact scope and identity. Permissions are checked again when you approve.</p>
      {proposal.arguments.action === 'configure_backup' && String(proposal.arguments.remote_url).startsWith('https://') && <div className="mb-3"><SecretField label="PAT (optional)" value={pats[proposal.id] || ''} onChange={value => setPATs(current => ({ ...current, [proposal.id]: value }))} disabled={busy} placeholder="For private repositories" hint={proposal.arguments.pat_configured ? 'A PAT was provided. Leave blank to keep it, or enter a replacement. Stored encrypted in Knowledge Base.' : 'Enter here for a private repository. Stored encrypted in Knowledge Base; never sent to chat.'} /></div>}
      <button type="button" disabled={busy} onClick={() => void confirm(proposal.id, true, proposal.arguments.action === 'configure_backup')} className="mr-3 rounded border px-3 py-1">Approve</button>
      <button type="button" disabled={busy} onClick={() => void confirm(proposal.id, false, false)} className="rounded border px-3 py-1">Cancel</button>
    </div>)}
    {error && <p role="alert">{error}</p>}
  </section>
}
