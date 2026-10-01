// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { GatewayGroupPolicyReview } from './GatewayGroupPolicyReview'
import type { GatewayAccessPackage } from './gatewayAdminApi'

vi.mock('../../services/api', () => ({ getApiBaseUrl: () => 'http://localhost:18745', getAuthToken: () => 'test-jwt' }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let root: Root
let container: HTMLDivElement
afterEach(async () => {
  if (root) await act(async () => root.unmount())
  container?.remove()
  vi.unstubAllGlobals()
})

it('reviews only the selected group, invalidates sample results, and publishes the reviewed version', async () => {
  const draft: GatewayAccessPackage = { id: 'p-eng', workspace_id: 'w', group_id: 'eng', name: 'Engineering policy', status: 'draft', version: 4,
    rules: [{ public_name: 'files__read', fingerprint: 'f1', conditions: [{ path: '/path', op: 'equals', value: '/docs' }] }] }
  const other = { ...draft, id: 'p-sales', group_id: 'sales', name: 'Sales policy' }
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    let body: unknown
    if (url.endsWith('/packages')) body = { packages: [draft, other] }
    else if (url.endsWith('/history')) body = { events: [
      { package_id: 'p-eng', action: 'draft_saved', version: 4, actor: 'eng-admin' },
      { package_id: 'p-sales', action: 'draft_saved', version: 4, actor: 'sales-admin' },
    ] }
    else if (url.endsWith('/simulate')) body = { allowed: true }
    else if (url.endsWith('/publish')) { expect(JSON.parse(init!.body as string)).toEqual({ version: 4 }); draft.status = 'published'; body = draft }
    else throw new Error(`Unexpected request: ${url}`)
    return { ok: true, status: 200, json: async () => body } as Response
  })
  vi.stubGlobal('fetch', fetchMock)
  container = document.createElement('div'); document.body.appendChild(container)
  root = createRoot(container)
  const onChanged = vi.fn()
  await act(async () => root.render(<GatewayGroupPolicyReview base="http://localhost:18745" groupId="eng" chatBusy={false} onChanged={onChanged} />))
  expect(container.textContent).toContain('Engineering policy')
  expect(container.textContent).not.toContain('Sales policy')
  expect(container.textContent).not.toContain('sales-admin')
  const tool = container.querySelector('#group-sim-tool') as HTMLSelectElement
  await act(async () => { tool.value = 'files__read'; tool.dispatchEvent(new Event('change', { bubbles: true })) })
  const button = (label: string) => [...container.querySelectorAll('button')].find(b => b.textContent === label)!
  await act(async () => button('Simulate').click())
  expect(container.textContent).toContain('Allowed by this draft')
  const sample = container.querySelector('textarea')!
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(sample, '{"path":"/other"}')
    sample.dispatchEvent(new Event('input', { bubbles: true }))
  })
  expect(container.textContent).not.toContain('Allowed by this draft')
  await act(async () => button('Publish permissions').click())
  expect(onChanged).toHaveBeenCalledOnce()
  expect(container.textContent).toContain('Revoke permissions')
})
