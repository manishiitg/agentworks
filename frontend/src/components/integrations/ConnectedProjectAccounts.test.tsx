// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({ google: vi.fn(), slack: vi.fn(), account: 'alice' }))
vi.mock('../../services/api', () => ({ default: { get: mocks.slack }, agentApi: { listGmailConnections: mocks.google }, getApiBaseUrl: () => 'local-test' }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: (s: unknown) => unknown) => select({ user: { id: mocks.account } }) }))
import { ConnectedProjectAccounts } from './ConnectedProjectAccounts'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let host: HTMLDivElement, root: Root
const open = vi.fn()
beforeEach(() => {
  mocks.account = 'alice'
  mocks.google.mockReset().mockResolvedValue({ connections: [{ id: 'google', display_name: 'Work account', enabled: true, ready: true }] })
  mocks.slack.mockReset().mockResolvedValue({ data: { accounts: [{ id: 'slack', owner: 'alice', label: 'Team Slack', selected: true, connected: false }, { id: 'other', owner: 'bob', label: 'Unused account', selected: false, connected: true }] } })
  host = document.createElement('div'); document.body.append(host); root = createRoot(host)
})
afterEach(async () => { await act(async () => root.unmount()); host.remove(); open.mockClear() })
const render = async (workspacePath = 'Crew/one', targets = ['gmail', 'slack']) => act(async () => root.render(<ConnectedProjectAccounts workspacePath={workspacePath} options={targets.map(value => ({ value }))} onOpen={open} />))

it('shows actual scoped account state and opens original management panels without a write', async () => {
  await render()
  expect(mocks.google).toHaveBeenCalledWith('Crew/one')
  expect(mocks.slack).toHaveBeenCalledWith('/api/integrations/slack/accounts', expect.objectContaining({ params: { workspace_path: 'Crew/one' } }))
  expect(host.textContent).toContain('Google apps · Connected')
  expect(host.textContent).toContain('Slack · Needs sign-in')
  expect(host.textContent).not.toContain('Unused account')
  await act(async () => host.querySelector<HTMLButtonElement>('button')!.click())
  expect(open).toHaveBeenCalledWith('gmail')
})

it('does not fetch or display channels outside the admitted inventory', async () => {
  await render('Crew/one', [])
  expect(mocks.google).not.toHaveBeenCalled(); expect(mocks.slack).not.toHaveBeenCalled()
  expect(host.textContent).toBe('')
  await render('Crew/one', ['gmail'])
  expect(mocks.google).toHaveBeenCalledOnce(); expect(mocks.slack).not.toHaveBeenCalled()
})

it('reports failed reads and allows retry instead of implying no accounts exist', async () => {
  mocks.google.mockRejectedValueOnce(new Error('denied'))
  await render('Crew/one', ['gmail'])
  expect(host.textContent).toContain('Could not load Google apps')
  expect(host.textContent).not.toContain('No accounts added')
  await act(async () => host.querySelector<HTMLButtonElement>('button')!.click())
  expect(host.textContent).toContain('Work account')
})

it('ignores late responses from a previous account/workspace', async () => {
  let finish!: (value: unknown) => void
  mocks.google.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
  await render('Crew/one', ['gmail'])
  mocks.account = 'bob'
  mocks.google.mockResolvedValue({ connections: [] })
  await render('Crew/two', ['gmail'])
  await act(async () => finish({ connections: [{ id: 'old', display_name: 'Old private account', enabled: true, ready: true }] }))
  expect(host.textContent).not.toContain('Old private account')
  expect(host.textContent).toContain('No accounts added')
})

it('removes channel details immediately when its inventory permission disappears', async () => {
  await render()
  expect(host.textContent).toContain('Team Slack')
  await render('Crew/one', ['gmail'])
  expect(host.textContent).not.toContain('Team Slack')
})
