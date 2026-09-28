import { useState, type FormEvent } from 'react'
import { AlertCircle, Loader2, X } from 'lucide-react'

// A Code workspace has a name only: no identity, purpose, icon or template.
export function CreateCodeWorkspaceDialog({ onClose, onCreate, submitting, error }: {
  onClose: () => void
  onCreate: (title: string) => void | Promise<void>
  submitting: boolean
  error: string | null
}) {
  const [title, setTitle] = useState('')
  const submit = (event: FormEvent) => {
    event.preventDefault()
    const trimmed = title.trim()
    if (!trimmed || submitting) return
    void onCreate(trimmed)
  }
  return (
    <div className="fixed inset-0 z-50 grid place-items-center bg-black/65 p-3 backdrop-blur-sm" role="presentation">
      <form onSubmit={submit} role="dialog" aria-modal="true" aria-labelledby="code-create-title" className="w-full max-w-md rounded-2xl border border-border bg-background p-5 shadow-2xl">
        <div className="flex items-start justify-between gap-3">
          <div>
            <h2 id="code-create-title" className="text-lg font-semibold text-foreground">New Code workspace</h2>
            <p className="mt-1 text-sm text-muted-foreground">A private workspace with its own files, terminal and chat. Only you can see it until you share it; server admins can view it.</p>
          </div>
          <button type="button" onClick={onClose} disabled={submitting} aria-label="Close" className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50">
            <X className="h-4 w-4" />
          </button>
        </div>
        <label className="mt-4 block text-sm font-medium text-foreground" htmlFor="code-create-name">Name</label>
        <input
          id="code-create-name"
          autoFocus
          value={title}
          maxLength={60}
          onChange={event => setTitle(event.target.value)}
          disabled={submitting}
          placeholder="e.g. billing-service"
          className="mt-1.5 w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
        />
        {error ? <p className="mt-3 flex items-center gap-1.5 text-sm text-destructive"><AlertCircle className="h-4 w-4" />{error}</p> : null}
        <div className="mt-5 flex justify-end gap-2">
          <button type="button" onClick={onClose} disabled={submitting} className="rounded-md px-3 py-1.5 text-sm text-muted-foreground hover:bg-muted disabled:opacity-50">Cancel</button>
          <button type="submit" disabled={!title.trim() || submitting} className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50">
            {submitting ? <><Loader2 className="h-3.5 w-3.5 animate-spin" />Creating…</> : 'Create'}
          </button>
        </div>
      </form>
    </div>
  )
}
