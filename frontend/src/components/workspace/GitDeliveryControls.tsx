import { useCallback, useEffect, useId, useRef, useState } from 'react'
import { GitPullRequest, Loader2, X } from 'lucide-react'
import api from '../../services/api'
import { gitActionError } from '../../services/workspaceGit'
import { useAuthStore } from '../../stores/useAuthStore'
import { useChatStore } from '../../stores/useChatStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { PRWatcherControls } from '../chat/PRWatcherControls'
import ModalPortal from '../ui/ModalPortal'

type Capability = { available: boolean; can_push: boolean; can_draft: boolean; note?: string; draft_note?: string }
type DraftPR = { title: string; body: string; base: string; head: string; branch: string; repository: string }
type Receipt = { pr: { number: number; url: string; base: string }; repository: string; branch: string }

/** Each operation captures its caller, network, project and chat. A late draft
 * can neither fill another chat's composer nor create a PR after a UI switch. */
export function useGitDelivery(workspacePath: string, repo: string, branch: string, enabled: boolean, setMessage: (text: string) => void) {
  const actor = useAuthStore(state => state.user?.id)
  const network = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const session = useChatStore(state => state.activeTabId ? state.chatTabs[state.activeTabId]?.sessionId : undefined)
  const scope = JSON.stringify([network, actor, workspacePath, repo, branch, session])
  const current = useRef(scope); current.current = scope
  const [capability, setCapability] = useState<Capability | null>(null)
  const [creating, setCreating] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [draft, setDraft] = useState<DraftPR | null>(null)
  const [receipt, setReceipt] = useState<Receipt | null>(null)
  const pending = useRef<AbortController | null>(null)
  const dialog = useRef<HTMLDialogElement>(null)
  const titleId = useId()
  useEffect(() => {
    setCapability(null); setDraft(null); setReceipt(null); setError(''); setBusy(false); setCreating(false)
    if (!enabled) return
    const abort = new AbortController(), identity = captureChatIdentity()
    void api.post<Capability>('/api/workspace-git', { workspace_path: workspacePath, repo, op: 'delivery_capabilities', session_id: session }, { signal: abort.signal, skipSessionContext: true })
      .then(({ data }) => { if (!abort.signal.aborted && isChatIdentityCurrent(identity)) setCapability(data.available ? data : null) })
      .catch(() => { if (!abort.signal.aborted) setCapability(null) })
    return () => { abort.abort(); pending.current?.abort() }
  }, [scope, enabled, workspacePath, repo, session])
  useEffect(() => { if (draft && dialog.current && !dialog.current.open) dialog.current.showModal() }, [Boolean(draft)])
  const action = useCallback(async (work: (post: <T>(body: Record<string, unknown>) => Promise<T>, valid: () => boolean) => Promise<void>) => {
    const captured = scope, identity = captureChatIdentity()
    pending.current?.abort()
    const abort = new AbortController(); pending.current = abort
    const valid = () => !abort.signal.aborted && current.current === captured && isChatIdentityCurrent(identity)
    const post = async <T,>(body: Record<string, unknown>): Promise<T> => (await api.post<T>('/api/workspace-git', { workspace_path: workspacePath, repo, session_id: session, ...body }, { signal: abort.signal, skipSessionContext: true })).data
    setBusy(true); setError('')
    try { await work(post, valid) } catch (cause) { if (valid()) setError(gitActionError(cause)) }
    finally { if (valid() && pending.current === abort) setBusy(false) }
  }, [scope, workspacePath, repo, session])
  const draftCommit = (all: boolean) => action(async (post, valid) => {
    const data = await post<{ message: string }>({ op: 'draft_commit', all })
    if (valid()) setMessage(data.message)
  })
  const preparePR = () => action(async (post, valid) => {
    const prepared = await post<DraftPR>({ op: 'prepare_pr' })
    if (!valid()) return
    setDraft(prepared)
    if (capability?.can_draft) {
      const data = await post<DraftPR>({ op: 'draft_pr', base: prepared.base })
      if (valid()) setDraft(data)
    }
  })
  const openPR = () => action(async (post, valid) => {
    if (!draft) return
    setCreating(true)
    try {
      const data = await post<Receipt>({ op: 'open_pr', title: draft.title, body: draft.body, base: draft.base, expected_head: draft.head })
      if (valid()) { setReceipt(data); setDraft(null) }
    } finally { if (valid()) setCreating(false) }
  })
  const close = () => { pending.current?.abort(); setDraft(null); setBusy(false); setError('') }
  const prControls = capability && <>
    {capability.can_push ? <button type="button" disabled={busy} className="mt-2 flex w-full items-center justify-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-xs hover:bg-muted disabled:opacity-50" onClick={() => void preparePR()}><GitPullRequest className="h-3.5 w-3.5" />Open pull request…</button>
      : capability.note && <p className="mt-1 text-xs text-muted-foreground">{capability.note}</p>}
    {error && !draft && <p role="alert" className="mt-1 text-xs text-destructive">{error}</p>}
    {receipt && <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
      <a href={receipt.pr.url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 underline hover:text-foreground"><GitPullRequest className="h-3.5 w-3.5" />PR #{receipt.pr.number} · {receipt.repository}</a>
      {session && <PRWatcherControls compact group={{ root: repo, github_repo: receipt.repository, pr: { number: receipt.pr.number, url: receipt.pr.url, state: 'open' } }} sessionId={session} />}
    </div>}
  </>
  const chooser = draft && <ModalPortal><dialog ref={dialog} aria-labelledby={titleId} onCancel={event => { if (creating) event.preventDefault(); else close() }} className="fixed inset-0 m-auto w-[calc(100%-2rem)] max-w-lg rounded-lg border border-border bg-popover p-4 text-popover-foreground shadow-xl backdrop:bg-black/50">
    <div className="flex items-center gap-3"><h2 id={titleId} className="flex-1 text-sm font-semibold">Open pull request</h2><button type="button" aria-label="Close pull request" disabled={creating} onClick={close} className="rounded p-1 text-muted-foreground hover:bg-muted"><X className="h-4 w-4" /></button></div>
    <p className="mb-3 mt-1 break-words text-xs text-muted-foreground">{draft.repository} · {draft.branch} → {draft.base}</p>
    <label className="block text-xs text-muted-foreground">Title<input disabled={busy} value={draft.title} maxLength={256} onChange={event => setDraft({ ...draft, title: event.target.value })} className="mb-3 mt-1 block w-full rounded border border-input bg-muted/40 p-2 text-sm text-foreground" /></label>
    <label className="block text-xs text-muted-foreground">Description<textarea disabled={busy} value={draft.body} maxLength={20000} rows={7} onChange={event => setDraft({ ...draft, body: event.target.value })} className="mt-1 block w-full rounded border border-input bg-muted/40 p-2 text-sm text-foreground" /></label>
    <p className="mt-2 text-xs text-muted-foreground">{creating ? 'Opening the pull request…' : busy ? 'Preparing the text…' : 'Check the title and description before opening. This does not commit or push.'}</p>
    {error && <p role="alert" className="mt-2 text-xs text-destructive">{error}</p>}
    <div className="mt-4 flex justify-end gap-2 text-xs"><button type="button" onClick={close} disabled={creating} className="rounded border border-border px-3 py-1.5">Cancel</button><button type="button" disabled={busy || !draft.title.trim()} onClick={() => void openPR()} className="inline-flex items-center gap-1.5 rounded bg-primary px-3 py-1.5 font-medium text-primary-foreground disabled:opacity-50">{busy && <Loader2 className="h-3.5 w-3.5 animate-spin" />}Open pull request</button></div>
  </dialog></ModalPortal>
  return { capability, busy, draftCommit, prControls, chooser }
}
