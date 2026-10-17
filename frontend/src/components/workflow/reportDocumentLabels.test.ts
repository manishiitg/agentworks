import { expect, it } from 'vitest'
import { reportDocumentLabel } from './reportDocumentLabels'

it('preserves unique report titles and distinguishes reused titles by their real files', () => {
  const documents = [
    { id: 'one', title: 'Tax report', path: 'db/reports/tax_2025.html', order: 0 },
    { id: 'two', title: 'Tax report', path: 'db/reports/tax_2026.html', order: 1 },
    { id: 'three', title: 'Investments', path: 'db/reports/portfolio.html', order: 2 },
  ]
  expect(documents.map(document => reportDocumentLabel(document, documents))).toEqual(['tax 2025', 'tax 2026', 'Investments'])
  expect(documents[0].title).toBe('Tax report')
})

it('includes the folder when duplicate titles also share a filename', () => {
  const documents = [
    { id: 'one', title: 'Report', path: 'db/reports/2025/report.html', order: 0 },
    { id: 'two', title: 'Report', path: 'db/reports/2026/report.html', order: 1 },
  ]
  expect(documents.map(document => reportDocumentLabel(document, documents))).toEqual(['2025/report', '2026/report'])
})
