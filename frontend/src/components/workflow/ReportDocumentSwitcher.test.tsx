// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'

const getPlannerFiles = vi.hoisted(() => vi.fn())
const getPlannerFileContent = vi.hoisted(() => vi.fn())
vi.mock('../../services/api', () => ({
  default: { post: vi.fn().mockResolvedValue({ data: { dashboards: [] } }) },
  agentApi: { getPlannerFiles, getPlannerFileContent },
  getApiBaseUrl: () => '',
  getAuthToken: () => null,
}))

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

import { ReportDocumentSwitcher } from './ReportDocumentSwitcher'

const WORKSPACE = 'Chats/Work/projects/p1'

const listing = (names: string[]) => names.map(name => ({ filepath: `${WORKSPACE}/db/reports/${name}`, type: 'file' }))
const html = (title: string) => ({ content: `<html><head><title>${title}</title></head><body></body></html>` })

function renderSwitcher() {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  const onOpen = vi.fn()
  return { host, root, onOpen }
}

function click(element: Element | null) {
  expect(element).not.toBeNull()
  act(() => {
    element!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  })
}

async function flushLoads() {
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)) })
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)) })
}

beforeEach(() => {
  getPlannerFiles.mockResolvedValue(listing(['index.html', 'investments.html']))
  getPlannerFileContent.mockImplementation(async (path: string) => {
    if (path.endsWith('index.html')) return html('Company')
    if (path.endsWith('investments.html')) return html('Investments')
    if (path.endsWith('customer-b-2627.html')) return html('customer-b 26-27')
    return { content: '' }
  })
})

afterEach(() => {
  vi.clearAllMocks()
})

describe('ReportDocumentSwitcher', () => {
  it('lists the known reports when the menu opens', async () => {
    const { host, root, onOpen } = renderSwitcher()
    try {
      await act(async () => root.render(<ReportDocumentSwitcher workspacePath={WORKSPACE} active={false} onOpen={onOpen} />))
      const button = host.querySelector('button[aria-label^="Dashboard"]')
      expect(button?.textContent).toContain('Company')
      expect(host.querySelector('[role="menu"]')).toBeNull()

      click(button)
      expect(onOpen).toHaveBeenCalledTimes(1)
      const menu = host.querySelector('[role="menu"]')
      expect(menu?.textContent).toContain('Company')
      expect(menu?.textContent).toContain('Investments')
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it('picks up an agent-added report when the menu is opened', async () => {
    const { host, root } = renderSwitcher()
    try {
      await act(async () => root.render(<ReportDocumentSwitcher workspacePath={WORKSPACE} active={false} onOpen={() => {}} />))
      // The agent adds a report while the menu is closed; the mounted catalog
      // is now stale.
      getPlannerFiles.mockResolvedValue(listing(['index.html', 'investments.html', 'customer-b-2627.html']))

      click(host.querySelector('button[aria-label^="Dashboard"]'))
      await flushLoads()

      expect(host.querySelector('[role="menu"]')?.textContent).toContain('customer-b 26-27')
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })
})
