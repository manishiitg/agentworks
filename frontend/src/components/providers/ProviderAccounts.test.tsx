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
    getProviderAccountStatus: vi.fn(),
    signOutProviderAccount: vi.fn(),
    getProviderAccountCosts: vi.fn(),
    getProviderModels: vi.fn(),
    setAccountAllowedModels: vi.fn(),
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
  kind: 'user', relation: 'shared_with_you', owner_name: 'Dana', usable: true, can_manage: false, can_view_usage: true,
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
  vi.mocked(llmConfigService.getProviderAccountStatus).mockImplementation(async (id: string) => (
    id === 'acct-own' ? { state: 'signed_in', identity: 'me@x.com', verified: false, checked_at: '' } : { state: 'signed_out', verified: false, checked_at: '' }
  ))
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
// Less common actions live in each row's "More" menu.
const menuItem = async (container: HTMLElement, menu: string, item: string) => {
  await click(container.querySelector(`[aria-label="${menu}"]`))
  return [...container.querySelectorAll('[role="menuitem"]')].find(element => element.textContent?.trim() === item)
}
const menuItems = async (container: HTMLElement, menu: string) => {
  await click(container.querySelector(`[aria-label="${menu}"]`))
  const items = [...container.querySelectorAll('[role="menuitem"]')].map(element => element.textContent?.trim())
  await click(container.querySelector(`[aria-label="${menu}"]`))
  return items
}
const setValue = async (input: HTMLInputElement | HTMLSelectElement, value: string) => {
  const prototype = input instanceof HTMLSelectElement ? HTMLSelectElement.prototype : HTMLInputElement.prototype
  await act(async () => {
    Object.getOwnPropertyDescriptor(prototype, 'value')!.set!.call(input, value)
    input.dispatchEvent(new Event(input instanceof HTMLSelectElement ? 'change' : 'input', { bubbles: true }))
  })
}

it('shows the server account with its origin and lets an admin change who can use it', async () => {
  const container = await render(<ProviderAccounts provider="claude-code" providerLabel="Claude Code" />)
  expect(container.textContent).toContain('Used by everyone')
  await click(await menuItem(container, 'More for the admin-managed account', 'Who can use it'))
  // Everyone / Only admins save at once, no Save button.
  await setValue(container.querySelector<HTMLSelectElement>('select[aria-label="Who can use the admin-managed account"]')!, 'admins')
  expect(llmConfigService.setServerAccountAvailability).toHaveBeenCalledWith('claude-code', 'admins')
})

it('says when the installation pins who can use the server account', async () => {
  vi.mocked(llmConfigService.getProviderConnections).mockResolvedValue([{ ...server, kind: 'admin', availability: { available_to: 'admins', text: 'Admins only', source: 'installation', pinned: true }, availability_editable: false }])
  const container = await render(<ProviderAccounts provider="claude-code" />)
  expect(container.textContent).toContain('set by the installation')
  expect(await menuItems(container, 'More for the admin-managed account')).not.toContain('Who can use it')
})

it('groups own, shared-with-you and admin-view accounts with the right controls', async () => {
  const container = await render(<ProviderAccounts provider="claude-code" />)
  expect(container.textContent).toContain('Your accounts')
  expect(container.textContent).toContain('Shared with 1 workflow, 0 Crews, 2 people')
  expect(container.textContent).toContain('Shared with you')
  expect(container.textContent).toContain('Shared by Dana')
  expect(container.textContent).not.toContain('Native tools off')
  const danaItems = await menuItems(container, 'More for Dana team')
  expect(danaItems).not.toContain('Remove')
  expect(danaItems).not.toContain('Who can use it')
  expect(container.textContent).toContain("Other people's accounts")
  expect(container.textContent).toContain('Owner: Erin · Private (only Erin can use it)')
  await click(await menuItem(container, 'More for Erin personal', 'Remove'))
  expect(llmConfigService.deleteProviderConnection).toHaveBeenCalledWith('acct-erin')
})

it('edits sharing on an own account', async () => {
  const container = await render(<ProviderAccounts provider="claude-code" />)
  await click(await menuItem(container, 'More for My Max', 'Who can use it'))
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
  await click(buttonByText(container, 'Add my account'))
  expect(container.textContent).not.toContain(SHARING_WARNING)
  // Browser login is the default (the person signs in with their own account); a key is a choice.
  const authentication = container.querySelector('select option[value="cli_login"]')!.parentElement as HTMLSelectElement
  expect(authentication.value).toBe('cli_login')
  await setValue(authentication, 'api_key')
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

it('offers a member only named colleagues, up to 10, when sharing (PLAT-715)', async () => {
  vi.mocked(llmConfigService.getProviderConnections).mockResolvedValue([{ ...server, can_manage: false }, own])
  const container = await render(<ProviderAccounts provider="claude-code" />)
  await click(await menuItem(container, 'More for My Max', 'Who can use it'))
  expect(container.textContent).toContain('Shared with named colleagues (up to 10)')
  expect(container.textContent).not.toContain('Shared with workflows, Crews and people')
})

it('shows server validation text when saving fails', async () => {
  vi.mocked(llmConfigService.updateProviderConnection).mockRejectedValue({ response: { status: 400, data: 'sharing names a workflow you cannot open' } })
  const container = await render(<ProviderAccounts provider="claude-code" />)
  await click(await menuItem(container, 'More for My Max', 'Who can use it'))
  await click(buttonByText(container, 'Save sharing'))
  expect(container.querySelector('[role="alert"]')?.textContent).toBe('sharing names a workflow you cannot open')
})

it('has no separate usage check: usage and sign-in are in the account terminal', async () => {
  vi.mocked(llmConfigService.startProviderSetup).mockResolvedValue({ id: 'term-1', provider: 'claude-code', action: 'inspect', status: 'running', created_at: '', updated_at: '' })
  const container = await render(<ProviderAccounts provider="claude-code" />)
  expect([...container.querySelectorAll('button')].some(button => button.textContent?.trim() === 'Usage')).toBe(false)
  await click(await menuItem(container, 'More for My Max', 'Terminal (sign in, check usage)'))
  expect(llmConfigService.startProviderSetup).toHaveBeenCalledWith('claude-code', 'inspect', 100, 24, undefined, false, 'acct-own')
  expect(container.querySelector('[data-testid="guided-terminal"]')?.textContent).toBe('Terminal term-1')
})

it('picker lists usable accounts in groups and keeps an unavailable selection', async () => {
  const container = await render(<ProviderAccounts provider="claude-code" selectionOnly workspacePath="Workflow/research" selectedId="acct-erin" onSelect={vi.fn()} />)
  expect(llmConfigService.getProviderConnections).toHaveBeenCalledWith({ workspacePath: 'Workflow/research', product: undefined })
  const select = container.querySelector<HTMLSelectElement>('select[aria-label="Provider account"]')!
  expect([...select.querySelectorAll('optgroup')].map(group => group.label)).toEqual(['Admin-managed account', 'Your accounts', 'Shared with you'])
  expect(select.value).toBe('acct-erin')
  expect(select.options[0].textContent).toBe('Erin personal (no longer available here)')
  expect(select.textContent).toContain('Dana team (Dana)')
})

it('shows each account\'s status and offers the per-account actions to managers only', async () => {
  const container = await render(<ProviderAccounts provider="claude-code" providerLabel="Claude Code" />)
  await act(async () => Promise.resolve())
  expect(container.querySelector('[aria-label="Status of My Max"]')?.textContent).toBe('Signed in as me@x.com')
  expect(await menuItems(container, 'More for My Max')).toContain('Terminal (sign in, check usage)')
  expect(await menuItems(container, 'More for the admin-managed account')).toContain('Terminal (sign in, check usage)')
  // The shared account is signed out, so its one button is Sign in.
  expect(buttonByText(container, 'Sign in')).toBeDefined()
  // Not a manager of Dana's account: no terminal, no sign-out.
  const dana = await menuItems(container, 'More for Dana team')
  expect(dana).not.toContain('Terminal (sign in, check usage)')
  expect(dana).not.toContain('Sign out')
  // API-key accounts have no sign-out.
  expect(await menuItems(container, 'More for Erin personal')).not.toContain('Sign out')
  vi.mocked(llmConfigService.getProviderAccountStatus).mockResolvedValue({ state: 'key_rejected', detail: '401 invalid token', verified: true, checked_at: '' })
  await click(await menuItem(container, 'More for My Max', 'Check sign-in again'))
  expect(llmConfigService.getProviderAccountStatus).toHaveBeenLastCalledWith('acct-own', true, undefined)
  expect(container.querySelector('[aria-label="Status of My Max"]')?.textContent).toBe('Login rejected: 401 invalid token')
})

it('confirms before signing out the server account', async () => {
  const container = await render(<ProviderAccounts provider="claude-code" providerLabel="Claude Code" />)
  await click(await menuItem(container, 'More for the admin-managed account', 'Sign out'))
  expect(window.confirm).toHaveBeenCalledWith('Every run that uses the Claude Code admin-managed account will stop working until someone signs in again.')
  expect(llmConfigService.signOutProviderAccount).toHaveBeenCalledWith('global:claude-code')
})

it('shows no native-tools badge on accounts the viewer does not own', async () => {
  const container = await render(<ProviderAccounts provider="claude-code" />)
  expect(container.textContent).not.toMatch(/native tools off/i)
  expect(container.textContent).toContain('Owner: Erin · Private (only Erin can use it)')
})

it('selection-only accounts hide setup choices and preserve a signed-out saved account as disabled', async () => {
  vi.mocked(llmConfigService.getProviderConnections).mockResolvedValue([
    { ...server, configured: false }, { ...own, configured: false }, sharedWithMe,
  ])
  const onSelect = vi.fn()
  const container = await render(<ProviderAccounts provider="claude-code" selectionOnly selectedId="acct-own" onSelect={onSelect} />)
  const select = container.querySelector<HTMLSelectElement>('select[aria-label="Provider account"]')!
  expect(select.value).toBe('acct-own')
  expect(select.selectedOptions[0].textContent).toContain('needs setup')
  expect(select.selectedOptions[0].disabled).toBe(true)
  expect([...select.options].filter(option => !option.disabled).map(option => option.value)).toEqual(['acct-dana'])
  expect(onSelect).not.toHaveBeenCalled()
})

const catalog = { provider: 'claude-code', model_selection_mode: 'static', source: 'test', models: [
  { model_id: 'gpt-5.3-codex', model_name: 'GPT-5.3 Codex' }, { model_id: 'gpt-5.5', model_name: 'GPT-5.5' },
] }

it('shows each account\'s models and lets an admin limit the admin-managed account', async () => {
  vi.mocked(llmConfigService.getProviderModels).mockResolvedValue(catalog)
  vi.mocked(llmConfigService.setAccountAllowedModels).mockResolvedValue(undefined)
  const container = await render(<ProviderAccounts provider="claude-code" providerLabel="Claude Code" />)
  expect(container.textContent).toContain('Models: All models')
  await click(await menuItem(container, 'More for the admin-managed account', 'Models'))
  await setValue(container.querySelector<HTMLSelectElement>('select[aria-label="Models allowed on this account"]')!, 'only')
  // Nothing picked yet: cannot save a limit that allows nothing.
  expect(buttonByText(container, 'Save models')?.disabled).toBe(true)
  await click(container.querySelector('fieldset[aria-label="Allowed models"] input[type="checkbox"]'))
  // The server list after the save (the page reloads accounts when one changes).
  vi.mocked(llmConfigService.getProviderConnections).mockResolvedValue([{ ...server, allowed_models: ['gpt-5.3-codex'] }, own, sharedWithMe, adminView])
  await click(buttonByText(container, 'Save models'))
  expect(llmConfigService.setAccountAllowedModels).toHaveBeenCalledWith('global:claude-code', ['gpt-5.3-codex'])
  // The server row names the models plainly (PLAT-714).
  expect(container.textContent).toContain('Models: gpt-5.3-codex only')
})

it('lets the owner set models on their own account, and sends [] for All models', async () => {
  vi.mocked(llmConfigService.getProviderModels).mockResolvedValue(catalog)
  vi.mocked(llmConfigService.getProviderConnections).mockResolvedValue([server, { ...own, allowed_models: ['gpt-5.5'] }])
  vi.mocked(llmConfigService.setAccountAllowedModels).mockResolvedValue(undefined)
  const container = await render(<ProviderAccounts provider="claude-code" providerLabel="Claude Code" />)
  expect(container.textContent).toContain('Models: 1 model')
  await click(await menuItem(container, 'More for My Max', 'Models'))
  const checked = [...container.querySelectorAll<HTMLInputElement>('fieldset[aria-label="Allowed models"] input')].filter(box => box.checked)
  expect(checked).toHaveLength(1)
  await setValue(container.querySelector<HTMLSelectElement>('select[aria-label="Models allowed on this account"]')!, 'all')
  await click(buttonByText(container, 'Save models'))
  expect(llmConfigService.setAccountAllowedModels).toHaveBeenCalledWith('acct-own', [])
})

it('offers no Models control for an account the caller does not manage', async () => {
  const container = await render(<ProviderAccounts provider="claude-code" providerLabel="Claude Code" />)
  expect(await menuItems(container, 'More for Dana team')).not.toContain('Models')
})
