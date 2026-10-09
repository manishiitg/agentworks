import { useCallback, useEffect, useState } from 'react'
import { flattenFiles, responseContent, responseFiles } from '../../utils/plannerFiles'
import { WORKFLOW_REPORT_REFRESH_EVENT } from './reportRefreshEvent'

export type ReportDocument = {
  id: string
  title: string
  path: string
  order: number
}

export type ReportDocumentCatalog = {
  documents: ReportDocument[]
  defaultPath: string
}

type ReportViewsManifest = {
  schema_version?: unknown
  default?: unknown
  views?: unknown
}

const DEFAULT_REPORT_PATH = 'db/reports/index.html'
const REPORT_SELECTION_EVENT = 'report-document-selection-changed'

function cleanReportPath(value: unknown): string {
  if (typeof value !== 'string') return ''
  let path = value.trim().replace(/^\/+/, '')
  if (path && !path.includes('/')) path = `db/reports/${path}`
  if (!path.startsWith('db/reports/') || !path.endsWith('.html') || path.split('/').includes('..')) return ''
  return path
}

function reportID(path: string): string {
  return path.replace(/^db\/reports\//, '').replace(/\.html$/i, '').replace(/[^a-z0-9]+/gi, '-').replace(/^-|-$/g, '') || 'dashboard'
}

function workspaceReportPath(filepath: string, workspacePath: string): string {
  const normalized = filepath.replace(/\\/g, '/')
  if (normalized.startsWith('db/reports/')) return normalized
  const workspaceRelative = normalized.replace(`${workspacePath.replace(/\/+$/, '')}/`, '')
  if (workspaceRelative.startsWith('db/reports/')) return workspaceRelative
  const marker = normalized.lastIndexOf('/db/reports/')
  return marker >= 0 ? normalized.slice(marker + 1) : normalized
}

function reportTitle(path: string, html?: string | null): string {
  const title = html?.match(/<title[^>]*>\s*([^<]+?)\s*<\/title>/i)?.[1]?.trim()
  if (title) return title
  const name = path.split('/').pop()?.replace(/\.html$/i, '') || 'Dashboard'
  if (name.toLowerCase() === 'index') return 'Dashboard'
  return name.split(/[-_]+/).filter(Boolean).map(part => part[0]?.toUpperCase() + part.slice(1)).join(' ')
}

export function buildReportDocumentCatalog(
  discoveredPaths: string[],
  manifestText = '',
  htmlByPath: Record<string, string | null | undefined> = {},
): ReportDocumentCatalog {
  const paths = [...new Set(discoveredPaths.map(cleanReportPath).filter(Boolean))]
  let manifest: ReportViewsManifest = {}
  try { manifest = JSON.parse(manifestText) as ReportViewsManifest } catch { /* optional manifest */ }

  const configured = Array.isArray(manifest.views) ? manifest.views : []
  const byPath = new Map<string, ReportDocument>()
  configured.forEach((value, index) => {
    if (!value || typeof value !== 'object') return
    const raw = value as Record<string, unknown>
    const path = cleanReportPath(raw.path)
    if (!path || !paths.includes(path) || byPath.has(path)) return
    const id = typeof raw.id === 'string' && raw.id.trim() ? raw.id.trim() : reportID(path)
    const title = typeof raw.title === 'string' && raw.title.trim() ? raw.title.trim() : reportTitle(path, htmlByPath[path])
    const order = typeof raw.order === 'number' && Number.isFinite(raw.order) ? raw.order : index
    byPath.set(path, { id, title, path, order })
  })

  paths.forEach((path, index) => {
    if (!byPath.has(path)) byPath.set(path, {
      id: reportID(path),
      title: reportTitle(path, htmlByPath[path]),
      path,
      order: path === DEFAULT_REPORT_PATH ? -1 : configured.length + index,
    })
  })

  const documents = [...byPath.values()].sort((a, b) => a.order - b.order || a.title.localeCompare(b.title))
  const configuredDefault = typeof manifest.default === 'string'
    ? documents.find(document => document.id === manifest.default || document.path === cleanReportPath(manifest.default))?.path
    : undefined
  const defaultPath = configuredDefault
    || documents.find(document => document.path === DEFAULT_REPORT_PATH)?.path
    || documents[0]?.path
    || DEFAULT_REPORT_PATH
  return { documents, defaultPath }
}

export async function loadReportDocumentCatalog(workspacePath: string): Promise<ReportDocumentCatalog> {
  // Keep the pure catalog helpers usable without initializing the full API/store
  // graph (notably in tests and lightweight toolbar renders).
  const { agentApi, default: api } = await import('../../services/api')
  const reportRoot = `${workspacePath.replace(/\/+$/, '')}/db/reports`
  let paths: string[] = []
  try {
    const listing = await agentApi.getPlannerFiles(reportRoot, -1, 3)
    paths = flattenFiles(responseFiles(listing))
      .filter(file => file.type !== 'folder')
      .map(file => workspaceReportPath(file.filepath, workspacePath))
      .filter(path => cleanReportPath(path) && !path.startsWith('db/reports/preview/') && !path.startsWith('db/reports/managed/'))
  } catch { /* a project without reports is valid */ }

  const managedTitles: Record<string, string> = {}
  try {
    let offset = 0
    let hasMore = true
    while (hasMore) {
      const { data } = await api.post<{ dashboards?: { document_path: string; title: string; managed: boolean; published_revision?: string }[]; has_more?: boolean }>('/api/dashboards', { workspace: workspacePath, action: 'list', limit: 200, offset })
      const dashboards = data.dashboards || []
      for (const dashboard of dashboards) {
        if (dashboard.managed && !dashboard.published_revision) continue
        if (!paths.includes(dashboard.document_path)) paths.push(dashboard.document_path)
        managedTitles[dashboard.document_path] = dashboard.title
      }
      offset += dashboards.length
      hasMore = Boolean(data.has_more) && dashboards.length > 0 && offset <= 10000
    }
  } catch { /* existing deployments without the service retain their HTML catalog */ }

  let manifestText = ''
  try {
    manifestText = responseContent(await agentApi.getPlannerFileContent(`${reportRoot}/views.json`))?.content || ''
  } catch { /* views.json is optional */ }

  // A manifest is an explicit report catalog. Seed valid declared paths before
  // loading titles so a shallow/stale directory listing cannot hide a view.
  try {
    const manifest = JSON.parse(manifestText) as ReportViewsManifest
    if (Array.isArray(manifest.views)) {
      for (const value of manifest.views) {
        if (!value || typeof value !== 'object') continue
        const declared = cleanReportPath((value as Record<string, unknown>).path)
        if (declared && !paths.includes(declared)) paths.push(declared)
      }
    }
  } catch { /* malformed optional metadata is ignored */ }

  const htmlEntries = await Promise.all(paths.map(async path => {
    try {
      const content = managedTitles[path] ? `<title>${managedTitles[path]}</title>`
        : responseContent(await agentApi.getPlannerFileContent(`${workspacePath.replace(/\/+$/, '')}/${path}`))?.content
      return [path, content || null] as const
    } catch {
      return [path, null] as const
    }
  }))
  return buildReportDocumentCatalog(paths, manifestText, Object.fromEntries(htmlEntries))
}

function selectionKey(workspacePath: string): string {
  return `report-document:${workspacePath.replace(/\/+$/, '')}`
}

export function readReportDocumentSelection(workspacePath: string): string {
  return readStoredReportDocumentSelection(workspacePath) || DEFAULT_REPORT_PATH
}

export function readStoredReportDocumentSelection(workspacePath: string): string {
  try { return cleanReportPath(window.localStorage.getItem(selectionKey(workspacePath)) || '') } catch { return '' }
}

export function selectReportDocument(workspacePath: string, path: string): void {
  const clean = cleanReportPath(path) || DEFAULT_REPORT_PATH
  try { window.localStorage.setItem(selectionKey(workspacePath), clean) } catch { /* optional persistence */ }
  window.dispatchEvent(new CustomEvent(REPORT_SELECTION_EVENT, { detail: { workspacePath, path: clean } }))
}

export function useReportDocuments(workspacePath: string): ReportDocumentCatalog & { loading: boolean; refresh: () => void } {
  const [catalog, setCatalog] = useState<ReportDocumentCatalog>({ documents: [], defaultPath: DEFAULT_REPORT_PATH })
  const [loading, setLoading] = useState(true)
  const [nonce, setNonce] = useState(0)
  const refresh = useCallback(() => setNonce(value => value + 1), [])
  useEffect(() => {
    let cancelled = false
    setLoading(true)
    void loadReportDocumentCatalog(workspacePath).then(next => {
      if (!cancelled) setCatalog(next)
    }).finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [nonce, workspacePath])
  useEffect(() => {
    window.addEventListener(WORKFLOW_REPORT_REFRESH_EVENT, refresh)
    return () => window.removeEventListener(WORKFLOW_REPORT_REFRESH_EVENT, refresh)
  }, [refresh])
  return { ...catalog, loading, refresh }
}

export function useSelectedReportDocument(workspacePath: string, explicitPath?: string): string {
  const [selected, setSelected] = useState(() => explicitPath || readReportDocumentSelection(workspacePath))
  useEffect(() => setSelected(explicitPath || readReportDocumentSelection(workspacePath)), [explicitPath, workspacePath])
  useEffect(() => {
    if (explicitPath) return
    const onSelection = (event: Event) => {
      const detail = (event as CustomEvent<{ workspacePath?: string; path?: string }>).detail
      if (detail?.workspacePath === workspacePath && detail.path) setSelected(detail.path)
    }
    window.addEventListener(REPORT_SELECTION_EVENT, onSelection)
    return () => window.removeEventListener(REPORT_SELECTION_EVENT, onSelection)
  }, [explicitPath, workspacePath])
  return selected
}
