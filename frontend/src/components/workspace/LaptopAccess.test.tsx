// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

vi.hoisted(() => {
  const data = new Map<string, string>()
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: {
    getItem: (key: string) => data.get(key) ?? null,
    setItem: (key: string, value: string) => { data.set(key, value) },
    removeItem: (key: string) => { data.delete(key) }, clear: () => data.clear(),
  } })
})
import api, { agentApi } from '../../services/api'
import { CodeLocalFilesSettings } from '../../products/work/CodeFilesPanel'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let host: HTMLDivElement, root: Root
beforeEach(() => {
  host = document.createElement('div'); document.body.append(host); root = createRoot(host)
})
afterEach(async () => { await act(async () => root.unmount()); host.remove(); vi.restoreAllMocks() })

it('uses the shared panel to select and browse a Vault computer for this Goal', async () => {
  const get = vi.spyOn(api, 'get').mockImplementation(async url => ({ data: url === '/api/devices'
    ? { devices: [{ device_id: 'my-laptop', settings_managed: true }] }
    : url.includes('/folders?') ? { path: '/home/person', parent: '/home', folders: [] }
      : url.includes('/workflow/laptop?') ? { device_id: '', folders: [], commands: false } : {} }) as never)
  const put = vi.spyOn(api, 'put').mockImplementation(async (_url, data) => ({ data }) as never)
  await act(async () => root.render(<CodeLocalFilesSettings workflowPath="Workflow/invoices" />))
  expect(host.textContent).toContain('Connect a computer in My Vault')
  expect(host.textContent).not.toContain('curl -fsSL')
  const select = host.querySelector('select')!
  await act(async () => { select.value = 'my-laptop'; select.dispatchEvent(new Event('change', { bubbles: true })) })
  expect(put).toHaveBeenCalledWith('/api/workflow/laptop?workspace_path=Workflow%2Finvoices', { device_id: 'my-laptop', folders: [], commands: false }, { skipSessionContext: true })
  await act(async () => Array.from(host.querySelectorAll('button')).find(button => button.textContent === 'Add folder')!.click())
  expect(get).toHaveBeenCalledWith('/api/workflow/laptop/folders?workspace_path=Workflow%2Finvoices&device_id=my-laptop&path=', { skipSessionContext: true })
})
