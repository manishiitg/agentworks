// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { GatewayPIIPanel } from './GatewayPIIPanel'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const BASE = 'http://127.0.0.1:18161'
const response = (body: unknown) => ({ ok: true, status: 200, json: async () => body } as Response)

describe('GatewayPIIPanel', () => {
  let container: HTMLDivElement | null = null
  afterEach(() => { container?.remove(); container = null; vi.unstubAllGlobals() })

  it('shows defaults and tests a sample without saving it', async () => {
    const fetchMock = vi.fn((url: string, init?: RequestInit) => {
      if (url.endsWith('/api/admin/pii/rules')) return Promise.resolve(response({ rules: [] }))
      if (url.endsWith('/api/admin/pii/reviews')) return Promise.resolve(response({ reviews: [] }))
      if (url.endsWith('/api/admin/groups')) return Promise.resolve(response({ groups: [] }))
      if (url.endsWith('/api/admin/connectors')) return Promise.resolve(response({ connectors: [] }))
      if (url.endsWith('/api/admin/tools')) return Promise.resolve(response({ tools: [] }))
      if (url.endsWith('/api/admin/pii/test') && init?.method === 'POST') return Promise.resolve(response({ decision: { action: 'mask', data_types: ['email'], match_count: 1 }, masked_preview: '[REDACTED:email]' }))
      return Promise.resolve(response({ error: 'unknown route' }))
    })
    vi.stubGlobal('fetch', fetchMock)
    container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => { root.render(<GatewayPIIPanel base={BASE} />) })
    await act(async () => {})
    expect(container.textContent).toContain('The default protection is active')
    const sample = container.querySelector('[aria-label="PII sample"]') as HTMLTextAreaElement
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(sample, 'alice@example.com')
      sample.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => { ([...container!.querySelectorAll('button')].find(button => button.textContent === 'Test policy') as HTMLButtonElement).click() })
    expect(fetchMock).toHaveBeenCalledWith(`${BASE}/api/admin/pii/test`, expect.objectContaining({ method: 'POST' }))
    expect(container.textContent).toContain('[REDACTED:email]')
  })
})
