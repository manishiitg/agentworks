// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import api from '../../services/api'
import { GatewayConnectPanel } from './GatewayConnectPanel'

vi.mock('../../services/api', () => ({ default: { get: vi.fn(), delete: vi.fn() }, getApiBaseUrl: () => 'https://product.example', externalSkillApi: {} }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const BASE = 'https://product.example'

describe('GatewayConnectPanel shared client setup', () => {
  let container: HTMLDivElement | null = null
  afterEach(() => { container?.remove(); container = null; vi.restoreAllMocks() })

  async function renderPanel(connections: { id: string; client_name: string }[] = []) {
    vi.mocked(api.get).mockImplementation(async path => ({ data: path === '/api/vault/connection' ? { endpoint: `${BASE}/api/vault/mcp` } : { connections } }) as never)
    container = document.createElement('div'); document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => { root.render(<GatewayConnectPanel base={BASE} />) })
    await act(async () => {})
  }
  const button = (label: string) => [...container!.querySelectorAll('button')].find(el => el.textContent === label)!

  it('uses Vault commands and group instructions without a test request or workflow downloads', async () => {
    await renderPanel()
    expect(container!.textContent).toContain(`claude mcp add --transport http vault '${BASE}/api/vault/mcp'`)
    expect(container!.textContent).toContain('your user and group permissions')
    expect(container!.textContent).not.toContain('Send test request')
    expect(api.get).toHaveBeenCalledWith('/api/oauth/vault/connections')
    expect(api.get).not.toHaveBeenCalledWith('/api/oauth/mcp/connections')
    await act(async () => { button('Codex').click() })
    expect(container!.textContent).toContain('codex mcp login vault')
    await act(async () => { button('JSON MCP client').click() })
    expect(container!.querySelector('pre')!.textContent).toContain('"vault"')
    expect(container!.querySelector('pre')!.textContent).not.toContain('"agentworks"')
    await act(async () => { [...container!.querySelectorAll('button')].find(el => el.textContent?.includes('Hosted AI app'))!.click() })
    await act(async () => { button('Claude Cowork').click() })
    expect(container!.textContent).toContain('Connect manually instead')
    expect(container!.textContent).not.toContain('Download Cowork plugin')
    expect(container!.textContent).not.toContain('Download skill')
  })

  it('does not claim Copied when clipboard rejects the URL', async () => {
    await renderPanel()
    vi.spyOn(navigator.clipboard, 'writeText').mockRejectedValue(new Error('Denied'))
    await act(async () => { (container!.querySelector('[title="Copy: Vault MCP URL"]') as HTMLButtonElement).click() })
    expect(container!.querySelector('[title="Copied"]')).toBeNull()
  })

  it('revokes only the selected Vault OAuth client', async () => {
    vi.mocked(api.delete).mockResolvedValue({ status: 204 } as never)
    await renderPanel([{ id: 'family_test', client_name: 'Claude test' }])
    await act(async () => { button('Revoke').click() })
    expect(api.delete).toHaveBeenCalledWith('/api/oauth/vault/connections/family_test')
    expect(container!.textContent).not.toContain('Claude test')
  })
})
