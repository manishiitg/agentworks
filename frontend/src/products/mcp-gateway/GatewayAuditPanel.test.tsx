// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { GatewayAuditPanel } from './GatewayAuditPanel'

vi.mock('../../services/api', () => ({ getApiBaseUrl: () => 'http://product', getAuthToken: () => 'test-token' }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const event = { ID: 'e1', Timestamp: '2026-10-04T07:29:32Z', UserID: 'default', GroupIDs: ['platform'], ClientID: 'agentworks-vault-builder', ConnectorID: 'c1', PublicName: 'notion_c1__notion-fetch', UpstreamName: 'notion-fetch', Decision: 'allow', Outcome: 'ok', DurationMs: 763 }

describe('Audit readable rows and complete details', () => {
  let host: HTMLDivElement, root: Root
  afterEach(async () => { if (root) await act(async () => root.unmount()); host?.remove(); vi.unstubAllGlobals() })
  async function render(events = [event]) {
    const fetchMock = vi.fn(async (url: string) => {
      let data: unknown
      if (url.includes('/audit/settings')) data = { enabled: true, provider: 'sqlite', write_mode: 'async', retention_seconds: 86400 }
      else if (url.includes('/audit?')) data = { events }
      else if (url.endsWith('/connectors')) data = { connectors: [{ ID: 'c1', Label: 'Notion · Team' }] }
      else if (url.endsWith('/users')) data = { users: [{ ID: 'default' }] }
      else if (url.endsWith('/groups')) data = { groups: [{ ID: 'platform', Name: 'Platform' }] }
      else if (url.endsWith('/tools')) data = { tools: [] }
      else throw new Error(`Unexpected read ${url}`)
      return { ok: true, status: 200, json: async () => data } as Response
    })
    vi.stubGlobal('fetch', fetchMock)
    host = document.createElement('div'); document.body.append(host); root = createRoot(host)
    await act(async () => root.render(<GatewayAuditPanel base="http://product" />))
    await act(async () => {})
    return fetchMock
  }

  it('shows friendly names and results while retaining actor and original IDs in details', async () => {
    await render()
    expect(host.querySelector('tbody')!.textContent).toContain('notion-fetch')
    expect(host.querySelector('tbody')!.textContent).toContain('Notion · Team')
    expect(host.querySelector('tbody')!.textContent).toContain('Success')
    expect(host.querySelector('tbody')!.textContent).not.toContain(event.PublicName)
    expect(host.querySelector('thead')!.textContent).not.toContain('Decision')
    await act(async () => { (host.querySelector('[aria-label^="View details"]') as HTMLButtonElement).click() })
    expect(host.textContent).toContain('Vault builder')
    expect(host.textContent).toContain('Platform')
    expect(host.textContent).toContain(event.PublicName)
    expect(host.textContent).toContain('Allowed')
    expect(host.textContent).toContain('Not recorded for this call.')
  })

  it('keeps blocked error details and does not invent an individual for a group-key call', async () => {
    await render([{ ...event, UserID: '', ClientID: 'api-key:team', Decision: 'deny', Outcome: 'denied', ErrorText: 'arguments do not match approved tool schema' } as typeof event])
    expect(host.querySelector('tbody')!.textContent).toContain('Blocked')
    expect(host.querySelector('tbody')!.textContent).toContain('Group API key')
    await act(async () => { (host.querySelector('[aria-label^="View details"]') as HTMLButtonElement).click() })
    expect(host.textContent).toContain('no individual user was identified')
    expect(host.textContent).toContain('arguments do not match approved tool schema')
    expect(host.textContent).toContain('Tool was not called.')
  })

  it('shows recorded input/output JSON and explicit truncation inside call details', async () => {
    await render([{ ...event, Input: { project: 'team-alpha' }, Output: { content: [{ type: 'text', text: 'result' }] }, OutputTruncated: true } as typeof event])
    await act(async () => { (host.querySelector('[aria-label^="View details"]') as HTMLButtonElement).click() })
    expect(host.querySelectorAll('#audit-event-e1 details')).toHaveLength(2)
    expect(host.textContent).toContain('Input arguments')
    expect(host.textContent).toContain('Tool output')
    expect(host.textContent).toContain('Truncated')
    expect(host.querySelectorAll('pre')[0].textContent).toContain('"project": "team-alpha"')
    expect(host.querySelectorAll('pre')[1].textContent).toContain('"text": "result"')
  })

  it('sends selected user and result filters to the audit API', async () => {
    const fetchMock = await render()
    await act(async () => {
      const select = host.querySelector('[aria-label="User"]') as HTMLSelectElement
      select.value = 'default'; select.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(fetchMock.mock.calls.some(([url]) => url.includes('/audit?') && url.includes('user=default'))).toBe(true)
    await act(async () => {
      const select = host.querySelector('[aria-label="Result"]') as HTMLSelectElement
      select.value = 'denied'; select.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(fetchMock.mock.calls.some(([url]) => url.includes('user=default') && url.includes('outcome=denied'))).toBe(true)
  })
})
