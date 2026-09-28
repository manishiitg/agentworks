import { beforeEach, describe, expect, it, vi } from 'vitest'

const { loadCatalog, readFile } = vi.hoisted(() => ({
  loadCatalog: vi.fn(),
  readFile: vi.fn(),
}))

vi.mock('./reportDocuments', () => ({ loadReportDocumentCatalog: loadCatalog }))
vi.mock('../../services/api', () => ({ agentApi: { getPlannerFileContent: readFile } }))

import { loadWorkspaceLandingView } from './workspaceLandingView'

describe('workspace landing view', () => {
  beforeEach(() => {
    loadCatalog.mockReset()
    readFile.mockReset()
    loadCatalog.mockResolvedValue({ documents: [], defaultPath: 'db/reports/index.html' })
    readFile.mockRejectedValue(new Error('not found'))
  })

  it('opens an existing dashboard before a plan', async () => {
    loadCatalog.mockResolvedValue({ documents: [{ path: 'db/reports/index.html' }] })
    readFile.mockResolvedValue({ success: true, data: { content: '{"steps":[{"id":"one"}]}' } })
    expect(await loadWorkspaceLandingView('Workflow/example')).toBe('dashboard')
    expect(await loadWorkspaceLandingView('Workflow/example', { dashboardAllowed: false })).toBe('plan')
  })

  it('opens the plan when no dashboard exists', async () => {
    readFile.mockResolvedValue({ success: true, data: { content: '{"steps":[{"id":"one"}]}' } })
    expect(await loadWorkspaceLandingView('Workflow/example')).toBe('plan')
    expect(readFile).toHaveBeenCalledWith('Workflow/example/planning/plan.json')
  })

  it('ignores a catalog entry whose dashboard file cannot be read', async () => {
    loadCatalog.mockResolvedValue({ documents: [{ path: 'db/reports/missing.html' }] })
    readFile.mockImplementation((path: string) => path.endsWith('/planning/plan.json')
      ? Promise.resolve({ success: true, data: { content: '{"steps":[{"id":"one"}]}' } })
      : Promise.reject(new Error('not found')))
    expect(await loadWorkspaceLandingView('Workflow/example')).toBe('plan')
  })

  it('opens Identity for an empty, invalid, or unavailable plan', async () => {
    readFile.mockResolvedValue({ success: true, data: { content: '{"steps":[]}' } })
    expect(await loadWorkspaceLandingView('Workflow/example')).toBe('identity')
    readFile.mockResolvedValue({ success: true, data: { content: 'invalid' } })
    expect(await loadWorkspaceLandingView('Workflow/example')).toBe('identity')
    readFile.mockRejectedValue(new Error('network error'))
    expect(await loadWorkspaceLandingView('Workflow/example')).toBe('identity')
  })
})
