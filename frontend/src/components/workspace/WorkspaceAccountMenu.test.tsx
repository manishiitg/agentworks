// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { WorkspaceAccountMenu } from './WorkspaceAccountMenu'
import { AgentHeaderLine } from '../chat/AgentCard'
import { useProductNavigationToolbarStore } from '../../stores/useProductNavigationToolbarStore'

const f = vi.hoisted(() => ({
  signInNeeded: vi.fn(async () => 0),
  openLimits: vi.fn(),
  user: { id: 'owner', username: 'Owner', email: 'owner@example.com', allowed_products: ['work'] } as { id: string; username?: string; email?: string; allowed_products?: string[] } | null,
}))

vi.mock('../../services/llm-config-api', () => ({ llmConfigService: { getSignInNeeded: f.signInNeeded } }))
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: () => {} }))
vi.mock('../../utils/subscriptionLimitsNavigation', () => ({ openSubscriptionLimits: f.openLimits }))
vi.mock('../../utils/workspaceSettingsNavigation', () => ({ openWorkspaceSettings: vi.fn() }))
vi.mock('../../utils/productWorkspaceNavigation', () => ({ openProductWorkspace: vi.fn() }))
vi.mock('../../stores/useAuthStore', async importOriginal => ({
  ...await importOriginal<typeof import('../../stores/useAuthStore')>(),
  useAuthStore: Object.assign((select?: (state: unknown) => unknown) => {
    const state = { user: f.user, isMultiUserMode: false, providers: [], logout: vi.fn() }
    return select ? select(state) : state
  }, {
    getState: () => ({ user: f.user, isMultiUserMode: false, providers: [], logout: vi.fn() }),
    subscribe: () => () => {},
  }),
}))
vi.mock('../../stores/useConnectedWorkStore', () => ({
  connectedLocations: () => [],
  useConnectedWorkPolling: () => {},
  useConnectedWorkStore: (select: (state: unknown) => unknown) => select({}),
}))
vi.mock('../../stores/useWorkspaceConnectionStore', async importOriginal => ({
  ...await importOriginal<typeof import('../../stores/useWorkspaceConnectionStore')>(),
  useWorkspaceConnectionStore: Object.assign((select: (state: unknown) => unknown) => select({ activeWorkspaceId: 'local', profiles: [] }), {
    getState: () => ({ activeWorkspaceId: 'local', profiles: [] }),
  }),
}))
vi.mock('./AppearanceControl', () => ({ default: () => null }))
vi.mock('./GetApp', () => ({ GetApp: () => null }))
vi.mock('../topbar/NotificationsControl', () => ({ default: () => null }))

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

afterEach(() => {
  useProductNavigationToolbarStore.setState({ sidebarSettingsTarget: null, compactSettingsTarget: null, settingsTarget: null })
  vi.clearAllMocks()
})

it('shows the sign-in alert on the sidebar Account & settings trigger and menu item, and opens Accounts and usage', async () => {
  f.signInNeeded.mockResolvedValue(2)
  const host = document.createElement('div')
  const footer = document.createElement('div')
  document.body.append(host, footer)
  useProductNavigationToolbarStore.setState({ sidebarSettingsTarget: footer })
  const root = createRoot(host)
  try {
    await act(async () => { root.render(<WorkspaceAccountMenu />); await Promise.resolve() })
    const trigger = footer.querySelector<HTMLButtonElement>('[data-product-settings-trigger]')!
    expect(trigger.getAttribute('aria-label')).toBe('Account & settings — 2 accounts need sign-in')
    expect(trigger.querySelector('[data-account-sign-in-dot]')?.textContent).toBe('2')

    await act(async () => trigger.click())
    const item = [...footer.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find(b => b.textContent?.includes('2 accounts need sign-in'))
    expect(item).toBeTruthy()
    await act(async () => item!.click())
    expect(f.openLimits).toHaveBeenCalledOnce()
  } finally {
    await act(async () => root.unmount())
    host.remove()
    footer.remove()
  }
})

it('shows a dot on the compact Account & settings trigger when collapsed, and hides it when all accounts are signed in', async () => {
  f.signInNeeded.mockResolvedValue(1)
  const host = document.createElement('div')
  const compact = document.createElement('div')
  document.body.append(host, compact)
  useProductNavigationToolbarStore.setState({ compactSettingsTarget: compact })
  const root = createRoot(host)
  try {
    await act(async () => { root.render(<WorkspaceAccountMenu />); await Promise.resolve() })
    const trigger = compact.querySelector<HTMLButtonElement>('[data-product-settings-trigger]')!
    expect(trigger.getAttribute('aria-label')).toBe('Account & settings — 1 account needs sign-in')
    expect(trigger.querySelector('[data-account-sign-in-dot]')).toBeTruthy()
  } finally {
    await act(async () => root.unmount())
    host.remove()
    compact.remove()
  }

  f.signInNeeded.mockResolvedValue(0)
  const host2 = document.createElement('div')
  const footer2 = document.createElement('div')
  document.body.append(host2, footer2)
  useProductNavigationToolbarStore.setState({ sidebarSettingsTarget: footer2 })
  const root2 = createRoot(host2)
  try {
    await act(async () => { root2.render(<WorkspaceAccountMenu />); await Promise.resolve() })
    const trigger = footer2.querySelector<HTMLButtonElement>('[data-product-settings-trigger]')!
    expect(trigger.getAttribute('aria-label')).toBe('Account & settings')
    expect(trigger.querySelector('[data-account-sign-in-dot]')).toBeNull()
  } finally {
    await act(async () => root2.unmount())
    host2.remove()
    footer2.remove()
  }
})

it('allows longer agent names in the desktop Crew header before truncating', async () => {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<AgentHeaderLine crewName="ai-work" agent={{ key: 'a', name: 'Skills: add and share across teams', tabId: 't1', isDefault: false }} />))
    const label = host.querySelector('[data-agent-header] span[title="Skills: add and share across teams"]')!
    expect(label.className).toContain('md:max-w-[28rem]')
    expect(label.className).toContain('lg:max-w-[36rem]')
  } finally {
    await act(async () => root.unmount())
    host.remove()
  }
})
