// @vitest-environment happy-dom
import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { ConsoleError, ConsoleStale, GatewayFeedbackBoundary } from './gatewayConsoleShared'
import { GatewayGroupsPanel } from './GatewayGroupsPanel'

vi.mock('../../services/api', () => ({ getApiBaseUrl: () => 'http://localhost:18161', getAuthToken: () => 'test-jwt' }))

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let root: Root | undefined
let container: HTMLDivElement
afterEach(async () => {
  if (root) await act(async () => root!.unmount())
  container?.remove()
  vi.unstubAllGlobals()
})
async function render(children: React.ReactNode) {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
  await act(async () => root!.render(children))
}

it('deduplicates one outage and refreshes all failed sections with one Retry action', async () => {
  const refreshParent = vi.fn()
  const refreshReview = vi.fn()
  await render(<GatewayFeedbackBoundary>
    <ConsoleStale message="Vault service unavailable" onRetry={refreshParent} />
    <ConsoleError message="Vault service unavailable" onRetry={refreshParent} />
    <ConsoleError message="Vault service unavailable" onRetry={refreshReview} />
    <p>Previously loaded permissions</p>
  </GatewayFeedbackBoundary>)
  expect(container.querySelectorAll('[role="alert"]')).toHaveLength(1)
  expect(container.textContent?.match(/Vault service unavailable/g)).toHaveLength(1)
  expect(container.textContent).toContain('Previously loaded permissions')
  expect(container.textContent).toContain('may be outdated')
  expect(container.querySelectorAll('button')).toHaveLength(1)
  await act(async () => container.querySelector('button')!.click())
  expect(refreshParent).toHaveBeenCalledOnce()
  expect(refreshReview).toHaveBeenCalledOnce()
})

it('clears the shared warning after recovery and uses the latest section callback', async () => {
  function Panel() {
    const [failed, setFailed] = useState(true)
    const [message, setMessage] = useState('Unavailable')
    return <GatewayFeedbackBoundary>
      {failed && <ConsoleError message={message} onRetry={() => setFailed(false)} />}
      <button onClick={() => setMessage('New failure')}>Change failure</button>
      <p>Saved configuration</p>
    </GatewayFeedbackBoundary>
  }
  await render(<Panel />)
  await act(async () => [...container.querySelectorAll('button')].find(button => button.textContent === 'Change failure')!.click())
  expect(container.textContent).toContain('New failure')
  expect(container.textContent).not.toContain('Unavailable')
  await act(async () => [...container.querySelectorAll('button')].find(button => button.textContent === 'Retry')!.click())
  expect(container.querySelector('[role="alert"]')).toBeNull()
  expect(container.textContent).toContain('Saved configuration')
})

it('recovers the real group, membership and permissions loaders from one gateway outage', async () => {
  let unavailable = false
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    let body: unknown = { error: 'Vault service unavailable' }
    if (!unavailable) {
      if (url.endsWith('/groups')) body = { groups: [{ ID: 'g', WorkspaceID: 'w', Name: 'Readers' }] }
      else if (url.endsWith('/users')) body = { users: [] }
      else if (url.endsWith('/members')) body = { members: [] }
      else if (url.endsWith('/connectors')) body = { connectors: [{ ID: 'c', Provider: 'docs', Label: 'Docs' }] }
      else if (url.endsWith('/tools')) body = { tools: [] }
      else if (url.endsWith('/servers')) body = { servers: [] }
      else if (url.endsWith('/permissions')) body = { permissions: [] }
      else if (url.endsWith('/packages')) body = { packages: [] }
      else if (url.endsWith('/secrets')) body = { secrets: [] }
      else if (url.endsWith('/history')) body = { events: [] }
      else throw new Error(`Unexpected read: ${url}`)
    }
    return { ok: !unavailable, status: unavailable ? 502 : 200, json: async () => body } as Response
  }))
  const panel = (revision: string) => <GatewayFeedbackBoundary><GatewayGroupsPanel base="local" revision={revision} /></GatewayFeedbackBoundary>
  await render(panel('initial'))
  expect(container.textContent).toContain('Readers')
  // Groups are a list first; opening one loads its members and MCP permissions.
  await act(async () => (container.querySelector('[aria-label="Select group Readers"]') as HTMLButtonElement).click())
  unavailable = true
  await act(async () => root!.render(panel('changed')))
  expect(container.querySelectorAll('[role="alert"]')).toHaveLength(1)
  const retryButtons = () => [...container.querySelectorAll('button')].filter(button => button.textContent === 'Retry')
  expect(retryButtons()).toHaveLength(1)
  // The opened group stays on screen (its name lives in the rename input).
  expect((container.querySelector('[data-testid="gateway-group-rename-input"]') as HTMLInputElement).value).toBe('Readers')
  unavailable = false
  await act(async () => retryButtons()[0].click())
  expect(container.querySelector('[role="alert"]')).toBeNull()
  // The MCP permissions loader recovered too: the server list renders.
  expect(container.querySelector('[data-testid="gateway-permissions"]')).not.toBeNull()
  expect(container.textContent).toContain('Docs')
})
