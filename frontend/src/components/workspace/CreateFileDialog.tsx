import { useState } from 'react'
import { FilePlus, X } from 'lucide-react'
import { newFileNameProblem } from '../../utils/editRawFile'

/** Asks for a new file's name. `onCreate` throws with the message to show (for example "already exists"). */
export default function CreateFileDialog({ isOpen, onClose, onCreate, folderLabel }: {
  isOpen: boolean
  onClose: () => void
  onCreate: (name: string) => Promise<void>
  folderLabel: string
}) {
  const [name, setName] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  if (!isOpen) return null

  const close = () => { if (!busy) { setName(''); setError(''); onClose() } }
  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    const problem = newFileNameProblem(name)
    if (problem) { setError(problem); return }
    setBusy(true)
    setError('')
    try {
      await onCreate(name)
      setName('')
      onClose()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not create the file')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" role="dialog" aria-modal="true" aria-label="New file" onKeyDown={event => { if (event.key === 'Escape') close() }}>
      <form onSubmit={event => { void submit(event) }} className="w-full max-w-sm rounded-lg border border-border bg-background p-4 shadow-lg">
        <div className="mb-3 flex items-center justify-between">
          <h2 className="flex items-center gap-2 text-sm font-semibold text-foreground"><FilePlus className="h-4 w-4 text-primary" />New file</h2>
          <button type="button" onClick={close} aria-label="Close" className="rounded p-1 text-muted-foreground hover:bg-muted"><X className="h-4 w-4" /></button>
        </div>
        <p className="mb-2 truncate text-xs text-muted-foreground">In {folderLabel}</p>
        <input
          autoFocus
          value={name}
          onChange={event => setName(event.target.value)}
          placeholder="File name, for example notes.md"
          disabled={busy}
          aria-label="File name"
          className="w-full rounded-md border border-input bg-transparent px-3 py-2 text-sm text-foreground placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:opacity-50"
        />
        {error && <p role="alert" className="mt-2 text-xs text-destructive">{error}</p>}
        <div className="mt-4 flex justify-end gap-2">
          <button type="button" onClick={close} disabled={busy} className="rounded-md px-3 py-1.5 text-sm text-muted-foreground hover:bg-muted">Cancel</button>
          <button type="submit" disabled={busy || !name} className="rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground disabled:opacity-50">{busy ? 'Creating…' : 'Create'}</button>
        </div>
      </form>
    </div>
  )
}
