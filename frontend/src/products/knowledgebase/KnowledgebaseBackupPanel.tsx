import { useState, type FormEvent } from 'react'
import { Archive, CheckCircle2, CircleAlert } from 'lucide-react'
import { knowledgebaseApi, knowledgebaseError, type KnowledgeBootstrap } from '../../services/knowledgebaseApi'
import { Button } from '../../components/ui/Button'
import { WorkspacePageContent } from '../../components/workspace/WorkspacePageContent'

export function KnowledgebaseBackupPanel({ bootstrap, onPrepared, onSecrets }: { bootstrap: KnowledgeBootstrap; onPrepared: () => void; onSecrets: () => void }) {
  const [editing, setEditing] = useState(false)
  const [remote, setRemote] = useState('')
  const [username, setUsername] = useState('')
  const [branch, setBranch] = useState('main')
  const [secret, setSecret] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [prepared, setPrepared] = useState(false)
  const canConfigure = bootstrap.is_admin && bootstrap.can_write !== false
  const configured = bootstrap.backup_configured
  const StatusIcon = configured ? CheckCircle2 : CircleAlert
  async function prepare(event: FormEvent) {
    event.preventDefault(); if (!canConfigure || busy) return
    setBusy(true); setError(''); setPrepared(false)
    try {
      await knowledgebaseApi.prepareAccess({ action: 'configure_backup', remote_url: remote.trim(), username: username.trim(), branch: branch.trim(), request_id: crypto.randomUUID(), ...(secret.trim() ? { pat_secret: secret.trim() } : {}) })
      setPrepared(true); setEditing(false); onPrepared()
    } catch (cause) { setError(knowledgebaseError(cause)) }
    finally { setBusy(false) }
  }
  return <WorkspacePageContent as="section" width="form" aria-label="Brain backup" className="space-y-5">
    <div className="flex items-start gap-3"><Archive className="mt-1 h-5 w-5 shrink-0 text-muted-foreground" aria-hidden="true" /><div><h2 className="text-lg font-semibold">Protect your knowledge</h2><p className="mt-1 text-sm text-muted-foreground">Keep a recoverable copy of your shared knowledge.</p></div></div>
    <div className="space-y-4 rounded-xl border border-border p-4">
      <p role="status" className={`flex items-center gap-2 text-sm font-medium ${configured ? 'text-emerald-600 dark:text-emerald-400' : 'text-amber-600 dark:text-amber-400'}`}><StatusIcon className="h-4 w-4" aria-hidden="true" />{configured ? 'Backup configured' : 'Backup not set up'}</p>
      <p className="text-sm text-muted-foreground">{configured ? 'A Git repository is configured for Brain.' : 'An administrator can connect a Git repository to store the backup.'}</p>
      {canConfigure ? <Button variant="outline" size="sm" onClick={() => setEditing(value => !value)}>{editing ? 'Cancel' : configured ? 'Change backup settings' : 'Set up backup'}</Button> : <p className="text-xs text-muted-foreground">Ask an administrator to manage backup.</p>}
      {canConfigure && editing && <form onSubmit={event => void prepare(event)} className="space-y-3 border-t border-border pt-4">
        <label className="block space-y-1 text-xs">Repository URL<input required type="url" value={remote} onChange={event => setRemote(event.target.value)} placeholder="https://github.com/your-team/brain-backup.git" className="w-full rounded-md border border-border bg-background p-2 text-sm" /></label>
        <label className="block space-y-1 text-xs">Git username<input required value={username} onChange={event => setUsername(event.target.value)} autoComplete="off" className="w-full rounded-md border border-border bg-background p-2 text-sm" /></label>
        <details className="text-xs text-muted-foreground"><summary className="w-fit cursor-pointer">Advanced: backup branch</summary><label className="mt-2 block space-y-1">Branch<input required value={branch} onChange={event => setBranch(event.target.value)} className="w-full rounded-md border border-border bg-background p-2 text-sm" /></label></details>
        <p className="text-xs text-muted-foreground">For a private repository, enter its token in the secure approval field after reviewing this change. Leave it blank to keep an existing credential.</p>
        <details className="text-xs"><summary className="w-fit cursor-pointer text-muted-foreground">Use a saved Brain secret instead</summary><label className="mt-2 block space-y-1">Secret name<input value={secret} onChange={event => setSecret(event.target.value)} placeholder="BRAIN_GITHUB_PAT" className="w-full rounded-md border border-border bg-background p-2 text-sm" /></label></details>
        <Button type="submit" disabled={busy}>{busy ? 'Preparing…' : 'Review backup change'}</Button>
      </form>}
      {prepared && <p role="status" className="text-sm text-muted-foreground">Review and approve the pending backup change above. Nothing has been applied yet.</p>}
      {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    </div>
    {canConfigure && <details className="text-xs text-muted-foreground"><summary className="w-fit cursor-pointer">Backup credentials</summary><p className="my-2">Existing Brain secrets remain available for backups that refer to a saved secret by name.</p><button type="button" onClick={onSecrets} className="text-primary underline">Manage Brain secrets</button></details>}
  </WorkspacePageContent>
}
