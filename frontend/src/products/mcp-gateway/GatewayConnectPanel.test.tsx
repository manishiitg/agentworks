// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import api from '../../services/api'
import { GatewayConnectPanel } from './GatewayConnectPanel'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const BASE = 'http://127.0.0.1:18161'

describe('GatewayConnectPanel', () => {
  let container: HTMLDivElement | null = null

  afterEach(() => {
    container?.remove()
    container = null
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  async function renderPanel(fetchMock: ReturnType<typeof vi.fn>, connections: { id: string; client_name: string }[] = []): Promise<void> {
    vi.mocked(api.get).mockImplementation(async path => ({ data: path === '/api/vault/connection' ? { endpoint: `${BASE}/api/vault/mcp` } : { connections } }) as never)
    vi.stubGlobal('fetch', fetchMock)
    container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => {
      root.render(<GatewayConnectPanel base={BASE} />)
    })
    await act(async () => {})
  }

  function challengeResponse(): Response {
    return {
      ok: false,
      status: 401,
      headers: { get: (name: string) => (name === 'WWW-Authenticate' ? 'Bearer resource="http://x/.well-known/oauth-protected-resource/mcp"' : null) },
      json: () => Promise.resolve({}),
    } as unknown as Response
  }

  it('shows the workspace endpoint with client instructions', async () => {
    await renderPanel(vi.fn().mockRejectedValue(new TypeError('Failed to fetch')))

    expect(container!.querySelector('[data-testid="gateway-connect-url"]')?.textContent).toBe(`${BASE}/api/vault/mcp`)
    expect(container!.textContent).toContain('Connect Claude')
    expect(container!.textContent).toContain('Sign in with your platform account')
  })

  it('does not report Copied when clipboard access is rejected', async () => {
    await renderPanel(vi.fn())
    vi.spyOn(navigator.clipboard, 'writeText').mockRejectedValue(new Error('Denied'))
    await act(async () => {
      ;(container!.querySelector('[data-testid="gateway-connect-copy"]') as HTMLButtonElement).click()
    })
    expect(container!.textContent).toContain('Could not copy')
    expect(container!.querySelector('[data-testid="gateway-connect-copy"]')!.textContent).toBe('Copy')
  })

  it('reports reachable when the probe answers 401 with the OAuth challenge', async () => {
    await renderPanel(vi.fn().mockResolvedValue(challengeResponse()))

    await act(async () => {
      ;(container!.querySelector('[data-testid="gateway-connect-test"]') as HTMLButtonElement).click()
    })
    await act(async () => {})

    expect(container!.textContent).toContain('Endpoint reachable')
  })

  it('disconnects the selected OAuth client and updates the list', async () => {
    vi.mocked(api.delete).mockResolvedValue({ status: 204 } as never)
    await renderPanel(vi.fn(), [{ id: 'family_test', client_name: 'Claude test' }])
    expect(container!.textContent).toContain('Claude test')
    await act(async () => {
      const button = [...container!.querySelectorAll('button')].find(b => b.textContent?.includes('Disconnect'))!
      button.click()
    })
    expect(api.delete).toHaveBeenCalledWith('/api/oauth/vault/connections/family_test')
    expect(container!.textContent).not.toContain('Claude test')
  })

  it('reports unreachable when the probe fails', async () => {
    await renderPanel(vi.fn().mockRejectedValue(new TypeError('Failed to fetch')))

    await act(async () => {
      ;(container!.querySelector('[data-testid="gateway-connect-test"]') as HTMLButtonElement).click()
    })
    await act(async () => {})

    expect(container!.textContent).toContain('Endpoint unreachable')
  })
})

vi.mock('../../services/api', () => ({ default: { get: vi.fn(), delete: vi.fn() } }))
