// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { SavedLLM } from '../../services/api-types'
import type { ProviderManifestEntry } from '../../services/llm-config-api'

const { storeState } = vi.hoisted(() => ({
  storeState: {
    availableLLMs: [],
    providerManifest: [] as ProviderManifestEntry[],
    providerManifestLoaded: true,
    loadProviderManifest: vi.fn(),
    defaultsLoaded: true,
    loadDefaultsFromBackend: vi.fn(),
    getProviderDynamicModels: vi.fn(),
    isProviderSupported: vi.fn(() => true),
    llmConfigLocked: false,
    lockedProviders: [],
    savedLLMs: [] as SavedLLM[],
    setShowLLMModal: vi.fn(),
  },
}))

vi.mock('../../stores/useLLMStore', () => ({
  useLLMStore: (selector: (state: typeof storeState) => unknown) => selector(storeState),
}))
vi.mock('../../hooks/useCanWriteWorkflow', () => ({
  READ_ONLY_TITLE: 'Read only',
  useCanWriteWorkflow: () => true,
}))
vi.mock('../../services/llm-config-api', () => ({
  llmConfigService: { getModelMetadata: vi.fn(async () => ({ models: [] })), getProviderConnections: vi.fn(async () => [] as import('../../services/llm-config-api').ProviderConnection[]) },
}))
vi.mock('../llm/CodingAgentSection', () => ({ CodingAgentSection: () => null }))
vi.mock('../WorkflowProviderCredentialField', () => ({ WorkflowProviderCredentialField: () => null }))

import WorkflowLLMConfigurationPanel from './WorkflowLLMConfigurationPanel'
import { llmConfigService } from '../../services/llm-config-api'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const provider = (overrides: Partial<ProviderManifestEntry>): ProviderManifestEntry => ({
  id: 'claude-code',
  display_name: 'Claude Code',
  description: 'Claude coding agent',
  kind: 'local_cli',
  integration_kind: 'coding_agent',
  model_selection_mode: 'fixed_tier',
  auth_description: 'Claude login',
  runtime_command: 'claude',
  runtime_available: true,
  auth_configured: true,
  usable: true,
  requires_api_key: false,
  supports_dynamic_models: false,
  default_model_id: 'claude-sonnet',
  default_tier_models: {
    high: { provider: 'claude-code', model_id: 'claude-sonnet' },
    medium: { provider: 'claude-code', model_id: 'claude-sonnet' },
    low: { provider: 'claude-code', model_id: 'claude-sonnet' },
  },
  models: [],
  capabilities: [],
  ...overrides,
})

afterEach(() => {
  storeState.providerManifest = []
  storeState.llmConfigLocked = false
  storeState.savedLLMs = []
  storeState.setShowLLMModal.mockReset()
  vi.mocked(llmConfigService.getProviderConnections).mockResolvedValue([])
  document.body.innerHTML = ''
})

describe('WorkflowLLMConfigurationPanel coding-agent rows', () => {
  it('shows authenticated CLIs as connected and keeps testing out of workflow selection', async () => {
    storeState.providerManifest = [
      provider({}),
      provider({
        id: 'codex-cli',
        display_name: 'OpenAI Codex CLI',
        runtime_command: 'codex',
        auth_configured: false,
        usable: false,
        setup_hint: 'Sign in from Providers',
        default_model_id: 'codex',
        default_tier_models: {
          high: { provider: 'codex-cli', model_id: 'codex' },
          medium: { provider: 'codex-cli', model_id: 'codex' },
          low: { provider: 'codex-cli', model_id: 'codex' },
        },
      }),
    ]

    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(
        <WorkflowLLMConfigurationPanel workspacePath="/workflow" onChange={vi.fn()} />,
      ))
      await act(async () => Promise.resolve())

      expect(host.textContent).toContain('Claude Code')
      expect(host.textContent).toContain('Connected')
      expect(host.textContent).toContain('OpenAI Codex CLI')
      expect(host.textContent).toContain('Needs setup')
      expect(Array.from(host.querySelectorAll('button')).some(button => button.textContent?.trim() === 'Test')).toBe(false)
      expect(Array.from(host.querySelectorAll('button')).filter(button => button.textContent?.trim() === 'Use')).toHaveLength(1)
      const setupButton = Array.from(host.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Manage in Providers')
      expect(setupButton).toBeDefined()
      await act(async () => setupButton?.click())
      expect(storeState.setShowLLMModal).toHaveBeenCalledWith(true)
      expect(host.textContent).not.toContain('Add a private account')
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it('can show the complete coding CLI set while keeping Pi as one provider row', async () => {
    storeState.getProviderDynamicModels.mockResolvedValue({ models: [], groups: [] })
    storeState.providerManifest = [
      provider({}),
      provider({ id: 'codex-cli', display_name: 'OpenAI Codex CLI' }),
      provider({ id: 'cursor-cli', display_name: 'Cursor CLI' }),
      provider({ id: 'pi-cli', display_name: 'Pi CLI', supports_dynamic_models: true, model_selection_mode: 'dynamic' }),
      provider({ id: 'muse-cli', display_name: 'Muse' }),
      provider({ id: 'agy-cli', display_name: 'Antigravity CLI (Alpha)', runtime_command: 'agy', default_model_id: 'gemini-3.8-flash-high' }),
    ]

    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(
        <WorkflowLLMConfigurationPanel
          workspacePath="/work-project"
          onChange={vi.fn()}
          allowedProviderIds={['claude-code', 'codex-cli', 'cursor-cli', 'pi-cli', 'muse-cli', 'agy-cli']}
          splitPiProviders={false}
        />,
      ))
      await act(async () => Promise.resolve())

      expect(host.textContent).toContain('Claude Code')
      expect(host.textContent).toContain('OpenAI Codex CLI')
      expect(host.textContent).toContain('Cursor CLI')
      expect(host.textContent).toContain('Pi CLI')
      expect(host.textContent).toContain('Muse')
      expect(host.textContent).toContain('Antigravity CLI (Alpha)')
      expect(Array.from(host.querySelectorAll('button')).filter(button => button.textContent?.trim() === 'Use')).toHaveLength(6)
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it('keeps a project profile selection editable under the global workflow lock', async () => {
    storeState.llmConfigLocked = true
    storeState.savedLLMs = [{ id: 'default', name: 'Cursor', provider: 'cursor-cli', model_id: 'auto' }]
    storeState.providerManifest = [
      provider({}),
      provider({ id: 'cursor-cli', display_name: 'Cursor CLI', default_model_id: 'auto' }),
    ]

    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(
        <WorkflowLLMConfigurationPanel
          workspacePath="/work-project"
          llmConfig={{ schema_version: 2, mode: 'provider_profile', provider: 'claude-code' }}
          onChange={vi.fn()}
          scopeNoun="project"
          canWriteOverride
          allowedProviderIds={['claude-code', 'cursor-cli']}
          configurationSource="agent_profile"
        />,
      ))
      await act(async () => Promise.resolve())

      expect(host.textContent).toContain('This project runs onClaude Code')
      expect(host.textContent).not.toContain('Set by your administrator')
      expect(Array.from(host.querySelectorAll('button')).some(button => button.textContent?.trim() === 'Change provider')).toBe(true)
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })
})


describe('workflow account tree', () => {
  it('uses the selected private account when shared authentication is absent and persists another child account', async () => {
    storeState.providerManifest = [provider({ usable: false, auth_configured: false })]
    vi.mocked(llmConfigService.getProviderConnections).mockResolvedValue([
      { id: 'global:claude-code', provider: 'claude-code', display_name: 'Server account', scope: 'global', auth_method: 'server' },
      { id: 'account-a', provider: 'claude-code', display_name: 'Personal A', scope: 'user', auth_method: 'api_key' },
      { id: 'account-b', provider: 'claude-code', display_name: 'Personal B', scope: 'user', auth_method: 'api_key' },
    ])
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host); const persist = vi.fn()
    try {
      await act(async () => root.render(<WorkflowLLMConfigurationPanel workspacePath="/workflow" llmConfig={{ schema_version: 2, mode: 'provider_profile', provider: 'claude-code', connection_id: 'account-b' }} onChange={vi.fn()} onUseProvider={persist} />))
      await act(async () => Promise.resolve())
      expect(host.textContent).not.toContain('Needs setup')
      expect(host.textContent).toContain('Personal B (private)')
      const providerBranch = host.querySelector<HTMLButtonElement>('[aria-label="Show Claude Code accounts"]')?.parentElement?.parentElement
      expect(providerBranch?.textContent).toContain('Claude Code')
      expect(providerBranch?.textContent).toContain('Personal B')
      const selected = host.querySelector<HTMLButtonElement>('[aria-label="Use Claude Code account Personal B"]')
      expect(selected?.textContent).toBe('Selected'); expect(selected?.disabled).toBe(true)
      await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Use Claude Code account Personal A"]')?.click())
      expect(persist).toHaveBeenCalledWith(expect.objectContaining({ provider: 'claude-code', connection_id: 'account-a' }))
      expect(Array.from(host.querySelectorAll('button')).some(button => button.textContent?.trim() === 'Add account')).toBe(false)
      expect(host.querySelector('input[placeholder="e.g. Personal account"]')).toBeNull()
    } finally { await act(async () => root.unmount()); host.remove() }
  })

  it('does not substitute another available account for a deleted selected account', async () => {
    storeState.providerManifest = [provider({})]
    vi.mocked(llmConfigService.getProviderConnections).mockResolvedValue([{ id: 'other-account', provider: 'claude-code', display_name: 'Other account', scope: 'user', auth_method: 'api_key' }])
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host); const persist = vi.fn()
    try {
      await act(async () => root.render(<WorkflowLLMConfigurationPanel workspacePath="/workflow" llmConfig={{ schema_version: 2, mode: 'provider_profile', provider: 'claude-code', connection_id: 'deleted-account' }} onChange={vi.fn()} onUseProvider={persist} />))
      await act(async () => Promise.resolve())
      expect(host.textContent).toContain('Account unavailable')
      expect(host.textContent).toContain('The selected account is unavailable')
      expect(persist).not.toHaveBeenCalled()
    } finally { await act(async () => root.unmount()); host.remove() }
  })

  it('asks for accounts usable at this path, groups them, and keeps a no-longer-usable selection visible', async () => {
    storeState.providerManifest = [provider({})]
    vi.mocked(llmConfigService.getProviderConnections).mockResolvedValue([
      { id: 'global:claude-code', provider: 'claude-code', display_name: 'Server account', scope: 'global', auth_method: 'server', relation: 'server', kind: 'installed', usable: true },
      { id: 'own-a', provider: 'claude-code', display_name: 'Mine', scope: 'user', auth_method: 'api_key', relation: 'own', usable: true },
      { id: 'dana', provider: 'claude-code', display_name: 'Dana team', scope: 'user', auth_method: 'api_key', relation: 'shared_with_workflow', owner_name: 'Dana', usable: true },
      { id: 'erin', provider: 'claude-code', display_name: 'Erin old', scope: 'user', auth_method: 'api_key', relation: 'shared_with_you', owner_name: 'Erin', usable: false },
      { id: 'frank', provider: 'claude-code', display_name: 'Frank hidden', scope: 'user', auth_method: 'api_key', relation: 'shared_with_you', owner_name: 'Frank', usable: false },
      { id: 'admin-only', provider: 'claude-code', display_name: 'Other person', scope: 'user', auth_method: 'api_key', relation: 'admin_view', usable: false },
    ])
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host); const persist = vi.fn()
    try {
      await act(async () => root.render(<WorkflowLLMConfigurationPanel workspacePath="Workflow/research" llmConfig={{ schema_version: 2, mode: 'provider_profile', provider: 'claude-code', connection_id: 'erin' }} onChange={vi.fn()} onUseProvider={persist} />))
      await act(async () => Promise.resolve())
      expect(llmConfigService.getProviderConnections).toHaveBeenCalledWith({ workspacePath: 'Workflow/research', product: undefined })
      const tree = host.querySelector('[aria-label="Claude Code accounts"]')!
      expect(tree.textContent).toContain('Shared with this workflow')
      expect(tree.textContent).toContain('Dana team')
      expect(tree.textContent).not.toContain('Native tools off')
      expect(tree.textContent).toContain('Erin old')
      expect(tree.textContent).toContain('No longer available here')
      expect(tree.textContent).not.toContain('Frank hidden')
      expect(tree.textContent).not.toContain('Other person')
      expect(host.querySelector<HTMLButtonElement>('[aria-label="Use Claude Code account Erin old"]')?.disabled).toBe(true)
      expect(host.textContent).toContain('Account unavailable')
      expect(persist).not.toHaveBeenCalled()
      await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Use Claude Code account Dana team"]')?.click())
      expect(persist).toHaveBeenCalledWith(expect.objectContaining({ provider: 'claude-code', connection_id: 'dana' }))
    } finally { await act(async () => root.unmount()); host.remove() }
  })
})
