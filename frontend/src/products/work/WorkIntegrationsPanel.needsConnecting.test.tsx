// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'

vi.mock('../../services/api', async (importOriginal) => ({ ...(await importOriginal<object>()), agentApi: { getTools: vi.fn(), getToolDetail: vi.fn() } }))
vi.mock('../../components/connectors/ConnectorsBrowser', () => ({
  default: ({ query }: { query?: string }) => <div data-testid="connectors-browser">{query}</div>,
}))
vi.mock('../../stores/useMCPStore', async () => {
  const { create } = await import('zustand')
  return { useMCPStore: create(() => ({ toolList: [] as unknown[], isLoadingTools: false })) }
})
vi.mock('../../stores/useAuthStore', async () => {
  const { create } = await import('zustand')
  return { useAuthStore: create(() => ({ user: { is_admin: true }, isMultiUserModeChecked: true, isMultiUserMode: true })) }
})
vi.mock('../../stores/useChatStore', async () => {
  const { create } = await import('zustand')
  return { useChatStore: create(() => ({ chatTabs: { t1: { config: { selectedServers: ['Resend', 'Notion'] } } } })) }
})

import { WorkMCPTabBody } from './WorkIntegrationsPanel'
import { useMCPStore } from '../../stores/useMCPStore'
import { useAuthStore } from '../../stores/useAuthStore'

const tool = (server: string) => ({ server, connection: 'connected', name: 'search', description: '', parameters: {} })

const cleanups: Array<() => void> = []
afterEach(() => { for (const cleanup of cleanups.splice(0)) cleanup() })

async function mount(onAsk = vi.fn()) {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  await act(async () => root.render(
    <WorkMCPTabBody tabId="t1" projectId="p1" workspacePath="_users/o/Chats/Work/projects/p1" onAsk={onAsk} onSelectedServersChange={vi.fn()} />,
  ))
  return { host, onAsk }
}

describe('Crew MCPs: selected but disconnected apps', () => {
  it('names each app that needs connecting and offers Connect and Ask agent', async () => {
    useAuthStore.setState({ user: { id: 'admin', username: 'admin', is_admin: true } })
    useMCPStore.setState({ toolList: [tool('Notion')], isLoadingTools: false })
    const { host, onAsk } = await mount()
    const banner = host.querySelector('[data-testid="work-mcp-needs-connecting"]')
    expect(banner?.textContent).toContain('Resend')
    expect(banner?.textContent).not.toContain('Notion')
    const [connect, ask] = Array.from(banner!.querySelectorAll('button'))
    await act(async () => connect.click())
    expect(host.querySelector('[data-testid="connectors-browser"]')?.textContent).toBe('Resend')
    // Connect takes the user to the connect section for that app, and the
    // notice stays until the app is actually connected.
    expect(host.querySelector('[data-testid="work-mcp-connect-section"]')?.textContent).toContain('Connect Resend')
    expect(host.querySelector('[data-testid="work-mcp-needs-connecting"]')).not.toBeNull()
    await act(async () => useMCPStore.setState({ isLoadingTools: true }))
    expect(host.querySelector('[data-testid="work-mcp-needs-connecting"]')?.textContent).toContain('Resend')
    await act(async () => useMCPStore.setState({ isLoadingTools: false }))
    await act(async () => ask.click())
    expect(onAsk.mock.calls[0][0]).toContain('connect Resend')
  })

  it('tells a non-admin who can connect the app instead of a dead Connect button', async () => {
    useAuthStore.setState({ user: { id: 'member', username: 'member', is_admin: false } })
    useMCPStore.setState({ toolList: [tool('Notion')], isLoadingTools: false })
    const { host } = await mount()
    const banner = host.querySelector('[data-testid="work-mcp-needs-connecting"]')
    expect(banner?.textContent).toContain('Only an admin can connect it.')
    expect(Array.from(banner!.querySelectorAll('button')).map(b => b.textContent)).toEqual(['Ask agent'])
  })

  it('stays quiet while connections load and when everything is connected', async () => {
    useMCPStore.setState({ toolList: [], isLoadingTools: true })
    let { host } = await mount()
    expect(host.querySelector('[data-testid="work-mcp-needs-connecting"]')).toBeNull()
    cleanups.splice(0).forEach(cleanup => cleanup())
    useMCPStore.setState({ toolList: [tool('Notion'), tool('Resend')], isLoadingTools: false })
    ;({ host } = await mount())
    expect(host.querySelector('[data-testid="work-mcp-needs-connecting"]')).toBeNull()
  })
})
