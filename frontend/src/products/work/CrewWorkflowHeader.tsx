import { useState, type ReactNode } from 'react'
import { Pencil, Trash2 } from 'lucide-react'
import { PageCard, RowButton } from '../../components/automation/AutomationKit'

type Mode = 'view' | 'rename' | 'delete'

/** An icon-only button: the label is its tooltip and its accessible name. */
function IconButton({ label, onClick, children }: { label: string; onClick: () => void; children: ReactNode }) {
  return <button type="button" title={label} aria-label={label} onClick={onClick}
    className="grid h-8 w-8 place-items-center rounded-full text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">{children}</button>
}

/**
 * The top of a Crew's Workflow page: the workflow's name with Rename and Delete. A Crew has one workflow.
 * The page does the saving and throws an Error with a plain message when the server refuses, which is shown here.
 */
export function CrewWorkflowHeader({ name, onRename, onDelete }: {
  name: string
  onRename: (name: string) => Promise<void>
  onDelete: () => Promise<void>
}) {
  const [mode, setMode] = useState<Mode>('view')
  const [draft, setDraft] = useState(name)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const open = (next: Mode) => { setError(''); setDraft(name); setMode(next) }
  async function run(action: () => Promise<void>, fallback: string) {
    setBusy(true); setError('')
    try {
      await action()
      setMode('view')
    } catch (e) {
      setError(e instanceof Error && e.message ? e.message : fallback)
    } finally { setBusy(false) }
  }
  const trimmed = draft.trim()
  const save = () => {
    if (trimmed && trimmed !== name) void run(() => onRename(trimmed), 'Could not rename the workflow. Try again.')
  }

  return <PageCard title={name} testId="crew-workflow-header"
    action={mode === 'view' ? <span className="flex shrink-0 gap-1">
      <IconButton label="Rename this workflow" onClick={() => open('rename')}><Pencil className="h-4 w-4" aria-hidden="true" /></IconButton>
      <IconButton label="Delete this workflow" onClick={() => open('delete')}><Trash2 className="h-4 w-4" aria-hidden="true" /></IconButton>
    </span> : undefined}>
    {mode === 'rename' && <form onSubmit={event => { event.preventDefault(); save() }} className="flex flex-wrap items-center gap-2 px-4 py-3">
      <input aria-label="Workflow name" value={draft} maxLength={120} autoFocus disabled={busy} onChange={event => setDraft(event.target.value)}
        className="min-w-0 flex-1 rounded-md border border-border bg-background px-2 py-1 text-sm" />
      <RowButton primary disabled={busy || !trimmed || trimmed === name} onClick={save}>Save</RowButton>
      <RowButton disabled={busy} onClick={() => open('view')}>Cancel</RowButton>
    </form>}
    {mode === 'delete' && <div role="alertdialog" aria-label="Delete this workflow" className="space-y-2 px-4 py-3">
      <p className="text-sm">Delete “{name}”? Its code, runs and saved settings are removed. This cannot be undone.</p>
      <div className="flex gap-2">
        <button type="button" disabled={busy} onClick={() => void run(onDelete, 'Could not delete the workflow. Try again.')}
          className="rounded-full bg-destructive px-3 py-1 text-xs font-medium text-destructive-foreground hover:bg-destructive/90 disabled:opacity-50">Delete workflow</button>
        <RowButton disabled={busy} onClick={() => open('view')}>Cancel</RowButton>
      </div>
    </div>}
    {error && <p role="alert" className="px-4 py-2 text-xs text-destructive">{error}</p>}
  </PageCard>
}
