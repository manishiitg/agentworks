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
    await import('../../components/integrations/CliMcpSetupContent')
    container = document.createElement('div'); document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => { root.render(<GatewayConnectPanel base={BASE} />) })
    await act(async () => {})
  }
  const button = (label: string) => [...container!.querySelectorAll('button')].find(el => el.textContent === label)!

  it('revokes only the selected Vault OAuth client', async () => {
    vi.mocked(api.delete).mockResolvedValue({ status: 204 } as never)
    await renderPanel([{ id: 'family_test', client_name: 'Claude test' }])
    await act(async () => { button('Revoke').click() })
    expect(api.delete).toHaveBeenCalledWith('/api/oauth/vault/connections/family_test')
    expect(container!.textContent).not.toContain('Claude test')
  })
})
