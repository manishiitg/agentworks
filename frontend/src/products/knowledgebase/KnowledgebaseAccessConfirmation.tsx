import type { KnowledgeAccessProposal } from '../../services/knowledgebaseApi'

const labels: Record<string, string> = {
  configure_backup: 'Configure Git backup',
  grant: 'Grant folder access', revoke: 'Remove folder grant',
  create_service_account: 'Create service account', disable_service_account: 'Disable service account',
  bind_project: 'Connect project to shared folder', unbind_project: 'Disconnect project from shared folder',
}
const fields: Array<[string, string]> = [
  ['remote_url', 'Repository'], ['branch', 'Branch'],
  ['folder_path', 'Folder'], ['folder_id', 'Folder ID'], ['identity_id', 'Identity ID'],
  ['role', 'Role'], ['name', 'Name'], ['workspace_path', 'Project'], ['alias', 'Alias'],
  ['access', 'Access'], ['replace_legacy_alias', 'Replace existing legacy alias'],
]
export function KnowledgebaseAccessConfirmation({ proposals, error, busy, onConfirm }: {
  proposals: KnowledgeAccessProposal[]; error: string; busy: boolean;
  onConfirm: (id: string, approve: boolean) => Promise<void>;
}) {
  if (!proposals.length) return null
  return <section className="max-h-64 shrink-0 overflow-auto border-b border-border p-3" aria-label="Pending access changes">
    {proposals.map(proposal => <div key={proposal.id} className="mb-2 rounded border border-border p-3">
      <p className="text-sm font-medium">{labels[String(proposal.arguments.action)] || 'Confirm access change'}</p>
      <dl className="my-2 text-sm">{fields.filter(([key]) => key in proposal.arguments).map(([key, label]) =>
        <div key={key} className="flex gap-2"><dt className="text-muted-foreground">{label}:</dt><dd className="break-all whitespace-pre-wrap">{String(proposal.arguments[key] || (key === 'folder_path' ? 'Organization root' : 'false'))}</dd></div>)}</dl>
      <p className="mb-2 text-xs text-muted-foreground">Review the exact scope and identity. Permissions are checked again when you approve.</p>
      <button type="button" disabled={busy} onClick={() => void onConfirm(proposal.id, true)} className="mr-3 rounded border px-3 py-1">Approve</button>
      <button type="button" disabled={busy} onClick={() => void onConfirm(proposal.id, false)} className="rounded border px-3 py-1">Cancel</button>
    </div>)}
    {error && <p role="alert">{error}</p>}
  </section>
}
