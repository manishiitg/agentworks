// HTML report viewer. A workspace owns one or more reporting documents under
// db/reports/. The shared toolbar selects the active document; each HTML file
// tabs, sections, a sidebar, or a single scrolling page.

import { lazy, memo, Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { LayoutDashboard, Loader2, RefreshCw } from 'lucide-react'
import { DashboardEmptyState } from './DashboardEmptyState'
import api, { agentApi, getApiBaseUrl } from '../../services/api'
import { useReportFilePreviewStore } from '../../stores/useReportFilePreviewStore'
import { useWorkflowStore } from '../../stores/useWorkflowStore'
import {
  REPORT_PREVIEW_PREFERENCE_CHANGED_EVENT,
  readReportPreviewPreference,
  type ReportPreviewDevice,
} from '../../utils/reportPreviewPreference'
import ModalPortal from '../ui/ModalPortal'
import { FilePreviewModal } from './reportWidgets/FilePreviewModal'
import { ReportQuestionFrame } from './reportWidgets/ReportQuestionFrame'
import { DashboardQuestionConversation, type DashboardQuestionCapability } from './reportWidgets/DashboardQuestionConversation'
import { ReportEmbedProvider, type ReportDataApi } from './reportWidgets/reportEmbedContext'
import { createReportQuery } from './reportWidgets/reportQuery'
import { isStreamableReportMediaPath } from './reportWidgets/reportMedia'
import { allowedReportPath, normalizeReportSource, renderReportMarkdown, reportMarkdownBasePath } from './reportWidgets/reportMarkdown'
import { useReportChat } from './reportWidgets/useReportChat'
import { DashboardChatDialog } from './reportWidgets/DashboardChatDialog'

import { WORKFLOW_REPORT_REFRESH_EVENT } from './reportRefreshEvent'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { loadReportDocumentCatalog, selectReportDocument, useSelectedReportDocument, type ReportDocumentCatalog } from './reportDocuments'
import { reportDocumentLabel } from './reportDocumentLabels'
import { WorkspaceViewHeader } from './WorkspaceViewHeader'
import { DashboardLayoutActions } from './DashboardLayoutControls'
import { ShareDashboardButton } from './ShareDashboardButton'

function debugReportView(event: string, detail?: Record<string, unknown>) {
  if (!import.meta.env.DEV) return
  console.debug('[ReportView]', event, detail)
}

type ReportDocument = {
  path: string
  label: string
  html?: string
  questions?: { resourceId: string; revision: string; projectId: string; workspacePath: string; capability: DashboardQuestionCapability; product: string }
}

const DashboardChatArea = lazy(() => import('../ChatArea'))
const DefaultRelayDashboard = lazy(() => import('./reportWidgets/DefaultRelayDashboard'))

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
  sendChatMessage?: ReportDataApi['sendChatMessage']
  /** Standalone viewing links have no project chat destination. */
  allowContentQuestions?: boolean
  dashboardConversation?: boolean
  /** Standalone Crew links keep the caller's native chat beside the UI. */
  crewConversationPane?: boolean
  defaultRelayDashboard?: boolean
  /** Standalone dashboard pages show the dashboard alone, without the app's help, refresh and close buttons. */
  hideToolbar?: boolean
}

const normalizeSource = normalizeReportSource

async function readWorkspaceText(workspacePath: string, path: string): Promise<string | null> {
  try {
    const response = await api.get<string>('/api/workflow/report-preview/file', {
      params: { workspace: workspacePath, path },
      responseType: 'text',
    })
    return typeof response.data === 'string' ? response.data : null
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
      return allowed ? readWorkspaceText(workspacePath, allowed) : null
    }
    const renderMarkdown = (markdown: string): string => renderReportMarkdown(markdown)
    const mediaUrl = async (allowedPath: string): Promise<string> => {
      // `api` uses the origin itself in web deployments, so API routes must
      // retain their `/api` prefix. Without it the SPA fallback answers with
      // index.html (HTTP 200), leaving response.data.url undefined and every
      // native media element pointed at `/undefined`.
      let response
      try {
        response = await api.post('/api/workflow/report-preview/media-url', { workspace: workspacePath, path: allowedPath })
      } catch (error) {
        const detail = (error as { response?: { data?: unknown } })?.response?.data
        throw new Error(typeof detail === 'string' && detail.trim() ? detail.trim() : 'This video could not be loaded. Ask the Builder to check its saved export.')
      }
      // Resolve against the configured API origin (also supports remote workspaces).
      return new URL(response.data.url, new URL(getApiBaseUrl(), window.location.href)).href
    }
    return {
      workspacePath,
      uploadFile: async (file: File) => {
        const body = new FormData()
        body.append("workspace", workspacePath)
        body.append("file", file)
        try {
          const response = await api.post("/api/workflow/report-preview/upload", body, { params: { workspace: workspacePath }, headers: { "Content-Type": "multipart/form-data" } })
          return { ...response.data, name: file.name, size: file.size, content_type: file.type }
        } catch (cause) {
          const detail = (cause as { response?: { data?: { error?: string | { message?: string } } } })?.response?.data?.error
          throw new Error(typeof detail === 'string' ? detail : detail?.message || 'This file could not be added.')
        }
      },
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
        return mediaUrl(path)
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
          const response = await api.get('/api/workflow/report-preview/file', {
            params: { workspace: workspacePath, path: allowed }, responseType: 'blob', headers: { Accept: 'application/octet-stream' }, transformResponse: [(data) => data],
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

async function loadReportDocument(workspacePath: string, documentPath = 'db/reports/index.html', dashboardConversation = false): Promise<ReportDocument | null> {
  let allowedDocumentPath = allowedReportPath(documentPath)
  if (!allowedDocumentPath) return null
  let questions: ReportDocument['questions']
  let liveHTML: string | undefined
  if (dashboardConversation && allowedDocumentPath.startsWith('db/reports/managed/')) {
    const slug = allowedDocumentPath.split('/')[3]
    const { data: discovery } = await api.post<{ resource_id?: string }>('/api/dashboards', { workspace: workspacePath, action: 'get', dashboard_id: slug })
    if (discovery.resource_id) {
      const { data: view } = await api.get<{ resource_id: string; project_id: string; product: string; dashboard: { published_revision: string }; capability: DashboardQuestionCapability; live_page?: { document_path: string; html: string } }>(`/api/dashboards/${encodeURIComponent(discovery.resource_id)}`)
      // Authored pages keep their captured publication. The server may supply
      // current HTML for its untouched automatic page on the stable URL.
      if (view.live_page && allowedDocumentPath === view.live_page.document_path) {
        allowedDocumentPath = view.live_page.document_path
        liveHTML = view.live_page.html
      } else {
        allowedDocumentPath = `db/reports/managed/${slug}/${view.dashboard.published_revision}/index.html`
      }
      questions = { resourceId: view.resource_id, revision: view.dashboard.published_revision, projectId: view.project_id, workspacePath, capability: view.capability, product: view.product }
    }
  }
  if (!questions && allowedDocumentPath.startsWith('db/reports/managed/')) {
    const { data } = await api.post<{ resolved_path: string }>('/api/dashboards', { workspace: workspacePath, action: 'resolve', document_path: allowedDocumentPath })
    allowedDocumentPath = data.resolved_path
  }
  const path = `${normalizeSource(workspacePath)}/${allowedDocumentPath}`
  const html = liveHTML ?? await readWorkspaceText(normalizeSource(workspacePath), allowedDocumentPath)
  if (html == null) return null
  const title = html.match(/<title[^>]*>\s*([^<]+?)\s*<\/title>/i)?.[1]?.trim()
  return { path, html, label: title || 'Dashboard', questions }
}

function ReportViewComponent({ workspacePath, onClose, focusTier, documentPath, emptyDescription, emptyAction, sendChatMessage, hideToolbar = false, allowContentQuestions = true, dashboardConversation = false, crewConversationPane = false, defaultRelayDashboard = false }: ReportViewProps) {
  const [report, setReport] = useState<ReportDocument | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [catalog, setCatalog] = useState<(ReportDocumentCatalog & { workspacePath: string; activePath: string }) | null>(null)
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
    const load = async () => {
      // Direct links and restored dashboards do not mount the context card or
      // its document picker. Read the catalog here too so server-provided
      // project defaults are ready before looking for a legacy index.html.
      let path = selectedDocumentPath
      let documents: ReportDocumentCatalog | undefined
      if (!documentPath) {
        documents = await loadReportDocumentCatalog(workspacePath)
        if (cancelled) return null
        if (documents.documents.length && !documents.documents.some(document => document.path === path)) path = documents.defaultPath
      }
      const next = await loadReportDocument(workspacePath, path, dashboardConversation)
      if (cancelled) return null
      setCatalog(documents ? { ...documents, workspacePath, activePath: path } : null)
      const named = documents?.documents.find(document => document.path === path)
      return next && named?.title && named.title !== 'Dashboard' ? { ...next, label: reportDocumentLabel(named, documents!.documents) } : next
    }
    void load().then(next => {
      if (cancelled) return
      setReport(next)
    }).catch(reason => {
      if (!cancelled) setError(reason instanceof Error ? reason.message : 'Failed to load report.')
    }).finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [refreshNonce, selectedDocumentPath, workspacePath, dashboardConversation, documentPath])

  useEffect(() => {
    debugReportView('mounted', { workspacePath })
    return () => debugReportView('unmounted', { workspacePath })
  }, [workspacePath])

  const currentCatalog = catalog?.workspacePath === workspacePath ? catalog : null
  const dashboardTitle = !loading && !error && report ? report.label : 'Dashboard'
  const title = !documentPath && currentCatalog && currentCatalog.documents.length > 1
    ? <select aria-label="Choose dashboard" title={dashboardTitle} value={currentCatalog.activePath} disabled={loading}
        onChange={event => selectReportDocument(workspacePath, event.target.value)}
        className="max-w-full cursor-pointer truncate rounded-md border-0 bg-transparent py-1 pr-1 text-sm font-semibold focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-60">
        {currentCatalog.documents.map(document => <option key={document.path} value={document.path}>{reportDocumentLabel(document, currentCatalog.documents)}</option>)}
      </select>
    : dashboardTitle
  const previewMode = focusTier || previewPreference
  const shellClass = previewMode === 'mobile' ? 'mx-auto w-full max-w-[480px] p-1.5' : 'w-full max-w-full'
  const conversationPane = crewConversationPane && report?.questions?.product === 'crew' && report.questions.capability.can_ask
  const runtime = useMemo(() => ({ data: dataApi }), [dataApi])

  return (
    <ReportEmbedProvider value={runtime}>
      <div className="relative flex h-full w-full flex-col overflow-hidden bg-background text-foreground">
        {!hideToolbar && <WorkspaceViewHeader icon={LayoutDashboard} title={title} actions={<>
          {/* Share is the main action on a page people pass around (PLAT-750): the link and who can open it. */}
          {report && !dashboardConversation && <ShareDashboardButton workspacePath={workspacePath} documentPath={report.path} />}
          <button type="button" onClick={refresh} aria-label="Refresh dashboard" title="Refresh dashboard" className="inline-flex h-8 w-8 items-center justify-center rounded-md border border-border bg-background/95 text-muted-foreground shadow-sm backdrop-blur-sm transition-colors hover:bg-muted hover:text-foreground">
            <RefreshCw className="h-3.5 w-3.5" />
          </button>
          {onClose && <button type="button" onClick={onClose} aria-label="Close dashboard" className="inline-flex h-8 w-8 items-center justify-center rounded-md border border-border bg-background/95 text-lg text-muted-foreground shadow-sm backdrop-blur-sm transition-colors hover:bg-muted hover:text-foreground">×</button>}
          <DashboardLayoutActions />
        </>} />}
        <div
          tabIndex={0}
          aria-label="Dashboard content"
          data-report-viewport
          className={`min-h-0 flex-1 overscroll-y-contain ${conversationPane ? 'flex flex-col overflow-hidden' : 'overflow-y-auto'} ${!loading && !error && !report && !defaultRelayDashboard ? 'flex flex-col' : ''}`}
        >
          <div className={`${conversationPane ? 'flex min-h-0 w-full flex-1 flex-col' : shellClass} ${!loading && !error && !report && !defaultRelayDashboard ? 'flex flex-1 items-center justify-center' : ''}`}>
            {loading && !report && <div className="flex min-h-64 items-center justify-center gap-2 text-sm text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" /> Loading dashboard…</div>}
            {error && <div className="m-3 rounded-md border border-destructive/30 bg-destructive/10 p-3 text-sm text-destructive">Failed to load report: {error}</div>}
            {!loading && !error && !report && defaultRelayDashboard && <Suspense fallback={<p className="p-6 text-sm text-muted-foreground">Opening Flow dashboard…</p>}><DefaultRelayDashboard workspacePath={workspacePath} refreshToken={refreshNonce} renderEmptyState={canCreate => <DashboardEmptyState description={emptyDescription} action={canCreate ? emptyAction : undefined} onRefresh={refresh} />} /></Suspense>}
            {!loading && !error && !report && !defaultRelayDashboard && (
              <DashboardEmptyState description={emptyDescription} action={emptyAction} onRefresh={refresh} />
            )}
            {!loading && report && !report.html && <div className="m-3 rounded-md border border-destructive/30 bg-destructive/10 p-3 text-sm text-destructive">Could not read {report.label}.</div>}
            {report?.html && (report.questions
              ? <DashboardQuestionConversation key={report.questions.resourceId} {...report.questions} conversationPane={conversationPane} sendChatMessage={sendChatMessage} html={report.html} title={report.label} documentPath={report.path} autoHeight refreshToken={refreshNonce} focusTarget={reportFocus} className="block w-full border-0" />
              : <ReportQuestionFrame key={report.path} html={report.html} title={report.label} documentPath={report.path} allowQuestions={allowContentQuestions} autoHeight refreshToken={refreshNonce} focusTarget={reportFocus} className="block w-full border-0" />)}
          </div>
        </div>
        {reportChat.tabId && !report?.questions && <DashboardChatDialog onClose={reportChat.close}><Suspense fallback={<p>Opening chat…</p>}><DashboardChatArea tabId={reportChat.tabId} compact hideHeader previousChatsWorkspacePath={workspacePath} landingContent={<div />} composerPlaceholder="Ask a question…" /></Suspense></DashboardChatDialog>}
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
          <ReportView workspacePath={workspacePath} onClose={onClose} dashboardConversation />
        </div>
      </div>
    </ModalPortal>
  )
}
