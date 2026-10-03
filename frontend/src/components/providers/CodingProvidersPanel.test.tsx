// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../../utils/productWorkspaceNavigation', () => ({ openProductWorkspace: vi.fn() }))
vi.mock('../../services/llm-config-api', () => ({
  llmConfigService: { getProviderManifest: vi.fn(), getProviderModels: vi.fn(), startProviderSetup: vi.fn(), cancelProviderSetup: vi.fn(), getProviderConnections: vi.fn(), getProviderAccountCosts: vi.fn(async () => ({ providers: [] })), getProductDefaults: vi.fn(async () => ({})), setProductDefaults: vi.fn() },
  providerApiErrorText: (_error: unknown, fallback: string) => fallback,
}))
const auth = vi.hoisted(() => ({ state: { isMultiUserMode: false, isMultiUserModeChecked: true, user: null as { is_admin?: boolean; is_code_reviewer?: boolean } | null } }))
vi.mock('../../stores/useAuthStore', () => ({
  useAuthStore: (selector: (state: typeof auth.state) => unknown) => selector(auth.state),
}))
vi.mock('./ConversationsOverview', () => ({ default: () => <div>Conversation review content</div> }))

vi.mock('./GuidedProviderTerminal', () => ({
  default: ({ session }: { session: { id: string } }) => <div data-testid="guided-terminal">Terminal {session.id}</div>,
}))

import { llmConfigService, type ProviderManifestEntry } from '../../services/llm-config-api'
import CodingProvidersPanel from './CodingProvidersPanel'
import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'
import { CODING_PROVIDER_GUIDES } from './codingProviderGuides'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

beforeEach(() => {
  auth.state = { isMultiUserMode: false, isMultiUserModeChecked: true, user: null }
  vi.mocked(llmConfigService.getProviderConnections).mockResolvedValue([])
  vi.mocked(llmConfigService.getProviderModels).mockImplementation(async providerId => ({
    provider: providerId,
    model_selection_mode: 'fixed_tier',
    models: [],
    source: 'test',
  }))
})

const provider = (overrides: Partial<ProviderManifestEntry>): ProviderManifestEntry => ({
  id: 'codex-cli',
  display_name: 'Codex CLI',
  description: 'OpenAI coding agent',
  kind: 'local_cli',
  integration_kind: 'coding_agent',
  model_selection_mode: 'dynamic',
  auth_description: 'Codex login',
  runtime_command: 'codex',
  runtime_available: true,
  auth_configured: true,
  auth_source: 'Codex home',
  usable: true,
  requires_api_key: false,
  supports_dynamic_models: true,
  default_model_id: 'gpt-5.3-codex',
  models: [],
  capabilities: ['live_input', 'interrupt'],
  ...overrides,
})

afterEach(() => {
  vi.resetAllMocks()
  document.body.innerHTML = ''
  document.body.style.overflow = ''
})

describe('CodingProvidersPanel', () => {
  it.each(['member', 'reviewer', 'admin', 'unknown'] as const)('gates review navigation and account costs for %s', async role => {
    auth.state = { isMultiUserMode: role !== 'unknown', isMultiUserModeChecked: role !== 'unknown', user: { is_admin: role === 'admin', is_code_reviewer: role === 'reviewer' } }
    vi.mocked(llmConfigService.getProviderManifest).mockResolvedValue({ providers: [provider({})], provider_order: ['codex-cli'], integration_kinds: {} })
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<CodingProvidersPanel isOpen embedded onClose={vi.fn()} />))
      const canReview = role === 'admin' || role === 'reviewer'
      expect(Boolean(host.querySelector('[data-tour="providers-costs"]'))).toBe(canReview)
      expect(Boolean(host.querySelector('[data-tour="providers-chats"]'))).toBe(canReview)
      expect(host.textContent?.includes('Cost by account')).toBe(canReview)
      if (canReview) {
        await act(async () => host.querySelector<HTMLButtonElement>('[data-tour="providers-chats"]')!.click())
        expect(host.textContent).toContain('Conversation review content')
        auth.state.user = {}
        await act(async () => root.render(<CodingProvidersPanel isOpen embedded onClose={vi.fn()} />))
        expect(host.textContent).not.toContain('Conversation review content')
        expect(host.querySelector('[data-tour="providers-costs"]')).toBeNull()
      } else {
        expect(llmConfigService.getProviderAccountCosts).not.toHaveBeenCalled()
      }
    } finally { await act(async () => root.unmount()); host.remove() }
  })

  it('offers the current account and costs walkthrough on demand and keeps Providers open', async () => {
    vi.mocked(llmConfigService.getProviderManifest).mockResolvedValue({ providers: [provider({})], provider_order: ['codex-cli'], integration_kinds: {} })
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    const onClose = vi.fn()
    try {
      await act(async () => root.render(<CodingProvidersPanel isOpen onClose={onClose} />))
      expect(document.querySelector('[aria-label="Providers walkthrough"]')).toBeNull()
      // Give the tour targets visible bounds as in the real browser.
      for (const target of document.querySelectorAll<HTMLElement>('[data-tour], [aria-label="Refresh provider status"]')) {
        target.getBoundingClientRect = () => ({ left: 20, top: 20, right: 220, bottom: 50, width: 200, height: 30 }) as DOMRect
      }
      await act(async () => document.querySelector<HTMLButtonElement>('[aria-label="Providers help & walkthrough"]')!.click())
      const tour = document.querySelector('[aria-label="Providers walkthrough"]')!
      expect(tour.textContent).toContain('Runs on')
      for (const text of ['Choose a provider', 'Your accounts and shared accounts', 'Costs across your work', 'Refresh connection status']) {
        await act(async () => tour.querySelector<HTMLButtonElement>('[data-testid="workflow-walkthrough-next"]')!.click())
        expect(tour.textContent).toContain(text)
      }
      await act(async () => window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' })))
      expect(document.querySelector('[aria-label="Providers walkthrough"]')).toBeNull()
      expect(onClose).not.toHaveBeenCalled()
      await act(async () => window.dispatchEvent(new Event('open-providers-walkthrough')))
      expect(document.querySelector('[aria-label="Providers walkthrough"]')?.textContent).toContain('Connect the account your work runs on')
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it('defines user-facing authentication guidance without server commands', () => {
    expect(Object.keys(CODING_PROVIDER_GUIDES)).toEqual([
      'claude-code',
      'codex-cli',
      'cursor-cli',
      'pi-cli',
      'muse-cli',
      'agy-cli',
    ])
    for (const guide of Object.values(CODING_PROVIDER_GUIDES)) {
      expect(guide.authenticateNote).toBeTruthy()
      expect(guide.authenticateNote).not.toContain('npm install')
      expect(guide.authenticateNote).not.toContain('curl ')
    }
  })

  it('shows coding agents only, with sign-in on the accounts and no setup steps or usage check', async () => {
    vi.mocked(llmConfigService.startProviderSetup).mockResolvedValue({
      id: 'usage-1', provider: 'codex-cli', action: 'usage', status: 'running',
    } as Awaited<ReturnType<typeof llmConfigService.startProviderSetup>>)
    vi.mocked(llmConfigService.getProviderManifest).mockResolvedValue({
      providers: [
        provider({}),
        provider({
          id: 'claude-code',
          display_name: 'Claude Code',
          runtime_command: 'claude',
          runtime_available: false,
          auth_configured: false,
          auth_source: undefined,
          usable: false,
          setup_hint: 'Install Claude Code',
        }),
        provider({
          id: 'agy-cli',
          display_name: 'Antigravity CLI (Alpha)',
          runtime_command: 'agy',
        }),
        provider({
          id: 'openai',
          display_name: 'OpenAI API',
          kind: 'api',
          integration_kind: 'api_model',
        }),
      ],
      provider_order: ['claude-code', 'codex-cli', 'agy-cli', 'openai'],
      integration_kinds: {},
    })

    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<CodingProvidersPanel isOpen onClose={vi.fn()} />))
      await act(async () => Promise.resolve())

      const dialog = document.querySelector('[role="dialog"]')!
      expect(dialog.getAttribute('aria-label')).toBe('Providers')
      expect(dialog.querySelector(':scope > header')).toBeNull()
      expect(dialog.textContent).toContain('Available providers')
      expect(dialog.textContent).toContain('Claude Code')
      expect(dialog.textContent).toContain('Codex')
      expect(dialog.textContent).toContain('Antigravity CLI (Alpha)')
      expect(dialog.textContent).not.toContain('OpenAI API')
      expect(dialog.textContent).toContain('Not installed')
      expect(dialog.textContent).toContain('not installed on this server')
      expect(dialog.textContent).not.toContain('npm install')
      // Signing in happens on the account rows; the page has no setup steps or usage check.
      for (const gone of ['CLI availability', 'Verify the installation', 'Use in a workflow', 'Check usage', 'Product defaults']) {
        expect(dialog.textContent).not.toContain(gone)
      }

      await act(async () => Array.from(dialog.querySelectorAll('button')).find(button => button.textContent?.includes('Codex'))!.click())
      expect(dialog.querySelector('[aria-label="Codex CLI is connected"]')).not.toBeNull()
      expect(dialog.textContent).not.toContain('Check usage')
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it('keeps a guided session mounted when leaving the Providers page, without modal Escape or cancellation', async () => {
    vi.mocked(llmConfigService.getProviderManifest).mockResolvedValue({ providers: [provider({ id: 'pi-cli', display_name: 'Pi', runtime_command: 'pi' })], provider_order: ['pi-cli'], integration_kinds: {} })
    vi.mocked(llmConfigService.startProviderSetup).mockResolvedValue({ id: 'setup-1', provider: 'pi-cli', action: 'authenticate', status: 'running' } as Awaited<ReturnType<typeof llmConfigService.startProviderSetup>>)
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    const onClose = vi.fn()
    const renderPage = (isOpen: boolean) => <div hidden={!isOpen}><CodingProvidersPanel embedded isOpen={isOpen} onClose={onClose} /></div>
    try {
      await act(async () => root.render(renderPage(true)))
      expect(host.querySelector('[role="dialog"]')).toBeNull()
      expect(host.querySelector('[role="region"]')).not.toBeNull()
      expect(document.body.style.overflow).toBe('')
      await act(async () => Array.from(host.querySelectorAll('button')).find(b => b.textContent?.includes('Manage connections'))!.click())
      const terminal = host.querySelector('[data-testid="guided-terminal"]')
      expect(terminal?.textContent).toBe('Terminal setup-1')
      await act(async () => window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' })))
      expect(onClose).not.toHaveBeenCalled()
      await act(async () => Array.from(host.querySelectorAll('header button')).find(button => button.textContent?.trim() === 'Back to Goals')!.dispatchEvent(new MouseEvent('click', { bubbles: true })))
      expect(onClose).toHaveBeenCalledOnce()
      expect(openProductWorkspace).toHaveBeenCalledWith('agentworks')
      await act(async () => root.render(renderPage(false)))
      expect(host.querySelector('[data-testid="guided-terminal"]')).toBe(terminal)
      await act(async () => root.render(renderPage(true)))
      expect(host.querySelector('[data-testid="guided-terminal"]')).toBe(terminal)
      expect(llmConfigService.cancelProviderSetup).not.toHaveBeenCalled()
    } finally { await act(async () => root.unmount()); host.remove() }
  })

  it('offers to replace a setup session after a conflict', async () => {
    vi.mocked(llmConfigService.getProviderManifest).mockResolvedValue({ providers: [provider({ id: 'pi-cli', display_name: 'Pi', runtime_command: 'pi' })], provider_order: ['pi-cli'], integration_kinds: {} })
    vi.mocked(llmConfigService.startProviderSetup)
      .mockRejectedValueOnce({ response: { status: 409, data: { error: 'a pi-cli setup session is already running' } } })
      .mockResolvedValueOnce({ id: 'setup-2', provider: 'pi-cli', action: 'authenticate', status: 'running' } as Awaited<ReturnType<typeof llmConfigService.startProviderSetup>>)
    Object.defineProperty(window, 'confirm', { configurable: true, value: vi.fn(() => true) })
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    try {
      await act(async () => root.render(<CodingProvidersPanel isOpen onClose={vi.fn()} />))
      await act(async () => Promise.resolve())
      await act(async () => Array.from(document.querySelectorAll('button')).find(button => button.textContent?.includes('Manage connections'))!.click())
      await act(async () => Promise.resolve())

      const replaceButton = Array.from(document.querySelectorAll('button')).find(button => button.textContent?.includes('End existing session and start new'))
      expect(replaceButton).toBeTruthy()
      await act(async () => replaceButton!.click())
      await act(async () => Promise.resolve())

      expect(window.confirm).toHaveBeenCalled()
      expect(llmConfigService.startProviderSetup).toHaveBeenLastCalledWith('pi-cli', 'authenticate', 100, 24, undefined, true)
      expect(document.querySelector('[data-testid="guided-terminal"]')?.textContent).toBe('Terminal setup-2')
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it('expects deployment-installed Muse and offers guided sign-in only', async () => {
    vi.mocked(llmConfigService.getProviderManifest).mockResolvedValue({
      providers: [
        provider({
          id: 'muse-cli',
          display_name: 'Muse Code',
          runtime_command: 'muse',
          runtime_available: false,
          install_command: "curl --proto '=https' --proto-redir '=https' --tlsv1.2 https://dev.meta.ai/install.sh | bash",
          auth_configured: false,
          auth_source: undefined,
          usable: false,
          setup_hint: 'Install Muse Code',
        }),
      ],
      provider_order: ['muse-cli'],
      integration_kinds: {},
    })

    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<CodingProvidersPanel isOpen onClose={vi.fn()} />))
      await act(async () => Promise.resolve())

      const dialog = document.querySelector('[role="dialog"]')!
      expect(dialog.textContent).toContain('Muse Code')
      expect(dialog.textContent).toContain('not installed on this server')
      expect(dialog.textContent).toContain("curl --proto '=https' --proto-redir '=https' --tlsv1.2 https://dev.meta.ai/install.sh | bash")

      vi.mocked(llmConfigService.getProviderManifest).mockResolvedValue({
        providers: [provider({
          id: 'muse-cli',
          display_name: 'Muse Code',
          runtime_command: 'muse',
          runtime_available: true,
          auth_configured: false,
          auth_source: undefined,
          usable: false,
        })],
        provider_order: ['muse-cli'],
        integration_kinds: {},
      })
      const refresh = Array.from(dialog.querySelectorAll('button')).find(button => button.getAttribute('aria-label') === 'Refresh provider status')!
      await act(async () => refresh.click())
      await act(async () => Promise.resolve())

      // Installed now: the notice goes; signing in is on the account rows.
      expect(dialog.textContent).not.toContain('not installed on this server')
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it('shows the installed CLI version and warns when it is below the supported floor', async () => {
    vi.mocked(llmConfigService.getProviderManifest).mockResolvedValue({
      providers: [
        provider({ installed_version: '0.155.1', min_supported_version: '0.155.1', update_status: 'supported' }),
        provider({
          id: 'claude-code',
          display_name: 'Claude Code',
          runtime_command: 'claude',
          installed_version: '2.1.0',
          min_supported_version: '2.1.278',
          update_status: 'unsupported',
        }),
      ],
      provider_order: ['codex-cli', 'claude-code'],
      integration_kinds: {},
    })

    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<CodingProvidersPanel isOpen onClose={vi.fn()} />))
      await act(async () => Promise.resolve())

      const dialog = document.querySelector('[role="dialog"]')!
      expect(dialog.textContent).toContain('CLI version 0.155.1')
      expect(dialog.textContent).not.toContain('below the minimum supported')

      await act(async () => Array.from(dialog.querySelectorAll('button')).find(button => button.textContent?.includes('Claude Code'))!.click())
      expect(dialog.textContent).toContain('CLI version 2.1.0')
      expect(dialog.textContent).toContain('below the minimum supported version 2.1.278')
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it('renders a provider without a guide entry instead of crashing', async () => {
    vi.mocked(llmConfigService.getProviderManifest).mockResolvedValue({
      providers: [
        provider({
          id: 'future-cli',
          display_name: 'Future CLI',
          runtime_command: 'future',
          installed_version: '9.9.9',
          min_supported_version: '9.0.0',
          update_status: 'supported',
          auth_configured: false,
          auth_source: undefined,
          usable: false,
        }),
      ],
      provider_order: ['future-cli'],
      integration_kinds: {},
    })

    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<CodingProvidersPanel isOpen onClose={vi.fn()} />))
      await act(async () => Promise.resolve())

      const dialog = document.querySelector('[role="dialog"]')!
      expect(dialog.textContent).toContain('Future CLI')
      expect(dialog.textContent).toContain('CLI version 9.9.9')
      expect(dialog.textContent).toContain('Guided sign-in for this provider is not available yet')
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })
})
