// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
const post = vi.hoisted(() => vi.fn())
vi.mock('../services/api', () => ({ default: { post } }))
vi.mock('../stores/useAuthStore', () => ({ useAuthStore: (select: (state: unknown) => unknown) => select({ user: { id: 'person' } }) }))
vi.mock('../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: (select: (state: unknown) => unknown) => select({ activeWorkspaceId: 'server' }) }))
vi.mock('./workspace/WorkspaceTopToolbar', () => ({ WorkspaceTopToolbar: ({ children }: { children: import('react').ReactNode }) => <header>{children}</header> }))
import { DashboardsPage } from './DashboardsPage'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('shows every published page in its server-selected audience and refreshes after access changes', async () => {
  post.mockResolvedValueOnce({ data: { dashboards: [{ dashboard_id: 'sales', title: 'Sales', workspace: 'Crew/sales', document_path: 'db/reports/sales.html', shared_with_me: false }], next_offset: 200, has_more: true } })
    .mockResolvedValueOnce({ data: { dashboards: [{ dashboard_id: 'team', title: 'Team totals', workspace: 'Crew/team', document_path: 'db/reports/managed/team/index.html', short_path: '/d/23456789ab', shared_with_me: true }], next_offset: 201, has_more: false } })
    .mockResolvedValueOnce({ data: { dashboards: [], next_offset: 0, has_more: false } })
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  try {
    await act(async () => root.render(<DashboardsPage />))
    expect(post.mock.calls.map(call => call[1])).toEqual([
      { action: 'list', published_only: true, limit: 200, offset: 0 },
      { action: 'list', published_only: true, limit: 200, offset: 200 },
    ])
    const yours = host.querySelector('section[aria-label="Yours"]')!
    const shared = host.querySelector('section[aria-label="Shared with you"]')!
    expect(yours.textContent).toContain('Sales')
    expect(yours.textContent).not.toContain('Team totals')
    expect(shared.querySelector('a')?.getAttribute('href')).toBe('/d/23456789ab')
    const url = new URL(yours.querySelector('a')!.href)
    expect(url.searchParams.get('document')).toBe('db/reports/sales.html')
    expect(atob(url.searchParams.get('path')!)).toBe('Crew/sales')
    await act(async () => window.dispatchEvent(new Event('focus')))
    expect(host.querySelector('section a')).toBeNull()
    expect(host.textContent).toContain('appears here automatically')
  } finally { await act(async () => root.unmount()); host.remove(); post.mockReset() }
})


it('finds dashboards by display name or project without duplicating filename titles', async () => {
  post.mockResolvedValue({ data: { dashboards: [
    { dashboard_id: 'overview', title: 'index', project_title: 'Account research', product: 'crew', workspace: 'Crew/research', document_path: 'db/reports/index.html', shared_with_me: true },
    { dashboard_id: 'sales', title: 'Weekly sales', project_title: 'Revenue', product: 'workflow', workspace: 'Goals/revenue', document_path: 'db/reports/sales.html', shared_with_me: true },
  ], next_offset: 2, has_more: false } })
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  const search = () => host.querySelector('input')!
  const type = async (value: string) => act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(search(), value)
    search().dispatchEvent(new Event('input', { bubbles: true }))
  })
  try {
    await act(async () => root.render(<DashboardsPage />))
    const shared = host.querySelector('section[aria-label="Shared with you"]')!
    expect(shared.querySelectorAll('a')).toHaveLength(2)
    expect(shared.querySelector('a')!.textContent).toBe('CrewAccount research')
    expect(shared.textContent).not.toContain('index')
    await type('  REVENUE ')
    expect(shared.querySelectorAll('a')).toHaveLength(1)
    expect(shared.textContent).toContain('Weekly sales')
    await type('missing')
    expect(host.querySelector('section a')).toBeNull()
    expect(host.textContent).toContain('No matching dashboards')
    await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="Clear search"]')!.click())
    expect(host.querySelectorAll('section a')).toHaveLength(2)
    expect(post).toHaveBeenCalledTimes(1)
  } finally { await act(async () => root.unmount()); host.remove(); post.mockReset() }
})
