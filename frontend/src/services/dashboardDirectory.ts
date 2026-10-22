import api from './api'
import { sharedReportLink } from '../utils/sharedLinks'

export type DashboardDirectoryEntry = {
  dashboard_id: string
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
