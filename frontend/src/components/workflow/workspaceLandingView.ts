import { agentApi } from '../../services/api'
import { responseContent } from '../../utils/plannerFiles'
import { loadReportDocumentCatalog } from './reportDocuments'

export type WorkspaceLandingView = 'dashboard' | 'plan' | 'identity'

async function hasDashboard(workspacePath: string): Promise<boolean> {
  const catalog = await loadReportDocumentCatalog(workspacePath)
  for (const document of catalog.documents) {
    try {
      const response = await agentApi.getPlannerFileContent(`${workspacePath.replace(/\/+$/, '')}/${document.path}`)
      if (response?.success && responseContent(response)?.content) return true
    } catch { /* A stale catalog entry is not a usable dashboard. */ }
  }
  return false
}

/** Choose an unsaved workspace's first view from content that already exists. */
export async function loadWorkspaceLandingView(workspacePath: string, options: { dashboardAllowed?: boolean } = {}): Promise<WorkspaceLandingView> {
  const [dashboard, plan] = await Promise.all([
    options.dashboardAllowed === false ? Promise.resolve(false) : hasDashboard(workspacePath).catch(() => false),
    agentApi.getPlannerFileContent(`${workspacePath.replace(/\/+$/, '')}/planning/plan.json`)
      .then(response => {
        if (!response?.success) return false
        const content = responseContent(response)?.content
        if (!content) return false
        const parsed: unknown = JSON.parse(content)
        return Boolean(parsed && typeof parsed === 'object' && 'steps' in parsed && Array.isArray(parsed.steps) && parsed.steps.length > 0)
      })
      .catch(() => false),
  ])
  return dashboard ? 'dashboard' : plan ? 'plan' : 'identity'
}
