// @vitest-environment happy-dom
import { describe, expect, it, vi } from 'vitest'
import api from '../../services/api'
import { openTeamOnlineReport } from './teamOnlineReport'

vi.mock('../../services/api', () => ({ default: { post: vi.fn() } }))
import { buildReportDocumentCatalog } from './reportDocuments'

describe('report document catalog', () => {
  it('keeps index as the backward-compatible default and discovers other HTML reports', () => {
    const catalog = buildReportDocumentCatalog(
      ['db/reports/tasks.html', 'db/reports/index.html', 'db/reports/notes.txt'],
      '',
      {
        'db/reports/index.html': '<title>Overview</title>',
        'db/reports/tasks.html': '<title>Tasks board</title>',
      },
    )
    expect(catalog.defaultPath).toBe('db/reports/index.html')
    expect(catalog.documents.map(document => [document.path, document.title])).toEqual([
      ['db/reports/index.html', 'Overview'],
      ['db/reports/tasks.html', 'Tasks board'],
    ])
  })

  it('uses views.json for titles, ordering and the default without trusting missing paths', () => {
    const catalog = buildReportDocumentCatalog(
      ['db/reports/index.html', 'db/reports/finance.html'],
      JSON.stringify({
        schema_version: 1,
        default: 'finance',
        views: [
          { id: 'finance', title: 'Finance', path: 'finance.html', order: 1 },
          { id: 'missing', title: 'Missing', path: 'missing.html', order: 0 },
          { id: 'overview', title: 'Overview', path: 'db/reports/index.html', order: 2 },
        ],
      }),
    )
    expect(catalog.defaultPath).toBe('db/reports/finance.html')
    expect(catalog.documents.map(document => document.id)).toEqual(['finance', 'overview'])
  })

  it('rejects traversal and non-HTML entries', () => {
    const catalog = buildReportDocumentCatalog(['db/reports/../product.json', 'db/reports/data.json'])
    expect(catalog.documents).toEqual([])
    expect(catalog.defaultPath).toBe('db/reports/index.html')
  })
})

// PLAT-746-1023: source Open must change the entire runtime destination, not
// load Team HTML while leaving write buttons bound to the Mac database.
it('opens accepted Team reports on their saved server and never falls back on refusal', async () => {
  const open = vi.fn().mockResolvedValue(undefined)
  const original = window.electronAPI
  Object.assign(window, { electronAPI: { openDesktopConnection: open } })
  const request = vi.mocked(api.post)
  try {
    request.mockResolvedValue({ data: { found: true, connection: 'company', short_path: '/d/23456789ab' } })
    expect(await openTeamOnlineReport('Crew/source', 'db/reports/managed/sales/index.html')).toBe(true)
    expect(open).toHaveBeenCalledWith('company', '/d/23456789ab')
    open.mockClear()
    // Mapped rows resolve the chosen Team through its saved connection rather
    // than loading source HTML or choosing an arbitrary Team for the source.
    request.mockResolvedValue({ data: { found: true, short_path: '/d/23456789ab' } })
    await openTeamOnlineReport('Crew/home', 'db/reports/managed/online-sales/index.html', () => true, undefined, { team_project_id: 'team', source_project_id: 'mac', item_kind: 'dashboard', dashboard_id: 'sales', connection: 'company' })
    expect(request).toHaveBeenLastCalledWith('/api/connections/company/api/dashboards', expect.objectContaining({ team_project_id: 'team', source_project_id: 'mac', dashboard_id: 'sales' }))
    expect(open).toHaveBeenCalledWith('company', '/d/23456789ab')
    open.mockClear()
    request.mockClear()
    expect(await openTeamOnlineReport('Crew/source', `db/reports/managed/sales/${'a'.repeat(64)}/index.html`)).toBe(false)
    expect(request).not.toHaveBeenCalled() // authoring previews stay source-local
    request.mockResolvedValue({ data: { found: false } })
    expect(await openTeamOnlineReport('Crew/source', 'db/reports/managed/unmoved/index.html')).toBe(false)
    const notice = vi.fn()
    request.mockResolvedValue({ data: { found: false, notice: "Couldn't reach your Team's server, so this is the copy on this computer." } })
    expect(await openTeamOnlineReport('Crew/source', 'db/reports/managed/sales/index.html', () => true, notice)).toBe(false)
    expect(notice).toHaveBeenCalledWith("Couldn't reach your Team's server, so this is the copy on this computer.")
    expect(open).not.toHaveBeenCalled()
    request.mockRejectedValue(new Error('Team access ended'))
    await expect(openTeamOnlineReport('Crew/source', 'db/reports/managed/sales/index.html')).rejects.toThrow('Team access ended')
    expect(open).not.toHaveBeenCalled()
    request.mockResolvedValue({ data: { found: true, connection: 'company', short_path: 'https://example.com/steal' } })
    await expect(openTeamOnlineReport('Crew/source', 'db/reports/managed/sales/index.html')).rejects.toThrow('unavailable')
    request.mockResolvedValue({ data: { found: true, connection: 'company', short_path: '/d/23456789ab' } })
    await openTeamOnlineReport('Crew/source', 'db/reports/managed/sales/index.html', () => false)
    expect(open).not.toHaveBeenCalled() // stale Open may not navigate a newer selection
  } finally {
    Object.assign(window, { electronAPI: original })
    request.mockReset()
  }
})
