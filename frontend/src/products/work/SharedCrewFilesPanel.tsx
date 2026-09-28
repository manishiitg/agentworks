import { useEffect, useState } from 'react'
import { FileText, Folder, Loader2 } from 'lucide-react'
import type { PlannerFile } from '../../services/api-types'
import { sharedCrewFileClient, sharedCrewRelativePath } from './sharedCrewFiles'
import { WorkspacePanelGuideButton } from '../../components/workflow/WorkspacePanelGuideButton'
import { panelGuideAskFromNode } from '../../components/workflow/workspacePanelGuideAsk'
import { WorkspaceViewIconButton } from '../../components/workflow/WorkspaceViewIconButton'

/**
 * Read-only file browser for someone else's Crew (Crew Run mode). The
 * browser proxy refuses raw cross-user workspace traffic, so the tree and
 * every file come through the mediated shared endpoints: crew-relative
 * paths, private subtrees (owner transcripts, run databases) already
 * excluded server-side. There are intentionally no create, rename, delete,
 * or edit affordances here.
 */
export function SharedCrewFilesPanel({ projectId, crewRoot, request, headerAction }: {
  projectId: string
  crewRoot: string
  /** Memory-panel "open file" requests: the nonce retriggers repeat opens of the same path. */
  request?: { path: string; nonce: number } | null
  headerAction?: React.ReactNode
}) {
  const [entries, setEntries] = useState<PlannerFile[]>([])
  const [truncated, setTruncated] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [openPath, setOpenPath] = useState<string | null>(null)
  const [content, setContent] = useState('')
  const [contentLoading, setContentLoading] = useState(false)
  const [contentError, setContentError] = useState<string | null>(null)
  const [reloadNonce, setReloadNonce] = useState(0)
  const guideAsk = panelGuideAskFromNode(headerAction)

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError(null)
    setOpenPath(null)
    setContent('')
    const client = sharedCrewFileClient(projectId, crewRoot)
    void client.listFiles()
      .then(response => {
        if (cancelled) return
        if (!response?.success) throw new Error(response?.message || 'Could not list Crew files.')
        setEntries(response.data || [])
        setTruncated(response.truncated === true)
      })
      .catch((cause: unknown) => {
        if (!cancelled) setError(cause instanceof Error ? cause.message : 'Could not list Crew files.')
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => { cancelled = true }
  }, [projectId, crewRoot, reloadNonce])

  const openEntry = async (entry: PlannerFile) => {
    setOpenPath(entry.filepath)
    setContent('')
    setContentError(null)
    setContentLoading(true)
    try {
      const response = await sharedCrewFileClient(projectId, crewRoot).readFile(entry.filepath)
      if (!response?.success) throw new Error(response?.message || 'Could not open file.')
      setContent(String(response.data?.content ?? ''))
    } catch (cause) {
      setContentError(cause instanceof Error ? cause.message : 'Could not open file.')
    } finally {
      setContentLoading(false)
    }
  }

  useEffect(() => {
    if (!request || entries.length === 0) return
    const match = entries.find(entry => entry.filepath === request.path && entry.type !== 'folder')
    if (match) void openEntry(match)
    // openEntry is stable per project; entries/request drive re-runs.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [entries, request])

  const rel = (filepath: string) => sharedCrewRelativePath(crewRoot, filepath) ?? filepath

  return (
    <div className="flex h-full min-h-0 flex-col bg-background" data-testid="shared-crew-files-panel">
      <div className="flex shrink-0 items-center gap-2 border-b border-border px-4 py-2.5">
        {openPath ? (
          <button
            type="button"
            onClick={() => setOpenPath(null)}
            className="rounded px-1.5 py-0.5 text-sm font-medium text-primary hover:bg-muted"
          >
            ← Files
          </button>
        ) : null}
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm font-semibold text-foreground">
            {openPath ? rel(openPath) : 'Workspace'}
          </p>
          {!openPath && (
            <p className="text-xs text-muted-foreground">Read-only — owned by another user.</p>
          )}
        </div>
        {guideAsk ? null : headerAction}
        <WorkspacePanelGuideButton topic="Files" ask={guideAsk} />
        <WorkspaceViewIconButton label="Refresh files" onClick={() => setReloadNonce(nonce => nonce + 1)} disabled={loading} spinning={loading} />
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto p-2">
        {loading ? (
          <div className="grid h-full place-items-center text-sm text-muted-foreground">
            <span><Loader2 className="mr-2 inline h-4 w-4 animate-spin" />Loading files…</span>
          </div>
        ) : error ? (
          <div className="grid h-full place-items-center p-6 text-center text-sm text-destructive">{error}</div>
        ) : openPath ? (
          contentLoading ? (
            <div className="grid h-full place-items-center text-sm text-muted-foreground">
              <span><Loader2 className="mr-2 inline h-4 w-4 animate-spin" />Opening file…</span>
            </div>
          ) : contentError ? (
            <div className="grid h-full place-items-center p-6 text-center text-sm text-destructive">{contentError}</div>
          ) : (
            <pre className="whitespace-pre-wrap break-words p-2 font-mono text-xs leading-5 text-foreground">{content || '(empty file)'}</pre>
          )
        ) : entries.length === 0 ? (
          <div className="grid h-full place-items-center p-6 text-center text-sm text-muted-foreground">No files in this Crew yet.</div>
        ) : (
          <div role="list" aria-label="Crew files">
            {truncated && (
              <p className="px-2 py-1 text-xs text-muted-foreground">Showing the first 1,000 entries.</p>
            )}
            {entries.map(entry => {
              const relative = rel(entry.filepath)
              const depth = relative.split('/').length - 1
              const name = relative.split('/').pop() || relative
              const isFolder = entry.type === 'folder'
              return isFolder ? (
                <div
                  key={entry.filepath}
                  role="listitem"
                  className="flex items-center gap-2 rounded px-2 py-1 text-sm text-muted-foreground"
                  style={{ paddingLeft: `${0.5 + depth * 1}rem` }}
                >
                  <Folder className="h-3.5 w-3.5 shrink-0" />
                  <span className="truncate font-medium">{name}</span>
                </div>
              ) : (
                <button
                  key={entry.filepath}
                  type="button"
                  role="listitem"
                  onClick={() => void openEntry(entry)}
                  className="flex w-full items-center gap-2 rounded px-2 py-1 text-left text-sm text-foreground hover:bg-muted"
                  style={{ paddingLeft: `${0.5 + depth * 1}rem` }}
                >
                  <FileText className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                  <span className="truncate">{name}</span>
                </button>
              )
            })}
          </div>
        )}
      </div>
    </div>
  )
}
