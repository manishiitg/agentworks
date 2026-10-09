import { useState, type FormEvent } from 'react'
import { RunsOnPicker, type RunsOnSelection } from './RunsOnPicker'
import { AlertCircle, Briefcase, Code2, Laptop, Loader2, X } from 'lucide-react'
import type { ProductMode } from '../../platform/chat/productProjects'

// What each mode is for, in the words a person choosing it needs. Local cannot be changed to or from later.
export const CODE_MODE_CHOICES: Array<{ mode: ProductMode; title: string; text: string; icon: typeof Code2 }> = [
  { mode: 'dev', title: 'Dev', text: 'Build and change code with an AI agent: files, terminal and chat on the server.', icon: Code2 },
  { mode: 'cowork', title: 'Cowork', text: 'A private assistant for your work: dashboards, the browser and automations. No code needed.', icon: Briefcase },
  { mode: 'local', title: 'Local', text: 'Work on a project folder on your own computer, through the AgentWorks CLI.', icon: Laptop },
]

// A Code workspace has a name only: no identity, purpose, icon or template.
export function CreateCodeWorkspaceDialog({ onClose, onCreate, submitting, error, profileId = 'code', initialMode = 'dev' }: {
  onClose: () => void
  onCreate: (title: string, runsOn?: RunsOnSelection, mode?: ProductMode) => void | Promise<void>
  profileId?: string
  initialMode?: ProductMode
  submitting: boolean
  error: string | null
}) {
  const [title, setTitle] = useState('')
  const [runsOn, setRunsOn] = useState<RunsOnSelection | undefined>()
  const [mode, setMode] = useState<ProductMode>(initialMode)
  const submit = (event: FormEvent) => {
    event.preventDefault()
    const trimmed = title.trim()
    if (!trimmed || submitting) return
    void onCreate(trimmed, runsOn, mode)
  }
  return (
    <div className="fixed inset-0 z-50 grid place-items-center bg-black/65 p-3 backdrop-blur-sm" role="presentation">
      <form onSubmit={submit} role="dialog" aria-modal="true" aria-labelledby="code-create-title" className="w-full max-w-md rounded-2xl border border-border bg-background p-5 shadow-2xl">
        <div className="flex items-start justify-between gap-3">
          <div>
            <h2 id="code-create-title" className="text-lg font-semibold text-foreground">New Code workspace</h2>
            <p className="mt-1 text-sm text-muted-foreground">A private workspace. Only you can open it. Server admins and Code reviewers can read its chats, files and costs (read-only, and every view is logged).</p>
          </div>
          <button type="button" onClick={onClose} disabled={submitting} aria-label="Close" className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50">
            <X className="h-4 w-4" />
          </button>
        </div>
        <div role="radiogroup" aria-label="Mode" className="mt-4 grid gap-2">
          {CODE_MODE_CHOICES.map(choice => <label key={choice.mode} className={`flex cursor-pointer items-start gap-3 rounded-lg border p-3 ${mode === choice.mode ? 'border-primary bg-primary/5' : 'border-border hover:bg-muted/40'}`}>
            <input type="radio" name="code-mode" className="mt-1" checked={mode === choice.mode} onChange={() => setMode(choice.mode)} disabled={submitting} />
            <choice.icon aria-hidden="true" className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
            <span><span className="block text-sm font-medium text-foreground">{choice.title}</span><span className="block text-xs leading-5 text-muted-foreground">{choice.text}</span></span>
          </label>)}
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
        {/* A Code workspace is private, so the account it was created on is saved when no server account is ready: a Claude or Codex account someone shared with you is not picked up by default, and without it the workspace's first message fails. */}
        <RunsOnPicker profileId={profileId} onChange={setRunsOn} disabled={submitting} saveAccount="when-needed" />
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
