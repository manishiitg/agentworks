import { lazy, Suspense, useEffect, useRef, useState } from 'react'
import api from '../../services/api'
import { markFilesReviewed, useReviewRevision, type ReviewResult } from '../workspace/chatFileReview'
import type { ReplyFileChangesRef } from '../../utils/replyFileChanges'
import { useProjectMode } from '../../products/work/codeLocalFiles'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { isViewableBinaryFile, openWorkspaceFile } from '../../utils/openWorkspaceFile'
import { FileDiffView } from '../workspace/FileDiffView'

const FileViewer = lazy(() => import('../FileContentViewer').then(module => ({ default: module.FileContentViewerBody })))
interface Change { path: string; status: string; added?: number; removed?: number; binary?: boolean }
const endpoint = '/api/workspace-snapshots'
const paramsFor = (reference: ReplyFileChangesRef) => ({ workspace_path: reference.workspacePath, snapshot_id: reference.snapshotId })

export function ReplyFileChanges({ reference }: { reference: ReplyFileChangesRef }) {
  const mode = useProjectMode(reference.sessionId)
  const account = useAuthStore(state => state.user?.id)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const scope = JSON.stringify([account, server, reference.workspacePath, reference.snapshotId])
  if (mode !== 'dev') return null
  return <ScopedFileChanges key={scope} reference={reference} />
}

function ScopedFileChanges({ reference }: { reference: ReplyFileChangesRef }) {
  const [result, setResult] = useState<ReviewResult | null>(null)
  const comparison = result?.comparison
  const [revision] = useReviewRevision()
  const [reviewing, setReviewing] = useState(false)
  const [reviewError, setReviewError] = useState('')
  const latest = result?.history.latest_snapshot_id === reference.snapshotId && (!reference.latestSnapshotId || reference.latestSnapshotId === reference.snapshotId)
  const reviewed = result?.history.baseline_snapshot_id === reference.snapshotId
  const [selected, setSelected] = useState<{ change: Change; view: 'preview' | 'diff' } | null>(null)
  useEffect(() => {
    const controller = new AbortController()
    void api.get<ReviewResult>(endpoint, { params: { ...paramsFor(reference), session_id: reference.sessionId, op: 'snapshot_compare' }, signal: controller.signal })
      .then(response => { if (!controller.signal.aborted) setResult(response.data) })
      .catch(() => { /* Expired/unavailable history has no change card. */ })
    return () => controller.abort()
  }, [reference.workspacePath, reference.snapshotId, reference.sessionId, reference.latestSnapshotId, revision])
  if (!comparison?.changes?.length && !(latest && result?.history.can_review)) {
    return (comparison?.uncaptured_changes ?? 0) > 0
      ? <p className="my-3 text-xs text-muted-foreground" role="status">{comparison?.uncaptured_changes} {comparison?.uncaptured_changes === 1 ? 'file may' : 'files may'} have changed but could not be compared.</p>
      : null
  }
  if (!comparison) return null
  return (
    <section className="my-3 rounded-lg border border-border px-3 py-2 text-sm" aria-label="Files changed during this reply">
      <details>
        <summary className="cursor-pointer font-medium">Files changed during this reply · {result?.total_changes ?? comparison.changes.length}</summary>
        <p className="mt-2 text-xs text-muted-foreground">Saved versions. Other chats or editors may have contributed.</p>
        {comparison.uncaptured_changes > 0 && <p className="text-xs text-muted-foreground">{comparison.uncaptured_changes} {comparison.uncaptured_changes === 1 ? 'file may' : 'files may'} have changed but could not be compared.</p>}
        <ul className="mt-2 space-y-1">
          {comparison.changes.map(change => (
            <li key={change.path} className="flex flex-wrap items-center gap-2">
              <button type="button" className="min-w-0 break-all text-left underline underline-offset-2" onClick={() => setSelected({ change, view: 'preview' })}>{change.path}</button>
              <span className="text-xs text-muted-foreground">{change.status === 'A' ? 'Added' : change.status === 'D' ? 'Deleted' : 'Changed'}</span>
              {!change.binary && typeof change.added === 'number' && typeof change.removed === 'number'
                && <span className="text-xs" aria-label={`${change.added} lines added, ${change.removed} lines removed`}><span className="text-green-600">+{change.added}</span> <span className="text-red-500">−{change.removed}</span></span>}
              {!change.binary && <button type="button" className="ml-auto text-xs underline" onClick={() => setSelected({ change, view: 'diff' })}>Compare</button>}
            </li>
          ))}
        </ul>
        {!!result?.next_offset && <button type="button" className="mt-2 text-xs underline" onClick={() => {
          const shown = result
          void api.get<ReviewResult>(endpoint, { params: { ...paramsFor(reference), session_id: reference.sessionId, op: 'snapshot_compare', offset: shown.next_offset } })
            .then(({ data }) => setResult(current => current === shown && data.comparison ? { ...data, comparison: { ...data.comparison, changes: [...shown.comparison!.changes, ...data.comparison.changes] } } : current))
            .catch(() => setReviewError('Could not load more files. Try again.'))
        }}>Show more files</button>}
      {latest && <div className="mt-2 flex flex-wrap items-center gap-2 border-t pt-2 text-xs">
        {result?.since_review && <span>Since your last review: {result.since_review.files} files <span className="text-green-600">+{result.since_review.added}</span> <span className="text-red-500">−{result.since_review.removed}</span></span>}
        {reviewed ? <span>Reviewed up to here</span> : result?.history.can_review && <button type="button" disabled={reviewing} className="rounded border px-2 py-1" onClick={() => { setReviewing(true); setReviewError(''); void markFilesReviewed(reference.workspacePath,reference.sessionId,reference.snapshotId).catch(() => setReviewError('Could not save the review point. Try again.')).finally(() => setReviewing(false)) }}>Mark as reviewed</button>}
        {reviewError && <p role="alert">{reviewError}</p>}
      </div>}
      </details>
      {selected && <SavedFile key={`${selected.change.path}:${selected.view}`} reference={reference} {...selected} close={() => setSelected(null)} />}
    </section>
  )
}

export function SavedFile({ reference, change, view, close, query }: { reference: ReplyFileChangesRef; change: Change; view: 'preview' | 'diff'; close: () => void; query?: Record<string, string> }) {
  const dialog = useRef<HTMLDialogElement>(null)
  const [side, setSide] = useState<'before' | 'after'>(change.status === 'D' ? 'before' : 'after')
  const [content, setContent] = useState<{ text: string; bytes?: ArrayBuffer } | null>(null)
  const [error, setError] = useState('')
  const name = change.path.split('/').pop() || change.path
  const imageType = ({ png: 'image/png', jpg: 'image/jpeg', jpeg: 'image/jpeg', gif: 'image/gif', webp: 'image/webp', svg: 'image/svg+xml' } as Record<string, string>)[name.split('.').pop()?.toLowerCase() || '']
  useEffect(() => {
    const previouslyFocused = document.activeElement as HTMLElement | null
    dialog.current?.showModal()
    return () => { previouslyFocused?.focus({ preventScroll: true }) }
  }, [])
  useEffect(() => {
    if (view === 'diff') return
    const controller = new AbortController()
    setContent(null); setError('')
    const params = { ...paramsFor(reference), ...query, file: change.path, op: 'snapshot_file', side }
    const load = async () => {
      const { data } = await api.get<ArrayBuffer>(endpoint, { params, responseType: 'arraybuffer', signal: controller.signal })
      let text = ''
      if (imageType) {
        const bytes = new Uint8Array(data)
        let binary = ''
        for (let offset = 0; offset < bytes.length; offset += 8192) binary += String.fromCharCode(...bytes.subarray(offset, offset + 8192))
        text = `data:${imageType};base64,${btoa(binary)}`
      } else if (!change.binary && !isViewableBinaryFile(name)) text = new TextDecoder().decode(data)
      if (!controller.signal.aborted) setContent({ text, bytes: data })
    }
    void load().catch(() => { if (!controller.signal.aborted) setError('This saved version is unavailable or you no longer have access. You can open the current file instead.') })
    return () => controller.abort()
  }, [reference.workspacePath, reference.snapshotId, change.path, change.binary, imageType, name, side, view, query?.session_id, query?.through_snapshot_id, query?.baseline_snapshot_id])
  const unsupported = change.binary && !imageType && !isViewableBinaryFile(name)
  return (
    <dialog ref={dialog} onCancel={event => { event.preventDefault(); close() }} className="h-[85vh] w-[min(95vw,1100px)] max-w-none rounded-xl border border-border bg-background p-0 text-foreground backdrop:bg-black/50">
      <div className="flex h-full flex-col">
        <header className="flex flex-wrap items-center gap-3 border-b border-border p-3">
          <h2 className="min-w-0 flex-1 break-all font-medium">{view === 'diff' ? 'File comparison' : 'Saved version'} · {name}</h2>
          {view === 'preview' && <select aria-label="Saved version" value={side} onChange={event => setSide(event.target.value as 'before' | 'after')} className="rounded border border-border bg-background p-1"><option value="before" disabled={change.status === 'A'}>{query ? 'Before comparison' : 'Before this reply'}</option><option value="after" disabled={change.status === 'D'}>{query ? 'After comparison' : 'After this reply'}</option></select>}
          <button type="button" className="text-xs underline" onClick={() => { void openWorkspaceFile(`${reference.workspacePath}/${change.path}`); close() }}>Open current file</button>
          <button type="button" onClick={close} className="rounded border border-border px-2 py-1">Close</button>
        </header>
        <div className="min-h-0 flex-1 overflow-auto">
          {view === 'diff' ? <FileDiffView workspacePath={reference.workspacePath} file={change.path} reply={reference} /> : error ? <p role="alert" className="p-4">{error}</p> : !content ? <p className="p-4">Loading saved version…</p> : unsupported ? <p className="p-4">Preview is not available for this file type.</p> : (
            <Suspense fallback={<p className="p-4">Loading preview…</p>}>
              <FileViewer source={{ selectedFile: { name, path: change.path }, fileContent: content.text, binaryFileData: content.bytes ?? null, loadingFileContent: false, showFileContent: true, setShowFileContent: show => { if (!show) close() }, openTabs: [], openFile: () => {}, closeFile: close, revealFolder: () => {} }} />
            </Suspense>
          )}
        </div>
      </div>
    </dialog>
  )
}
