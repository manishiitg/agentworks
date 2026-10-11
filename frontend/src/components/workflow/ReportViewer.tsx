// HTML report viewer. A workspace owns one or more reporting documents under
// db/reports/. The shared toolbar selects the active document; each HTML file
// tabs, sections, a sidebar, or a single scrolling page.

import { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { BarChart3, Loader2, RefreshCw } from 'lucide-react'
import api, { agentApi, workspaceApi, getApiBaseUrl } from '../../services/api'
import { useReportFilePreviewStore } from '../../stores/useReportFilePreviewStore'
import { useWorkflowStore } from '../../stores/useWorkflowStore'
import {
  REPORT_PREVIEW_PREFERENCE_CHANGED_EVENT,
  readReportPreviewPreference,
  type ReportPreviewDevice,
} from '../../utils/reportPreviewPreference'
import ModalPortal from '../ui/ModalPortal'
import { FilePreviewModal } from './reportWidgets/FilePreviewModal'
import { HtmlReportFrame } from './reportWidgets/HtmlWidgetFrame'
import { ReportEmbedProvider, type ReportDataApi } from './reportWidgets/reportEmbedContext'
import { createReportQuery } from './reportWidgets/reportQuery'
import { isStreamableReportMediaPath } from './reportWidgets/reportMedia'
import { allowedReportPath, normalizeReportSource, renderReportMarkdown, reportMarkdownBasePath } from './reportWidgets/reportMarkdown'
import { useReportChat } from './reportWidgets/useReportChat'
import { EntityIdentityIcon } from '../ui/EntityIdentityIcon'

import { WORKFLOW_REPORT_REFRESH_EVENT } from './reportRefreshEvent'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { useSelectedReportDocument } from './reportDocuments'
import { WorkspacePanelGuideButton } from './WorkspacePanelGuideButton'
import { panelGuideAskFromNode } from './workspacePanelGuideAsk'

function debugReportView(event: string, detail?: Record<string, unknown>) {
  if (!import.meta.env.DEV) return
  console.debug('[ReportView]', event, detail)
}

type ReportDocument = {
  path: string
  label: string
  html?: string
}

interface ReportViewerProps {
  workspacePath: string
  isOpen: boolean
  onClose: () => void
}

interface ReportViewProps {
  workspacePath: string
  selectedRunFolder?: string | null
  reviewData?: unknown
  onClose?: () => void
  focusTier?: 'mobile'
  reserveTopControlsSpace?: boolean
  documentPath?: string
  emptyDescription?: string
  emptyAction?: React.ReactNode
  emptyIdentity?: { icon?: string; name: string; projectName: string }
  sendChatMessage?: ReportDataApi['sendChatMessage']
  headerAction?: React.ReactNode
}

const normalizeSource = normalizeReportSource

async function readWorkspaceText(filepath: string): Promise<string | null> {
  try {
    const marker = filepath.lastIndexOf('/db/')
    if (marker >= 0) {
      const response = await api.get<string>('/api/workflow/report-preview/file', {
        params: { workspace: filepath.slice(0, marker), path: filepath.slice(marker + 1) },
        responseType: 'text',
      })
      return typeof response.data === 'string' ? response.data : null
    }
    const response = await agentApi.getPlannerFileContent(filepath)
    return response?.success && typeof response.data?.content === 'string' ? response.data.content : null
  } catch { return null }
}

// The endpoint answers a failed script with {error, stderr}; surface both so
// the report (and whoever reads its error box) sees why, not just "422".
function reportRunError(error: unknown): string {
  const data = (error as { response?: { data?: unknown } })?.response?.data
  if (data && typeof data === 'object') {
    const body = data as { error?: string; stderr?: string }
    if (body.error) return body.stderr ? `${body.error}\n${body.stderr}` : body.error
  }
  if (typeof data === 'string' && data.trim()) return data.trim()
  return error instanceof Error ? error.message : 'Script failed.'
}

function useReportDataApi(workspacePath: string, sendChatMessage: ReportDataApi['sendChatMessage']): ReportDataApi {
  // Parents often pass sendChatMessage as an inline arrow, a new function on
  // every render (e.g. each keystroke in the chat input). A new data API makes
  // the report re-dispatch report:data and re-run its live scripts, so the
  // callback is read through a ref and never changes the API's identity.
  const sendChatMessageRef = useRef(sendChatMessage)
  sendChatMessageRef.current = sendChatMessage
  return useMemo(() => {
    const getText = async (path: string): Promise<string | null> => {
      const allowed = allowedReportPath(path)
      return allowed ? readWorkspaceText(`${workspacePath}/${allowed}`) : null
    }
    const renderMarkdown = (markdown: string): string => renderReportMarkdown(markdown)
    const mediaUrl = async (allowedPath: string): Promise<string> => {
      // `api` uses the origin itself in web deployments, so API routes must
      // retain their `/api` prefix. Without it the SPA fallback answers with
      // index.html (HTTP 200), leaving response.data.url undefined and every
      // native media element pointed at `/undefined`.
      const response = await api.post('/api/workflow/report-preview/media-url', { workspace: workspacePath, path: allowedPath })
      // Resolve against the configured API origin (also supports remote workspaces).
      return new URL(response.data.url, new URL(getApiBaseUrl(), window.location.href)).href
    }
    return {
      workspacePath,
      getRelayRuns: async (options = {}) => {
        const response = await api.get('/api/workflow/report-preview/relay-runs', { params: { workspace: workspacePath, ...options } })
        return response.data
      },
      getCosts: (options) => agentApi.getCosts(workspacePath, { ...options, view: 'summary' }),
      sendChatMessage: (...args: Parameters<ReportDataApi['sendChatMessage']>) => sendChatMessageRef.current(...args),
      query: createReportQuery(workspacePath, async body => {
        const response = await api.post('/api/workflow/report-preview/query', body)
        return response.data
      }),
      getText,
      get: async (path: string) => {
        const text = await getText(path)
        if (!text?.trim()) return null
        try { return JSON.parse(text) } catch { return text }
      },
      renderMarkdown,
      getHtml: async (path: string) => {
        const text = await getText(path)
        if (text == null) return null
        return renderReportMarkdown(text, reportMarkdownBasePath(path)) || null
      },
      mediaUrl: async (path: string) => {
        const allowed = allowedReportPath(path)
        if (!allowed?.startsWith('db/assets/')) return null
        return mediaUrl(allowed)
      },
      fileUrl: async (path: string) => {
        const allowed = allowedReportPath(path)
        if (!allowed) return null
        // Compatibility for existing reports that used fileUrl for recordings:
        // return a range-streaming URL instead of downloading the entire media
        // file into a Blob before the report can render.
        if (allowed.startsWith('db/assets/') && isStreamableReportMediaPath(allowed)) {
          return mediaUrl(allowed)
        }
        try {
          const response = await workspaceApi.get(`/api/documents/${encodeURIComponent(`${workspacePath}/${allowed}`)}`, {
            params: { download: 'true' }, responseType: 'blob', headers: { Accept: 'application/octet-stream' }, transformResponse: [(data) => data],
          })
          const blob = response.data instanceof Blob ? response.data : new Blob([response.data])
          return URL.createObjectURL(blob)
        } catch {
          return null
        }
      },
      openFile: (path: string) => {
        const allowed = allowedReportPath(path)
        if (allowed) useReportFilePreviewStore.getState().show({ path: `${workspacePath}/${allowed}` })
      },
      updateField: async (table, rowId, column, value) => {
        const response = await agentApi.updateReportFields(`${workspacePath}/db/db.sqlite`, table, rowId, { [column]: value })
        if (!response.success || !response.data) throw new Error(response.error || 'Update failed.')
        return { oldValue: response.data.old_values[column], newValue: response.data.new_values[column] }
      },
      updateFields: async (table, rowId, fields) => {
        const response = await agentApi.updateReportFields(`${workspacePath}/db/db.sqlite`, table, rowId, fields)
        if (!response.success || !response.data) throw new Error(response.error || 'Update failed.')
        return { oldValues: response.data.old_values, newValues: response.data.new_values }
      },
      run: async (path: string, args?: unknown) => {
        try {
          const response = await api.post('/api/workflow/report-preview/run', { workspace: workspacePath, path, args: args ?? {} })
          return response.data.data
        } catch (error) {
          throw new Error(reportRunError(error))
        }
      },
    }
  }, [workspacePath])
}

async function loadReportDocument(workspacePath: string, documentPath = 'db/reports/index.html'): Promise<ReportDocument | null> {
  let allowedDocumentPath = allowedReportPath(documentPath)
  if (!allowedDocumentPath) return null
  if (allowedDocumentPath.startsWith('db/reports/managed/')) {
    const { data } = await api.post<{ resolved_path: string }>('/api/dashboards', { workspace: workspacePath, action: 'resolve', document_path: allowedDocumentPath })
    allowedDocumentPath = data.resolved_path
  }
  const path = `${normalizeSource(workspacePath)}/${allowedDocumentPath}`
  const html = await readWorkspaceText(path)
  if (html == null) return null
  const title = html.match(/<title[^>]*>\s*([^<]+?)\s*<\/title>/i)?.[1]?.trim()
  return { path, html, label: title || 'Dashboard' }
}

function ReportViewComponent({ workspacePath, onClose, focusTier, documentPath, emptyDescription, emptyAction, emptyIdentity, sendChatMessage, headerAction }: ReportViewProps) {
  const [report, setReport] = useState<ReportDocument | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [refreshNonce, setRefreshNonce] = useState(0)
  // open_workspace_view(view="report", target="<tab>") lands here; the frame
  // hands it to the report HTML, which owns its own tabs.
  const viewTarget = useWorkflowStore(state => state.workspaceViewTarget)
  const reportFocus = useMemo(() => viewTarget?.view === 'report'
    ? { value: viewTarget.target, token: viewTarget.token }
    : undefined, [viewTarget])
  const [previewPreference, setPreviewPreference] = useState<ReportPreviewDevice>(() => readReportPreviewPreference(workspacePath))
  const reportChat = useReportChat(workspacePath)
  const dataApi = useReportDataApi(workspacePath, sendChatMessage ?? reportChat.request)
  const selectedDocumentPath = useSelectedReportDocument(workspacePath, documentPath)

  const refresh = useCallback(() => setRefreshNonce(value => value + 1), [])
  // Re-run the dashboard when its HTML or its workflow DB is written through
  // the tools, or a run in this workflow finishes (at most every 2s during a
  // burst). No polling: without the live feed, manual refresh remains.
  useLiveRefetch(refresh, { kinds: ['report'], workflow: workspacePath, fallbackMs: 0, safetyMs: 0, minIntervalMs: 2_000 })
  useEffect(() => {
    const sync = () => setPreviewPreference(readReportPreviewPreference(workspacePath))
    window.addEventListener(REPORT_PREVIEW_PREFERENCE_CHANGED_EVENT, sync)
    window.addEventListener(WORKFLOW_REPORT_REFRESH_EVENT, refresh)
    return () => {
      window.removeEventListener(REPORT_PREVIEW_PREFERENCE_CHANGED_EVENT, sync)
      window.removeEventListener(WORKFLOW_REPORT_REFRESH_EVENT, refresh)
    }
  }, [refresh, workspacePath])
  useEffect(() => setPreviewPreference(readReportPreviewPreference(workspacePath)), [workspacePath])
  useEffect(() => {
    debugReportView('report loading', { workspacePath, refreshNonce })
    let cancelled = false
    setLoading(true)
    setError(null)
    void loadReportDocument(workspacePath, selectedDocumentPath).then(next => {
      if (cancelled) return
      setReport(next)
    }).catch(reason => {
      if (!cancelled) setError(reason instanceof Error ? reason.message : 'Failed to load report.')
    }).finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [refreshNonce, selectedDocumentPath, workspacePath])

  useEffect(() => {
    debugReportView('mounted', { workspacePath })
    return () => debugReportView('unmounted', { workspacePath })
  }, [workspacePath])

  const guideAsk = panelGuideAskFromNode(headerAction)
  const previewMode = focusTier || previewPreference
  const shellClass = previewMode === 'mobile' ? 'mx-auto w-full max-w-[480px] p-1.5' : 'w-full max-w-full'
  const runtime = useMemo(() => ({ data: dataApi }), [dataApi])

  return (
    <ReportEmbedProvider value={runtime}>
      <div className="relative flex h-full w-full flex-col overflow-hidden bg-background text-foreground">
        <div className="absolute right-3 top-3 z-20 flex gap-1">
          {guideAsk ? null : headerAction}
          <WorkspacePanelGuideButton topic="Dashboard" ask={guideAsk} />
          <button type="button" onClick={refresh} aria-label="Refresh dashboard" title="Refresh dashboard" className="inline-flex h-8 w-8 items-center justify-center rounded-md border border-border bg-background/95 text-muted-foreground shadow-sm backdrop-blur-sm transition-colors hover:bg-muted hover:text-foreground">
            <RefreshCw className="h-3.5 w-3.5" />
          </button>
          {onClose && <button type="button" onClick={onClose} aria-label="Close dashboard" className="inline-flex h-8 w-8 items-center justify-center rounded-md border border-border bg-background/95 text-lg text-muted-foreground shadow-sm backdrop-blur-sm transition-colors hover:bg-muted hover:text-foreground">×</button>}
        </div>
        <div
          tabIndex={0}
          aria-label="Dashboard content"
          className="min-h-0 flex-1 overflow-y-auto overscroll-y-contain"
        >
          <div className={shellClass}>
            {loading && !report && <div className="flex min-h-64 items-center justify-center gap-2 text-sm text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" /> Loading dashboard…</div>}
            {error && <div className="m-3 rounded-md border border-destructive/30 bg-destructive/10 p-3 text-sm text-destructive">Failed to load report: {error}</div>}
            {!loading && !error && !report && (
              <div className="m-3 flex flex-col items-center gap-3 rounded-xl border border-dashed border-border p-8 text-center">
                {emptyIdentity ? (
                  <div className="flex flex-col items-center gap-2">
                    <EntityIdentityIcon icon={emptyIdentity.icon} label={emptyIdentity.name} className="h-12 w-12 rounded-xl text-2xl shadow-sm" />
                    <div>
                      <div className="text-[11px] font-medium uppercase tracking-[0.16em] text-muted-foreground">Project identity</div>
                      <div className="mt-0.5 text-lg font-semibold">{emptyIdentity.name}</div>
                      {emptyIdentity.projectName !== emptyIdentity.name && <div className="mt-0.5 text-xs text-muted-foreground">Project name: {emptyIdentity.projectName}</div>}
                    </div>
                  </div>
                ) : <BarChart3 className="h-8 w-8 text-muted-foreground" />}
                <div>
                  <div className="font-semibold">No dashboard yet</div>
                  <p className="mt-1 text-sm text-muted-foreground">{emptyDescription ?? 'Run the workflow or ask the Builder to set up its dashboard. It will appear here when it is ready.'}</p>
                </div>
                <div className="flex flex-wrap items-center justify-center gap-2">
                  {emptyAction}
                  <button type="button" onClick={refresh} className="inline-flex items-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-sm hover:bg-muted">
                    <RefreshCw className="h-3.5 w-3.5" /> Check again
                  </button>
                </div>
              </div>
            )}
            {!loading && report && !report.html && <div className="m-3 rounded-md border border-destructive/30 bg-destructive/10 p-3 text-sm text-destructive">Could not read {report.label}.</div>}
            {report?.html && <HtmlReportFrame key={report.path} html={report.html} title={report.label} autoHeight refreshToken={refreshNonce} focusTarget={reportFocus} className="block w-full border-0" />}
          </div>
        </div>
        <FilePreviewModal />
      </div>
    </ReportEmbedProvider>
  )
}

export const ReportView = memo(ReportViewComponent)

export function ReportViewer({ workspacePath, isOpen, onClose }: ReportViewerProps) {
  if (!isOpen) return null
  return (
    <ModalPortal>
      <div className="fixed inset-0 z-[9999] flex items-center justify-center bg-black/60 px-2 py-3 backdrop-blur-sm sm:px-4 sm:py-6" onClick={onClose}>
        <div className="flex max-h-[94vh] w-full max-w-6xl flex-col overflow-hidden rounded-xl border border-border/70 bg-background shadow-[0_24px_80px_rgba(0,0,0,0.45)] sm:max-h-[90vh] sm:rounded-2xl" onClick={event => event.stopPropagation()}>
          <ReportView workspacePath={workspacePath} onClose={onClose} />
        </div>
      </div>
    </ModalPortal>
  )
}
