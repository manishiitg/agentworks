// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

vi.mock('../services/llm-config-api', () => ({ llmConfigService: { getProviderManifest: vi.fn(), getProviderConnections: vi.fn() } }))
const session = vi.hoisted(() => ({ user: { id: 'person' } as { id: string }, workspaceId: 'local', token: undefined as string | undefined }))
vi.mock('../stores/useAuthStore', () => ({ useAuthStore: (select: (state: unknown) => unknown) => select({ user: session.user }) }))
vi.mock('../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: (select: (state: unknown) => unknown) => select({ activeWorkspaceId: session.workspaceId, profiles: [{ id: session.workspaceId, apiBaseUrl: 'https://example.test', token: session.token }] }) }))

import WelcomeAiReadiness from './WelcomeAiReadiness'
import { llmConfigService, type ProviderConnection, type ProviderManifestEntry } from '../services/llm-config-api'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let root: Root
let host: HTMLDivElement
const open = vi.fn()
const provider = (override: Partial<ProviderManifestEntry> = {}): ProviderManifestEntry => ({ id: 'codex-cli', display_name: 'Codex', integration_kind: 'coding_agent', runtime_available: true, auth_configured: true, usable: true, ...override } as ProviderManifestEntry)
const account = (override: Partial<ProviderConnection> = {}): ProviderConnection => ({ id: 'global:codex-cli', provider: 'codex-cli', display_name: 'Shared account', scope: 'global', auth_method: 'server', usable: true, configured: false, personal_accounts_allowed: true, relation: 'server', ...override })
const render = async () => { await act(async () => root.render(<WelcomeAiReadiness products={['work', 'agentworks', 'relays']} onSetup={open} />)) }
const button = (label: string) => [...host.querySelectorAll('button')].find(element => element.textContent?.trim() === label)!

beforeEach(() => {
  session.user = { id: 'person' }; session.workspaceId = 'local'; session.token = undefined
  vi.mocked(llmConfigService.getProviderManifest).mockResolvedValue({ providers: [provider()], provider_order: ['codex-cli'], integration_kinds: {} })
  vi.mocked(llmConfigService.getProviderConnections).mockResolvedValue([account()])
  host = document.createElement('div'); document.body.append(host); root = createRoot(host)
})
afterEach(async () => { await act(async () => root.unmount()); host.remove(); vi.resetAllMocks() })

it('prompts setup from actual accounts despite a configured server manifest or saved model default', async () => {
  await render()
  expect(host.textContent).toContain('First, connect an AI account')
  expect(host.querySelectorAll('ol li')).toHaveLength(3)
  expect(llmConfigService.getProviderConnections).toHaveBeenCalledWith({ product: 'work' })
  expect(llmConfigService.getProviderConnections).toHaveBeenCalledWith({ product: 'agentworks' })
  await act(async () => button('Set up my AI').click())
  expect(open).toHaveBeenCalledOnce()
})

it.each(['server', 'own', 'shared_with_you'] as const)('recognizes a ready %s account without changing its selection', async relation => {
  vi.mocked(llmConfigService.getProviderConnections).mockImplementation(async scope => scope?.product === 'work' ? [account({ configured: true, relation })] : [account({ usable: false })])
  await render()
  expect(host.textContent).toContain('Your AI account is ready')
  expect(button('Manage AI accounts')).toBeDefined()
  expect(host.textContent).toContain('Available for Crew.')
  const summary = host.querySelector('[aria-label="Available AI accounts"]')!
  expect(summary.textContent).toContain(relation === 'server' ? 'Company' : relation === 'own' ? 'Personal' : 'Shared with you')
  expect(summary.textContent).toContain('Codex')
  expect(host.textContent).not.toContain('Available for Crew, Goals')
  expect(open).not.toHaveBeenCalled()
})

it.each([{ usable: false, configured: true }, { relation: 'admin_view' as const, configured: true }])('does not mistake inaccessible accounts for readiness: %j', async override => {
  vi.mocked(llmConfigService.getProviderConnections).mockResolvedValue([account(override)])
  await render()
  expect(host.textContent).toContain('First, connect an AI account')
})

it('requires an installed runtime even with a signed-in account', async () => {
  vi.mocked(llmConfigService.getProviderManifest).mockResolvedValue({ providers: [provider({ runtime_available: false })], provider_order: [], integration_kinds: {} })
  vi.mocked(llmConfigService.getProviderConnections).mockResolvedValue([account({ configured: true })])
  await render()
  expect(host.textContent).toContain('First, connect an AI account')
})

it('keeps an unknown sign-in distinct from ready or missing', async () => {
  vi.mocked(llmConfigService.getProviderConnections).mockResolvedValue([account({ configured: undefined })])
  await render()
  expect(host.textContent).toContain('Sign-in status is not confirmed yet')
  expect(host.textContent).not.toContain('No ready account')
  expect(button('Review AI setup')).toBeDefined()
})

it('directs users to the administrator when personal accounts are locked', async () => {
  vi.mocked(llmConfigService.getProviderConnections).mockResolvedValue([account({ personal_accounts_allowed: false })])
  await render()
  expect(host.textContent).toContain('Ask your administrator to provide a shared account')
  expect(button('Set up my AI')).toBeUndefined()
  expect(button('Review AI setup')).toBeDefined()
})

it('keeps failures actionable without reporting the account is missing', async () => {
  vi.mocked(llmConfigService.getProviderConnections).mockRejectedValueOnce(new Error('offline'))
  await render()
  expect(host.textContent).toContain('Account status could not be checked')
  expect(host.textContent).not.toContain('No ready account')
  await act(async () => button('Retry').click())
  expect(host.textContent).toContain('First, connect an AI account')
})

it('refreshes the readiness card when accounts change on the settings page', async () => {
  await render()
  vi.mocked(llmConfigService.getProviderConnections).mockResolvedValue([account({ configured: true })])
  await act(async () => window.dispatchEvent(new Event('provider-connections-changed')))
  expect(host.textContent).toContain('Your AI account is ready')
  expect(host.querySelector('[role="dialog"]')).toBeNull()
})

it('ignores delayed account results after switching user and workspace', async () => {
  let resolveOld!: (accounts: ProviderConnection[]) => void
  const old = new Promise<ProviderConnection[]>(resolve => { resolveOld = resolve })
  vi.mocked(llmConfigService.getProviderConnections).mockReturnValue(old)
  await render()
  expect(host.textContent).toContain('Checking your AI accounts')
  session.user = { id: 'other' }; session.workspaceId = 'remote'
  vi.mocked(llmConfigService.getProviderConnections).mockResolvedValue([account()])
  await render()
  await act(async () => resolveOld([account({ configured: true })]))
  expect(host.textContent).toContain('First, connect an AI account')
  expect(host.textContent).not.toContain('Your AI account is ready')
})

it('rechecks live account changes without installing or signing in automatically', async () => {
  await render()
  vi.mocked(llmConfigService.getProviderConnections).mockResolvedValue([account({ configured: true })])
  await act(async () => window.dispatchEvent(new Event('provider-connections-changed')))
  expect(host.textContent).toContain('Your AI account is ready')
  expect(open).not.toHaveBeenCalled()
})


it('shows distinct ready Personal and Company providers once across products and hides unavailable accounts', async () => {
  vi.mocked(llmConfigService.getProviderManifest).mockResolvedValue({ providers: [provider(), provider({ id: 'claude-code', display_name: 'Claude Code' }), provider({ id: 'muse-cli', display_name: 'Muse' })], provider_order: [], integration_kinds: {} })
  vi.mocked(llmConfigService.getProviderConnections).mockResolvedValue([
    account({ id: 'personal', relation: 'own', scope: 'user', configured: true }),
    account({ id: 'company', provider: 'claude-code', relation: 'server', configured: true }),
    account({ id: 'admin-only', provider: 'muse-cli', relation: 'admin_view', configured: true }),
    account({ id: 'not-ready', provider: 'muse-cli', relation: 'own', configured: false }),
  ])
  await render()
  const summary = host.querySelector('[aria-label="Available AI accounts"]')!
  expect([...summary.querySelectorAll('dt')].map(item => item.textContent)).toEqual(['Personal', 'Company'])
  expect([...summary.querySelectorAll('dd')].map(item => item.textContent)).toEqual(['Codex', 'Claude Code'])
  expect(summary.textContent).not.toContain('Muse')
})
