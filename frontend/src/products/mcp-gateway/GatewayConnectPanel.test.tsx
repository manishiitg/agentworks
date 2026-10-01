// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
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

  async function renderPanel(fetchMock: ReturnType<typeof vi.fn>): Promise<void> {
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

    expect(container!.querySelector('[data-testid="gateway-connect-url"]')?.textContent).toBe(`${BASE}/mcp`)
    expect(container!.textContent).toContain('Connect Claude')
    expect(container!.textContent).toContain('Connect with an API key')
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

  it('reports unreachable when the probe fails', async () => {
    await renderPanel(vi.fn().mockRejectedValue(new TypeError('Failed to fetch')))

    await act(async () => {
      ;(container!.querySelector('[data-testid="gateway-connect-test"]') as HTMLButtonElement).click()
    })
    await act(async () => {})

    expect(container!.textContent).toContain('Endpoint unreachable')
  })
})

vi.mock('../../services/api', () => ({ getApiBaseUrl: () => 'http://127.0.0.1:18161', getAuthToken: () => 'product-jwt' }))
