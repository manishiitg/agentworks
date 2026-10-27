import api from './api'
import { sharedReportLink } from '../utils/sharedLinks'
import { currentProductRoute } from '../utils/productRoutes'
import { openAppProduct } from '../utils/appNavigation'
import { selectReportDocument } from '../components/workflow/reportDocuments'

export type DashboardDirectoryEntry = {
  dashboard_id: string
  project_id?: string
  product: 'crew' | 'workflow' | 'relay'
  title?: string
  workspace: string
  project_title?: string
  document_path: string
  short_path?: string
  shared_with_me: boolean
}

type DashboardPage = {
  dashboards: DashboardDirectoryEntry[]
  next_offset: number
  has_more: boolean
  unavailable_projects?: string[]
}

export function dashboardDisplayName(row: DashboardDirectoryEntry): string {
  const title = row.title?.trim()
  return (!title || /^index(?:\.html?)?$/i.test(title)) ? row.project_title?.trim() || 'Dashboard' : title
}

export function dashboardDirectoryLink(row: DashboardDirectoryEntry): string {
  return row.short_path || sharedReportLink(window.location.origin, row.workspace, row.document_path)
}

/** The website directory and Ctrl+K use the same current-access inventory.
 * No global cache: rows must not survive a server/account switch. */
export async function loadDashboardDirectory(signal?: AbortSignal): Promise<{ dashboards: DashboardDirectoryEntry[]; partial: boolean }> {
  const dashboards: DashboardDirectoryEntry[] = []
  let offset = 0
  let partial = false
  for (;;) {
    const { data } = await api.post<DashboardPage>('/api/dashboards', { action: 'list', published_only: true, limit: 200, offset }, { signal })
    signal?.throwIfAborted()
    dashboards.push(...data.dashboards)
    partial ||= Boolean(data.unavailable_projects?.length)
    if (!data.has_more) return { dashboards, partial }
    if (data.next_offset <= offset) throw new Error('Dashboard list did not advance')
    offset = data.next_offset
  }
}

/** Only report links clicked inside a product workspace use its dashboard panel.
 * The standalone report route remains the entry point for shared links. */
export function dashboardWorkspaceLink(href: string): { workspace: string; document: string } | null {
  if (!currentProductRoute()) return null
  try {
    const url = new URL(href, window.location.href)
    if (url.origin !== window.location.origin || url.pathname !== '/report') return null
    const encoded = url.searchParams.get('path')
    if (!encoded) return null
    const workspace = new TextDecoder('utf-8', { fatal: true }).decode(Uint8Array.from(atob(encoded), ch => ch.charCodeAt(0)))
    return { workspace, document: url.searchParams.get('document') || 'db/reports/index.html' }
  } catch { return null }
}

export async function openDashboardWorkspaceLink(href: string): Promise<boolean> {
  const target = dashboardWorkspaceLink(href)
  if (!target) return false
  try {
    let offset = 0
    for (;;) {
      // The server resolves the project and checks today's access, including
      // shared Run access and owner-only drafts. Never derive a project ID/role from a folder name.
      const { data } = await api.post<DashboardPage>('/api/dashboards', {
        action: 'list', workspace: target.workspace, limit: 200, offset,
      })
      const row = data.dashboards.find(row => row.document_path === target.document && row.project_id)
      if (row?.project_id) {
        const surface = row.product === 'crew' ? 'work' : row.product === 'workflow' ? 'agentworks' : row.product === 'relay' ? 'relays' : null
        if (!surface) return false
        selectReportDocument(target.workspace, row.document_path)
        openAppProduct({ surface, id: row.project_id, view: surface === 'work' ? 'dashboard' : 'report' })
        return true
      }
      if (!data.has_more || data.next_offset <= offset) return false
      offset = data.next_offset
    }
  } catch { return false }
}
