// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

vi.mock('../../services/llm-config-api', () => ({
  llmConfigService: {
    getProviderConnections: vi.fn(),
    addProviderConnection: vi.fn(),
    updateProviderConnection: vi.fn(),
    deleteProviderConnection: vi.fn(),
    setServerAccountAvailability: vi.fn(),
    getProviderShareTargets: vi.fn(),
    startProviderSetup: vi.fn(),
    checkProviderUsage: vi.fn(),
    getProviderAccountCosts: vi.fn(),
  },
  providerApiErrorText: (error: { response?: { data?: unknown } }, fallback: string) => typeof error?.response?.data === 'string' ? error.response.data : fallback,
}))
vi.mock('./GuidedProviderTerminal', () => ({
  default: ({ session }: { session: { id: string } }) => <div data-testid="guided-terminal">Terminal {session.id}</div>,
}))

import { llmConfigService, type ProviderConnection } from '../../services/llm-config-api'
import ProviderAccounts from './ProviderAccounts'
import { resetShareTargetsCache, SHARING_WARNING } from './SharingEditor'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let root: Root | undefined

const server: ProviderConnection = {
  id: 'global:claude-code', provider: 'claude-code', display_name: 'Server account', scope: 'global', auth_method: 'server',
  kind: 'installed', relation: 'server', source: 'Installation (.env: CLAUDE_CODE_OAUTH_TOKEN)',
  availability: { available_to: 'all', text: 'Everyone', source: 'installation', pinned: false },
  availability_editable: true, usable: true, can_manage: true, can_view_usage: true,
}
const own: ProviderConnection = {
  id: 'acct-own', provider: 'claude-code', display_name: 'My Max', scope: 'user', auth_method: 'cli_login',
  kind: 'user', relation: 'own', usable: true, can_manage: true, can_view_usage: true,
  sharing: { mode: 'shared', workflows: ['wf-1'], crews: [], users: ['u-bob', 'u-carol'] },
}
const sharedWithMe: ProviderConnection = {
  id: 'acct-dana', provider: 'claude-code', display_name: 'Dana team', scope: 'user', auth_method: 'api_key',
  kind: 'user', relation: 'shared_with_you', owner_name: 'Dana', usable: true, native_tools_off: true, can_manage: false, can_view_usage: true,
}
const adminView: ProviderConnection = {
  id: 'acct-erin', provider: 'claude-code', display_name: 'Erin personal', scope: 'user', auth_method: 'api_key',
  kind: 'user', relation: 'admin_view', owner_name: 'Erin', usable: false, can_manage: true, can_view_usage: true, sharing: { mode: 'private' },
}

beforeEach(() => {
  resetShareTargetsCache()
  vi.mocked(llmConfigService.getProviderConnections).mockResolvedValue([server, own, sharedWithMe, adminView])
  vi.mocked(llmConfigService.getProviderShareTargets).mockResolvedValue({
    workflows: [{ id: 'wf-1', name: 'Research' }], crews: [{ id: '_users/bob/Chats/Work/projects/c1', name: 'Ops Crew', owner: 'bob' }], users: [{ id: 'u-bob', name: 'bob', email: 'bob@x.com' }],
  })
  vi.mocked(llmConfigService.getProviderAccountCosts).mockResolvedValue({ providers: [] })
  Object.defineProperty(window, 'confirm', { configurable: true, value: vi.fn(() => true) })
})
afterEach(() => { act(() => { root?.unmount() }); root = undefined; document.body.innerHTML = ''; vi.clearAllMocks() })

const render = async (element: React.ReactElement) => {
  const container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
  await act(async () => { root?.render(element) })
  await act(async () => Promise.resolve())
  return container
}
const buttonByText = (container: HTMLElement, text: string) => [...container.querySelectorAll('button')].find(button => button.textContent?.trim() === text)
const click = async (element?: Element | null) => { expect(element).toBeTruthy(); await act(async () => { (element as HTMLElement).click() }); await act(async () => Promise.resolve()) }
const setValue = async (input: HTMLInputElement | HTMLSelectElement, value: string) => {
  const prototype = input instanceof HTMLSelectElement ? HTMLSelectElement.prototype : HTMLInputElement.prototype
  await act(async () => {
    Object.getOwnPropertyDescriptor(prototype, 'value')!.set!.call(input, value)
    input.dispatchEvent(new Event(input instanceof HTMLSelectElement ? 'change' : 'input', { bubbles: true }))
  })
}

it('shows the server account with its origin and lets an admin change who can use it', async () => {
  const container = await render(<ProviderAccounts provider="claude-code" providerLabel="Claude Code" />)
  expect(container.textContent).toContain('Installed')
  expect(container.textContent).toContain('Installation (.env: CLAUDE_CODE_OAUTH_TOKEN)')
  expect(container.textContent).toContain('Available to: Everyone')
  await click(buttonByText(container, 'Edit who can use it'))
  const adminsOnly = [...container.querySelectorAll('label')].find(label => label.textContent?.trim() === 'Admins only')?.querySelector('input')
  await click(adminsOnly)
  await click(buttonByText(container, 'Save'))
  expect(llmConfigService.setServerAccountAvailability).toHaveBeenCalledWith('claude-code', 'admins')
})

it('says when the installation pins who can use the server account', async () => {
  vi.mocked(llmConfigService.getProviderConnections).mockResolvedValue([{ ...server, kind: 'admin', availability: { available_to: 'admins', text: 'Admins only', source: 'installation', pinned: true }, availability_editable: false }])
  const container = await render(<ProviderAccounts provider="claude-code" />)
  expect(container.textContent).toContain('Admin-configured')
  expect(container.textContent).toContain('Set by the installation')
  expect(buttonByText(container, 'Edit who can use it')).toBeUndefined()
})

it('groups own, shared-with-you and admin-view accounts with the right controls', async () => {
  const container = await render(<ProviderAccounts provider="claude-code" />)
  expect(container.textContent).toContain('Your accounts')
  expect(container.textContent).toContain('Shared with 1 workflow, 0 Crews, 2 people')
  expect(container.textContent).toContain('Shared with you')
  expect(container.textContent).toContain('Shared by Dana')
  expect(container.textContent).toContain('Native tools off')
  expect(container.querySelector('[aria-label="Remove Dana team"]')).toBeNull()
  expect(container.querySelector('[aria-label="Sharing for Dana team"]')).toBeNull()
  expect(container.textContent).toContain("Other people's accounts")
  expect(container.textContent).toContain('Owner: Erin · Private')
  await click(container.querySelector('[aria-label="Remove Erin personal"]'))
  expect(llmConfigService.deleteProviderConnection).toHaveBeenCalledWith('acct-erin')
})

it('edits sharing on an own account', async () => {
  const container = await render(<ProviderAccounts provider="claude-code" />)
  await click(container.querySelector('[aria-label="Sharing for My Max"]'))
  expect(container.textContent).toContain(SHARING_WARNING)
  const crew = [...container.querySelectorAll('label')].find(label => label.textContent?.includes('Ops Crew'))?.querySelector('input')
  await click(crew)
  vi.mocked(llmConfigService.getProviderConnections).mockResolvedValue([server, { ...own, sharing: { mode: 'shared', workflows: ['wf-1'], crews: ['_users/bob/Chats/Work/projects/c1'], users: ['u-bob', 'u-carol'] } }])
  await click(buttonByText(container, 'Save sharing'))
  expect(llmConfigService.updateProviderConnection).toHaveBeenCalledWith('acct-own', { sharing: { mode: 'shared', workflows: ['wf-1'], crews: ['_users/bob/Chats/Work/projects/c1'], users: ['u-bob', 'u-carol'] } })
  expect(container.textContent).toContain('Shared with 1 workflow, 1 Crew, 2 people')
})

it('adds an account shared with a workflow after showing the billing warning', async () => {
  vi.mocked(llmConfigService.addProviderConnection).mockResolvedValue({ id: 'acct-new', provider: 'claude-code', display_name: 'Team key', scope: 'user', auth_method: 'api_key' })
  const container = await render(<ProviderAccounts provider="claude-code" />)
  await click(buttonByText(container, 'Add account'))
  expect(container.textContent).not.toContain(SHARING_WARNING)
  await setValue(container.querySelector<HTMLInputElement>('input[placeholder="e.g. Personal account"]')!, 'Team key')
  await setValue(container.querySelector<HTMLInputElement>('input[type="password"]')!, 'sk-token')
  const sharedRadio = [...container.querySelectorAll('label')].find(label => label.textContent?.includes('Shared with workflows, Crews and people'))?.querySelector('input')
  await click(sharedRadio)
  expect(container.textContent).toContain(SHARING_WARNING)
  const workflow = [...container.querySelectorAll('label')].find(label => label.textContent?.trim() === 'Research')?.querySelector('input')
  await click(workflow)
  await act(async () => { container.querySelector('form')!.requestSubmit() })
  expect(llmConfigService.addProviderConnection).toHaveBeenCalledWith({ provider: 'claude-code', display_name: 'Team key', credential: 'sk-token', sharing: { mode: 'shared', workflows: ['wf-1'], crews: [], users: [] } })
})

it('shows server validation text when saving fails', async () => {
  vi.mocked(llmConfigService.updateProviderConnection).mockRejectedValue({ response: { status: 400, data: 'sharing names a workflow you cannot open' } })
  const container = await render(<ProviderAccounts provider="claude-code" />)
  await click(container.querySelector('[aria-label="Sharing for My Max"]'))
  await click(buttonByText(container, 'Save sharing'))
  expect(container.querySelector('[role="alert"]')?.textContent).toBe('sharing names a workflow you cannot open')
})

it('runs usage for the chosen account and shows it inline', async () => {
  vi.mocked(llmConfigService.checkProviderUsage).mockResolvedValue({ session: { id: 'usage-1', provider: 'claude-code', action: 'usage', status: 'running', created_at: '', updated_at: '' } })
  const container = await render(<ProviderAccounts provider="claude-code" />)
  await click(container.querySelector('[aria-label="Usage for Dana team"]'))
  expect(llmConfigService.checkProviderUsage).toHaveBeenCalledWith('claude-code', 'acct-dana')
  expect(container.querySelector('[data-testid="guided-terminal"]')?.textContent).toBe('Terminal usage-1')
})

it('shows server-collected usage text, with no terminal, for an account the viewer does not manage', async () => {
  vi.mocked(llmConfigService.checkProviderUsage).mockResolvedValue({ usage_output: 'Plan: Max · resets 5pm' })
  const container = await render(<ProviderAccounts provider="claude-code" />)
  await click(container.querySelector('[aria-label="Usage for Dana team"]'))
  expect(container.querySelector('[aria-label="Usage output for Dana team"]')?.textContent).toBe('Plan: Max · resets 5pm')
  expect(container.querySelector('[data-testid="guided-terminal"]')).toBeNull()
})

it('picker lists usable accounts in groups and keeps an unavailable selection', async () => {
  const container = await render(<ProviderAccounts provider="claude-code" selectionOnly workspacePath="Workflow/research" selectedId="acct-erin" onSelect={vi.fn()} />)
  expect(llmConfigService.getProviderConnections).toHaveBeenCalledWith({ workspacePath: 'Workflow/research', product: undefined })
  const select = container.querySelector<HTMLSelectElement>('select[aria-label="Provider account"]')!
  expect([...select.querySelectorAll('optgroup')].map(group => group.label)).toEqual(['Server account', 'Your accounts', 'Shared with you'])
  expect(select.value).toBe('acct-erin')
  expect(select.options[0].textContent).toBe('Erin personal (no longer available here)')
  expect(select.textContent).toContain('Dana team (Dana) · native tools off')
})
