// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'
import { installReportDocumentHost } from './reportHostRuntime'
import type { ReportDataApi } from './reportEmbedContext'

afterEach(() => { document.body.innerHTML = '' })

function mountReport(html: string) {
  const frame = document.createElement('iframe')
  document.body.appendChild(frame)
  frame.contentDocument!.body.innerHTML = html
  const dataApi = { workspacePath: '_users/m/Chats/Code/projects/p1' } as unknown as ReportDataApi
  installReportDocumentHost(frame.contentDocument!, { title: 't', dataApi, tokenSource: null, theme: 'light', dispatchData: false } as Parameters<typeof installReportDocumentHost>[1])
  return frame
}

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
