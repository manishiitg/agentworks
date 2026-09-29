// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'
import { installReportHost } from './reportHostRuntime'
import type { ReportDataApi } from './reportEmbedContext'

afterEach(() => { document.body.innerHTML = '' })

function mountReport(html: string) {
  const frame = document.createElement('iframe')
  document.body.appendChild(frame)
  frame.contentDocument!.body.innerHTML = html
  const dataApi = { workspacePath: '_users/m/Chats/Code/projects/p1' } as unknown as ReportDataApi
  installReportHost(frame, { title: 't', dataApi, tokenSource: null, theme: 'light', dispatchData: false } as Parameters<typeof installReportHost>[1])
  return frame
}

it('a relative link to another dashboard view switches views instead of navigating the frame', () => {
  const frame = mountReport('<a id="l" href="tasks.html">Open tasks</a>')
  const seen: unknown[] = []
  const onSelect = (e: Event) => seen.push((e as CustomEvent).detail)
  window.addEventListener('report-document-selection-changed', onSelect)
  const click = new MouseEvent('click', { bubbles: true, cancelable: true })
  frame.contentDocument!.getElementById('l')!.dispatchEvent(click)
  window.removeEventListener('report-document-selection-changed', onSelect)
  expect(click.defaultPrevented).toBe(true)
  expect(seen).toEqual([{ workspacePath: '_users/m/Chats/Code/projects/p1', path: 'db/reports/tasks.html' }])
})

it('never lets any other relative link navigate the frame', () => {
  const frame = mountReport('<a id="l" href="/anthropics/whatever">x</a>')
  const click = new MouseEvent('click', { bubbles: true, cancelable: true })
  frame.contentDocument!.getElementById('l')!.dispatchEvent(click)
  expect(click.defaultPrevented).toBe(true)
})

it('does not show the benign ResizeObserver warning as a render failure', () => {
  const frame = mountReport('<p>ok</p>')
  vi.spyOn(console, 'error').mockImplementation(() => {})
  frame.contentWindow!.dispatchEvent(new ErrorEvent('error', { message: 'ResizeObserver loop completed with undelivered notifications.' }))
  expect(frame.contentDocument!.querySelector('[data-report-error]')).toBeNull()
})
