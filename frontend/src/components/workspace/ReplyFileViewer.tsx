import { lazy, Suspense, useEffect, useState } from 'react'
import { shownFilePath } from '../../utils/replyFileChanges'
import api, { agentApi, workspaceApi } from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'
import { captureChatIdentity, assertChatIdentityCurrent } from '../../utils/chatIdentity'
import { sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'
import { isViewableBinaryFile } from '../../utils/openWorkspaceFile'
import DiffRenderer, { type DiffLineAnchor } from '../ui/DiffRenderer'
import { reviewEndpoint, type ReviewResult } from './chatFileReviewData'
import type { SavedFileSelection } from './chatFileNavigation'

const FileViewer = lazy(() => import('../FileContentViewer').then(module => ({ default: module.FileContentViewerBody })))
type Version = 'current' | 'after' | 'before' | 'changes'

/** The existing Files panel hosts both current and immutable saved content.
 * Only Current uses the editable workspace source. */
export function ReplyFileViewer({ selection, tabId, onBack }: { selection: SavedFileSelection; tabId: string; onBack: () => void }) {
  const { reference, change, query } = selection
  const [version, setVersion] = useState<Version>('changes')
  const [content, setContent] = useState<{ text: string; bytes?: ArrayBuffer } | null>(null)
  const [comparison, setComparison] = useState<ReviewResult['comparison']>()
  const [error, setError] = useState('')
  const [anchor, setAnchor] = useState<DiffLineAnchor | null>(null)
  const [comment, setComment] = useState('')
  const [sending, setSending] = useState(false)
  const name = change.path.split('/').pop() || change.path
  const imageType = ({ png: 'image/png', jpg: 'image/jpeg', jpeg: 'image/jpeg', gif: 'image/gif', webp: 'image/webp', svg: 'image/svg+xml' } as Record<string, string>)[name.split('.').pop()?.toLowerCase() || '']
  const params = { workspace_path: reference.workspacePath, session_id: reference.sessionId, snapshot_id: reference.snapshotId, ...query, file: change.path }
  useEffect(() => {
    const controller = new AbortController()
    const identity = captureChatIdentity()
    setContent(null); setComparison(undefined); setError(''); setAnchor(null)
    const load = async () => {
      if (version === 'current') {
        const path = `${reference.workspacePath}/${change.path}`
        if (isViewableBinaryFile(name)) {
          const { data } = await workspaceApi.get<ArrayBuffer>(`/api/documents/${encodeURIComponent(path)}`, { params: { download: 'true' }, responseType: 'arraybuffer', signal: controller.signal })
          assertChatIdentityCurrent(identity)
          if (!controller.signal.aborted) setContent({ text: '', bytes: data })
        } else {
          const response = await agentApi.getPlannerFileContent(path)
          assertChatIdentityCurrent(identity)
          if (!response.success || !response.data) throw new Error(response.message || 'The current file is unavailable. Browse files or choose a saved version.')
          if (response.data.is_binary && !response.data.is_image) throw new Error('Preview is unavailable for this file type.')
          if (!controller.signal.aborted) setContent({ text: String(response.data.content ?? '') })
        }
        return
      }
      if (version === 'changes') {
        const { data } = await api.get<ReviewResult>(reviewEndpoint, { params: { ...params, op: 'snapshot_compare' }, signal: controller.signal })
        if (!data.comparison) throw new Error(data.history.note || 'This saved comparison is no longer available.')
        if (!controller.signal.aborted) setComparison(data.comparison)
        return
      }
      const { data } = await api.get<ArrayBuffer>(reviewEndpoint, { params: { ...params, op: 'snapshot_file', side: version }, responseType: 'arraybuffer', signal: controller.signal })
      let text = ''
      if (imageType) {
        const bytes = new Uint8Array(data)
        let binary = ''
        for (let offset = 0; offset < bytes.length; offset += 8192) binary += String.fromCharCode(...bytes.subarray(offset, offset + 8192))
        text = `data:${imageType};base64,${btoa(binary)}`
      } else if (!change.binary && !isViewableBinaryFile(name)) text = new TextDecoder().decode(data)
      if (!controller.signal.aborted) setContent({ text, bytes: data })
    }
    void load().catch(cause => { if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : 'This file is unavailable. Choose another version or browse files.') })
    return () => controller.abort()
  }, [reference.workspacePath, reference.sessionId, reference.snapshotId, change.path, change.binary, version, imageType, name, query?.baseline_snapshot_id, query?.through_snapshot_id])
  const send = async () => {
    if (!anchor || !comment.trim()) return
    const identity = captureChatIdentity()
    setSending(true); setError('')
    try {
      const { data } = await api.get<{ context: string }>(reviewEndpoint, { params: { ...params, op: 'snapshot_compare', side: anchor.side, line: anchor.line } })
      assertChatIdentityCurrent(identity)
      if (useChatStore.getState().chatTabs[tabId]?.sessionId !== reference.sessionId) throw new Error('This chat changed. Reopen the file before sending.')
      if (!data.context) throw new Error('Choose a saved line again.')
      await sendWorkspacePaneMessageToChat({ tabId, message: `${data.context}\n\n${comment.trim()}` })
      setComment(''); setAnchor(null)
    } catch (cause) { setError(cause instanceof Error ? cause.message : 'Could not send to this chat.') }
    finally { setSending(false) }
  }
  return <section className="flex h-full min-h-0 flex-col" aria-label="File versions">
    <header className="flex flex-wrap items-center gap-2 border-b border-border p-3">
      <strong className="min-w-0 flex-1 break-all text-sm">{shownFilePath(change.path)}</strong>
      <select aria-label="File version" value={version} className="rounded border border-border bg-background p-1 text-xs" onChange={event => setVersion(event.target.value as Version)}>
        <option value="current">Current</option><option value="after" disabled={change.status === 'D'}>{query ? 'After comparison' : 'After this reply'}</option><option value="before" disabled={change.status === 'A'}>{query ? 'Before comparison' : 'Before this reply'}</option><option value="changes">Changes</option>
      </select>
      <button type="button" className="text-xs underline" onClick={onBack}>Browse files</button>
    </header>
    {error && <p role="alert" className="p-3 text-sm text-destructive">{error}</p>}
    <div className="min-h-0 flex-1 overflow-auto">
      {!error && (version === 'changes' ? !comparison ? <p className="p-3 text-sm">Loading comparison…</p> : change.binary ? <p className="p-3 text-sm">Choose Before or After to preview this file.</p> : <DiffRenderer content={comparison.diff || ''} onSelectLine={setAnchor} maxHeightClassName="max-h-none" />
        : !content ? <p className="p-3 text-sm">Loading file…</p>
        : <Suspense fallback={<p className="p-3 text-sm">Loading viewer…</p>}>
          {version !== 'current' && change.binary && !imageType && !isViewableBinaryFile(name) ? <p className="p-3 text-sm">Preview is unavailable for this file type.</p>
          : <FileViewer editable={version === 'current'} source={{ selectedFile: { name, path: version === 'current' ? `${reference.workspacePath}/${change.path}` : change.path }, fileContent: content.text, binaryFileData: content.bytes ?? null, loadingFileContent: false, showFileContent: true, setShowFileContent: show => { if (!show) onBack() }, openTabs: [], openFile: () => {}, closeFile: onBack, revealFolder: onBack }} />}</Suspense>)}
      {comparison?.truncated && <p className="p-3 text-xs text-muted-foreground">Only the start of this large comparison is shown.</p>}
    </div>
    {anchor && <div className="space-y-2 border-t p-3"><p className="text-xs">{anchor.side} line {anchor.line}</p><textarea aria-label="Request a change" className="min-h-20 w-full rounded border bg-background p-2 text-sm" value={comment} onChange={event => setComment(event.target.value)} placeholder="What should change here?" /><button type="button" disabled={sending || !comment.trim()} className="rounded border px-3 py-1 text-sm disabled:opacity-50" onClick={() => void send()}>Send to this chat</button></div>}
  </section>
}
