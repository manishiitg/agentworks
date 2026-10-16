// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({ home: vi.fn() }))
vi.mock('../services/workspaceState', () => ({ workspaceStateApi: { home: mocks.home } }))
vi.mock('./workspace/ProductTopBar', () => ({ ProductTopBar: ({ children }: { children: React.ReactNode }) => <nav>{children}</nav>, ProductTopBarMain: ({ children }: { children: React.ReactNode }) => <>{children}</>, ProductTopBarActions: ({ children }: { children: React.ReactNode }) => <>{children}</> }))
vi.mock('./ProductSurfaceSwitcher', () => ({ ProductSurfaceSwitcher: () => <span>Products</span> }))
vi.mock('./ProductSurfaceIcon', () => ({ ProductSurfaceIcon: () => <span /> }))
vi.mock('./topbar/AccountControl', () => ({ default: () => <span>Account</span> }))
vi.mock('./WelcomeHome', () => ({ OPEN_WELCOME_HOME_EVENT: 'open-welcome-home' }))
vi.mock('../utils/globalProductNavigation', () => ({ openGlobalActivitySession: vi.fn() }))
import { HomePage } from './HomePage'
import { useWorkspaceReturnStore } from '../stores/useWorkspaceReturnStore'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => { vi.clearAllMocks(); window.history.replaceState({}, '', '/') })
describe('Home', () => {
  it('renders server attention before existing work and running activity; rows open existing routes', async () => {
    mocks.home.mockResolvedValue({ attention: [{ id: 'decision', kind: 'decision', title: 'Review budget', work: { surface: 'agentworks', id: 'finance', label: 'Finance', workspace_path: 'Workflow/finance' }, view: 'human-actions' }], work: [{ surface: 'agentworks', id: 'finance', label: 'Finance', workspace_path: 'Workflow/finance' }], running: [], warnings: [], truncated: false })
    useWorkspaceReturnStore.setState({ account: 'alice', note: '', saveError: '', record: { version: 1, revision: 0, last: null, recent: [] } })
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    try {
      await act(async () => root.render(<HomePage />))
      expect([...host.querySelectorAll('h2')].map(node => node.textContent?.trim())).toEqual(['Needs my attention 1', 'My existing work', 'Running activity'])
      expect(host.textContent).toContain('Review budget')
      await act(async () => (Array.from(host.querySelectorAll('button')).find(button => button.textContent?.includes('Review budget')) as HTMLButtonElement).click())
      expect(window.location.pathname + window.location.search).toBe('/workflows/finance?view=human-actions')
      expect(host.textContent).not.toContain('Operations assistant')
    } finally { await act(async () => root.unmount()); host.remove() }
  })
  it('shows a load failure instead of claiming no attention or inventing sample work', async () => {
    mocks.home.mockRejectedValue(new Error('offline'))
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    try {
      await act(async () => root.render(<HomePage />))
      expect(host.querySelector('[role=alert]')?.textContent).toContain('Could not refresh')
      expect(host.textContent).not.toContain('Nothing needs your attention')
    } finally { await act(async () => root.unmount()); host.remove() }
  })
})
