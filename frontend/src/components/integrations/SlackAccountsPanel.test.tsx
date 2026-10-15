// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'

const requests = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(async () => ({ data: {} })) }))
vi.mock('../../services/api', () => ({ default: requests }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: (state: unknown) => unknown) => select({ user: { is_admin: false } }) }))
vi.mock('../../products/work/McpAppsSection', () => ({ McpAppsSection: () => null }))
import SlackAccountsPanel from './SlackAccountsPanel'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('reuses the exact saved account and displays only the server-authorized actions', async () => {
  requests.get.mockResolvedValue({ data: { can_connect: false, notice: 'Anyone who can run this item can send as you through it.', accounts: [
    { id: 'slack_work', owner: 'builder', label: 'Work Slack', connected: true, selected: false, can_add: true, can_detach: false, can_manage: false },
    { id: 'slack_selected', owner: 'another', label: 'Selected Slack', connected: true, selected: true, can_add: false, can_detach: true, can_manage: false },
  ] } })
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => { root.render(<SlackAccountsPanel workspacePath="Workflow/orders" />) })
    expect(host.textContent).toContain('send as you')
    expect(host.textContent).not.toContain('Disconnect everywhere')
    expect(host.textContent).not.toContain('Connect Slack account and add here')
    const add = [...host.querySelectorAll('button')].find(button => button.textContent === 'Add here')!
    await act(async () => { add.click() })
    expect(requests.post).toHaveBeenLastCalledWith('/api/integrations/slack/accounts', { workspace_path: 'Workflow/orders', action: 'attach', connection_id: 'slack_work', connection_owner: 'builder' })
    const remove = [...host.querySelectorAll('button')].find(button => button.textContent === 'Remove here')!
    await act(async () => { remove.click() })
    expect(requests.post).toHaveBeenLastCalledWith('/api/integrations/slack/accounts', { workspace_path: 'Workflow/orders', action: 'detach', connection_id: 'slack_selected', connection_owner: 'another' })
  } finally {
    act(() => root.unmount()); host.remove()
  }
})
