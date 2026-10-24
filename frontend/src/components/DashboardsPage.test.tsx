// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
const post = vi.hoisted(() => vi.fn())
// Product switching has its own permission and navigation coverage.
vi.mock('./ProductSurfaceSwitcher', () => ({ ProductSurfaceSwitcher: () => <h1>Dashboards</h1> }))
vi.mock('../services/api', () => ({ default: { post } }))
vi.mock('../stores/useAuthStore', () => ({ useAuthStore: (select: (state: unknown) => unknown) => select({ user: { id: 'person' } }) }))
vi.mock('../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: (select: (state: unknown) => unknown) => select({ activeWorkspaceId: 'server' }) }))
vi.mock('./workspace/WorkspaceTopToolbar', () => ({ WorkspaceTopToolbar: ({ children }: { children: import('react').ReactNode }) => <header>{children}</header> }))
import { DashboardsPage } from './DashboardsPage'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('shows every published page in its server-selected audience and refreshes after access changes', async () => {
  post.mockResolvedValueOnce({ data: { dashboards: [{ dashboard_id: 'sales', title: 'Sales', product: 'crew', project_title: 'Sales crew', workspace: 'Crew/sales', document_path: 'db/reports/sales.html', shared_with_me: false }], next_offset: 200, has_more: true } })
    .mockResolvedValueOnce({ data: { dashboards: [{ dashboard_id: 'team', title: 'Team totals', product: 'crew', project_title: 'Team crew', workspace: 'Crew/team', document_path: 'db/reports/managed/team/index.html', short_path: '/d/23456789ab', shared_with_me: true }], next_offset: 201, has_more: false } })
    .mockResolvedValueOnce({ data: { dashboards: [], next_offset: 0, has_more: false } })
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  try {
    await act(async () => root.render(<DashboardsPage />))
    expect(post.mock.calls.map(call => call[1])).toEqual([
      { action: 'list', published_only: true, limit: 200, offset: 0 },
      { action: 'list', published_only: true, limit: 200, offset: 200 },
    ])
    const yours = host.querySelector('section[aria-label="Sales crew"]')!
    const shared = host.querySelector('section[aria-label="Team crew"]')!
    expect(yours.textContent).toContain('Sales')
    expect(yours.textContent).not.toContain('Team totals')
    expect(shared.querySelector('a')?.getAttribute('href')).toBe('/d/23456789ab')
    const url = new URL(yours.querySelector('a')!.href)
    expect(url.searchParams.get('document')).toBe('db/reports/sales.html')
    expect(atob(url.searchParams.get('path')!)).toBe('Crew/sales')
    await act(async () => window.dispatchEvent(new Event('focus')))
    expect(host.querySelector('section a')).toBeNull()
    expect(host.textContent).toContain('appear here automatically')
  } finally { await act(async () => root.unmount()); host.remove(); post.mockReset() }
})


it('groups by actual project identity within each product and searches every dashboard', async () => {
  post.mockResolvedValue({ data: { dashboards: [
    { dashboard_id: 'overview', title: 'index', project_title: 'Account research', product: 'crew', workspace: 'Crew/research', document_path: 'db/reports/index.html', shared_with_me: true },
    { dashboard_id: 'briefing', title: 'Briefing', project_title: 'Account research', product: 'crew', workspace: 'Crew/research', document_path: 'db/reports/briefing.html', shared_with_me: true },
    { dashboard_id: 'budget', title: 'Budget', project_title: 'Account research', product: 'crew', workspace: 'Crew/other-research', document_path: 'db/reports/budget.html', shared_with_me: false },
    { dashboard_id: 'sales', title: 'Weekly sales', project_title: 'Revenue', product: 'workflow', workspace: 'Goals/revenue', document_path: 'db/reports/sales.html', shared_with_me: true },
    { dashboard_id: 'forecast', title: 'Forecast', project_title: 'Revenue', product: 'workflow', workspace: 'Goals/revenue', document_path: 'db/reports/forecast.html', shared_with_me: true },
    { dashboard_id: 'queue', title: 'Request queue', project_title: 'Support', product: 'relay', workspace: 'Workflow/support', document_path: 'db/reports/queue.html', shared_with_me: false },
  ], next_offset: 6, has_more: false } })
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  const search = () => host.querySelector('input')!
  const type = async (value: string) => act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(search(), value)
    search().dispatchEvent(new Event('input', { bubbles: true }))
  })
  try {
    await act(async () => root.render(<DashboardsPage />))
    const group = (label: string) => host.querySelector<HTMLElement>(`section[aria-label="${label}"]`)!
    expect(Array.from(host.querySelectorAll('section > div > h3')).map(heading => heading.textContent)).toEqual(['Crew', 'Goals', 'Relays'])
    const crews = group('Crew').querySelectorAll('[data-dashboard-project]')
    expect(crews).toHaveLength(2)
    expect(Array.from(crews).map(project => project.querySelector('h4')!.textContent)).toEqual(['Account research', 'Account research'])
    expect(Array.from(crews[0].querySelectorAll('a')).map(link => link.textContent)).toEqual(['Account research', 'Briefing'])
    expect(crews[0].textContent).toContain('Shared with you')
    expect(crews[1].querySelector('a')!.textContent).toBe('Budget')
    expect(crews[1].textContent).toContain('Yours')
    expect(group('Crew').textContent).not.toContain('index')
    expect(group('Goals').querySelectorAll('[data-dashboard-project]')).toHaveLength(1)
    expect(group('Goals').querySelector('h4')!.textContent).toBe('Revenue')
    expect(group('Goals').querySelectorAll('a')).toHaveLength(2)
    expect(group('Relays').querySelector('h4')!.textContent).toBe('Support')
    expect(group('Relays').querySelector('a')!.textContent).toBe('Request queue')
    expect(host.querySelector('header a')).toBeNull()
    expect(host.querySelector('header')!.textContent).toBe('Dashboards')
    await type('  REVENUE ')
    expect(group('Crew')).toBeNull()
    expect(group('Relays')).toBeNull()
    expect(group('Goals').querySelectorAll('a')).toHaveLength(2)
    expect(group('Goals').textContent).toContain('Weekly sales')
    await type('missing')
    expect(host.querySelector('section a')).toBeNull()
    expect(host.textContent).toContain('No matching dashboards')
    await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="Clear search"]')!.click())
    expect(host.querySelectorAll('section[aria-label="Crew"] a, section[aria-label="Goals"] a, section[aria-label="Relays"] a')).toHaveLength(6)
    expect(post).toHaveBeenCalledTimes(1)
  } finally { await act(async () => root.unmount()); host.remove(); post.mockReset() }
})
