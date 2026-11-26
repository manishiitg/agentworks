import { useState, type ReactNode } from 'react'
import { Pencil, Trash2 } from 'lucide-react'
import { PageCard, RowButton } from '../../components/automation/AutomationKit'

type Mode = 'view' | 'delete'

/** An icon-only button: the label is its tooltip and its accessible name. */
function IconButton({ label, onClick, children }: { label: string; onClick: () => void; children: ReactNode }) {
  return <button type="button" title={label} aria-label={label} onClick={onClick}
    className="grid h-8 w-8 place-items-center rounded-full text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">{children}</button>
}

/**
 * The top of a Crew's Workflow page: the workflow's name and description, with Edit (a chat with the Crew's agent) and Delete. A Crew can have several workflows.
 * The page does the deleting and throws an Error with a plain message when the server refuses, which is shown here.
 */
export function CrewWorkflowHeader({ name, description, onEdit, onDelete }: {
  name: string
  /** What the workflow is for, shown in full under the name. */
  description?: string
  /** Opens the Crew chat with a message about this workflow; the agent asks what to change. */
  onEdit: () => void
  onDelete: () => Promise<void>
}) {
  const [mode, setMode] = useState<Mode>('view')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const open = (next: Mode) => { setError(''); setMode(next) }
  async function run(action: () => Promise<void>, fallback: string) {
    setBusy(true); setError('')
    try {
      await action()
      setMode('view')
    } catch (e) {
      setError(e instanceof Error && e.message ? e.message : fallback)
    } finally { setBusy(false) }
  }
  return <PageCard title={name} testId="crew-workflow-header"
    action={mode === 'view' ? <span className="flex shrink-0 gap-1">
      <IconButton label="Edit this workflow" onClick={onEdit}><Pencil className="h-4 w-4" aria-hidden="true" /></IconButton>
      <IconButton label="Delete this workflow" onClick={() => open('delete')}><Trash2 className="h-4 w-4" aria-hidden="true" /></IconButton>
    </span> : undefined}>
    <p className="whitespace-pre-wrap px-4 py-3 text-sm text-muted-foreground" data-testid="crew-workflow-description">{description?.trim() || 'No description yet.'}</p>
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
