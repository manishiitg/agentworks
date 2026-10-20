// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { readReportTabSelection, restoreReportTabSelection } from './reportTabSelection'

vi.mock('./reportEmbedContext', () => ({ useReportDataApi: () => null, useReportChatWidgetFactory: () => undefined }))
vi.mock('./reportHostRuntime', () => ({
  applyReportTheme: vi.fn(),
  withReportBootstrap: (html: string) => html,
  installReportHost: (frame: HTMLIFrameElement) => {
    Object.assign(frame.contentWindow!, { report: {} })
  },
}))
import { HtmlReportFrame } from './HtmlWidgetFrame'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanups: (() => Promise<void>)[] = []
afterEach(async () => { for (const cleanup of cleanups.splice(0)) await cleanup() })

describe('report tab selection on refresh', () => {
  it('does not replay a tab-opening command on refresh, but accepts a new command', async () => {
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    cleanups.push(async () => { await act(async () => root.unmount()); host.remove() })
    const render = async (refreshToken: number, token: number) => {
      await act(async () => root.render(<HtmlReportFrame html="<p>Dashboard</p>" title="Report" className=""
        refreshToken={refreshToken} focusTarget={{ value: 'Email', token }} />))
    }
    await render(0, 1)
    const frame = host.querySelector('iframe')!
    await act(async () => frame.dispatchEvent(new Event('load')))
    const focus = vi.fn()
    frame.contentWindow!.addEventListener('report:focus', focus)
    // The initial request may still be waiting for the frame to load.
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 120)) })
    focus.mockClear()
    await render(1, 1)
    expect(host.querySelector('iframe')).toBe(frame)
    expect(focus).not.toHaveBeenCalled()
    await render(1, 2)
    expect(focus).toHaveBeenCalledTimes(1)
  })

  it.each([
    '<button id="tab-btn-companies" class="active">Companies</button>',
    '<button role="tab" aria-controls="companies" aria-selected="true">Companies</button>',
    '<button data-tab="companies" data-state="active">Companies</button>',
  ])('restores the selected tab in an updated document: %s', html => {
    const oldDoc = document.implementation.createHTMLDocument()
    oldDoc.body.innerHTML = html
    const selection = readReportTabSelection(oldDoc)!
    const newDoc = document.implementation.createHTMLDocument()
    newDoc.body.innerHTML = html
    const click = vi.fn()
    newDoc.querySelector('button')!.addEventListener('click', click)
    expect(restoreReportTabSelection(newDoc, selection)).toBe(true)
    expect(click).toHaveBeenCalledOnce()
  })

  it('does not click unrelated actions or substitute a tab when the saved tab was removed', () => {
    const doc = document.implementation.createHTMLDocument()
    doc.body.innerHTML = '<button id="approve" class="active">Approve</button>'
    const click = vi.fn()
    doc.querySelector('button')!.addEventListener('click', click)
    expect(readReportTabSelection(doc)).toBeNull()
    expect(restoreReportTabSelection(doc, { attribute: 'id', value: 'tab-btn-companies' })).toBe(false)
    expect(click).not.toHaveBeenCalled()
  })
})

it('fills the host pane only when the dashboard author requests viewport fit', async () => {
  const host = document.createElement('div')
  host.setAttribute('data-report-viewport', '')
  document.body.append(host)
  vi.spyOn(host, 'getBoundingClientRect').mockReturnValue({ top: 20, bottom: 720 } as DOMRect)
  const root = createRoot(host)
  cleanups.push(async () => { await act(async () => root.unmount()); host.remove(); vi.restoreAllMocks() })
  await act(async () => root.render(<HtmlReportFrame html="<p>Dashboard</p>" title="Fit report" className="" autoHeight />))
  const frame = host.querySelector('iframe')!
  vi.spyOn(frame, 'getBoundingClientRect').mockReturnValue({ top: 20 } as DOMRect)
  frame.contentDocument!.documentElement.setAttribute('data-report-fit', 'viewport')
  await act(async () => frame.dispatchEvent(new Event('load')))
  expect(frame.style.height).toBe(`${Math.floor(Math.min(window.innerHeight, 720) - 20)}px`)
  frame.contentDocument!.documentElement.removeAttribute('data-report-fit')
  frame.contentDocument!.body.innerHTML = '<section>Authored short page</section>'
  vi.spyOn(frame.contentDocument!.body.firstElementChild!, 'getBoundingClientRect').mockReturnValue({ bottom: 240 } as DOMRect)
  await act(async () => window.dispatchEvent(new Event('resize')))
  expect(frame.style.height).toBe('240px')
})
