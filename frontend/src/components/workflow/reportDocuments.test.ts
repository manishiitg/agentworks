// @vitest-environment happy-dom
import { describe, expect, it } from 'vitest'
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
