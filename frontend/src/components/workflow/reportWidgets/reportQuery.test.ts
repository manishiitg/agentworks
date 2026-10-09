// @vitest-environment happy-dom
import { expect, it, vi } from 'vitest'
import { createReportQuery } from './reportQuery'
import { installReportHost, withReportBootstrap } from './reportHostRuntime'
import type { ReportDataApi } from './reportEmbedContext'

it('keeps bindings through the authored HTML bootstrap, host and shared query transport', async () => {
  const value = "a' OR 1=1 --"
  const post = vi.fn(async () => ({ success: true, data: { rows: [{ name: value }] } }))
  const frame = document.createElement('iframe')
  document.body.replaceChildren(frame)
  const doc = frame.contentDocument!, win = frame.contentWindow!
  const stub = withReportBootstrap('<div></div>').match(/<script>([\s\S]*?)<\/script>/)![1]
  new Function('window', 'document', stub)(win, doc)
  const reportWindow = win as unknown as { report: { query: ReportDataApi['query'] } }
  const pending = reportWindow.report.query('SELECT name FROM items WHERE name = ?', [value])
  const data = { workspacePath: 'Workflow/relay', query: createReportQuery('Workflow/relay', post) } as ReportDataApi
  installReportHost(frame, { title: 'Bindings', dataApi: data, theme: 'light', tokenSource: null, dispatchData: true })
  await expect(pending).resolves.toEqual([{ name: value }])
  expect(post).toHaveBeenCalledWith({ workspace: 'Workflow/relay', sql: 'SELECT name FROM items WHERE name = ?', params: [value] })
  frame.remove()
})

it('preserves no-parameter queries and surfaces server query failures', async () => {
  const post = vi.fn(async () => ({ success: true, data: { rows: [{ n: 1 }] } }))
  await expect(createReportQuery('Crew/project', post)('SELECT 1 AS n')).resolves.toEqual([{ n: 1 }])
  expect(post).toHaveBeenCalledWith({ workspace: 'Crew/project', sql: 'SELECT 1 AS n', params: undefined })
  await expect(createReportQuery('Crew/project', async () => ({ success: false, error: 'denied' }))('SELECT 1')).rejects.toThrow('denied')
})
