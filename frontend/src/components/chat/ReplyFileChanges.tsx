import { lazy, Suspense, useEffect, useRef, useState } from 'react'
import api from '../../services/api'
import type { ReplyFileChangesRef } from '../../utils/replyFileChanges'
import { useProjectMode } from '../../products/work/codeLocalFiles'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { isViewableBinaryFile, openWorkspaceFile } from '../../utils/openWorkspaceFile'
import { FileDiffView } from '../workspace/FileDiffView'

const FileViewer = lazy(() => import('../FileContentViewer').then(module => ({ default: module.FileContentViewerBody })))
interface Change { path: string; status: string; added?: number; removed?: number; binary?: boolean }
interface Comparison { changes: Change[]; snapshot: { status: string } }
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
  const [comparison, setComparison] = useState<Comparison | null>(null)
  const [selected, setSelected] = useState<{ change: Change; view: 'preview' | 'diff' } | null>(null)
  useEffect(() => {
    const controller = new AbortController()
    void api.get<Comparison>(endpoint, { params: { ...paramsFor(reference), op: 'snapshot_compare' }, signal: controller.signal })
      .then(response => { if (!controller.signal.aborted) setComparison(response.data) })
      .catch(() => { /* Expired/unavailable history has no change card. */ })
    return () => controller.abort()
  }, [reference.workspacePath, reference.snapshotId])
  if (!comparison?.changes?.length) {
    return comparison?.snapshot?.status === 'partial'
      ? <p className="my-3 text-xs text-muted-foreground" role="status">File comparison is incomplete. Some files could not be captured for this reply.</p>
      : null
  }
  return (
    <section className="my-3 rounded-lg border border-border px-3 py-2 text-sm" aria-label="Files changed during this reply">
      <details>
        <summary className="cursor-pointer font-medium">Files changed during this reply · {comparison.changes.length}</summary>
        <p className="mt-2 text-xs text-muted-foreground">Saved versions. Other chats or editors may have contributed.</p>
        {comparison.snapshot.status === 'partial' && <p className="text-xs text-muted-foreground">Some files could not be captured.</p>}
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
    return () => { previouslyFocused?.focus() }
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
  }, [reference.workspacePath, reference.snapshotId, change.path, change.binary, imageType, name, side, view, query?.session_id, query?.through_snapshot_id])
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
