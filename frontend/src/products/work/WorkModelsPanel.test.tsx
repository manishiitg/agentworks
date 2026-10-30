// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ProviderManifestEntry } from '../../services/llm-config-api'
import type { AgentProfileProviderOption } from '../../utils/agentProfileCapabilities'
import type { PresetLLMConfig } from '../../services/api-types'

const state = vi.hoisted(() => ({
  product: { profileId: 'work', profileVersion: 3 },
  providerManifest: [] as ProviderManifestEntry[],
  providerManifestLoaded: true,
  loadProviderManifest: vi.fn(),
  options: [] as AgentProfileProviderOption[],
  configurationProps: {} as { product?: string; accountUsage?: React.ReactNode; llmConfig?: PresetLLMConfig; onChange: (config: PresetLLMConfig) => void },
  chatTabs: { project: { metadata: { agentProfileEngine: 'muse-cli', agentProfileReasoningEffort: 'medium', agentProfileConnectionID: undefined as string | undefined } } },
}))
vi.mock('../../stores/useLLMStore', () => ({ useLLMStore: (selector: (value: typeof state) => unknown) => selector(state) }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: (selector: (value: typeof state) => unknown) => selector(state) }))
vi.mock('./projectProduct', async original => ({ ...await original<typeof import('./projectProduct')>(), useProjectProduct: () => state.product }))
vi.mock('../../components/workflow/WorkflowLLMConfigurationPanel', () => ({ default: (props: typeof state.configurationProps) => { state.configurationProps = props; return <div data-testid="provider-configuration">{props.accountUsage}</div> } }))
vi.mock('../../services/api', () => ({ getApiBaseUrl: () => '', getAuthToken: () => null, agentApi: {} }))
vi.mock('../../services/llm-config-api', () => ({
  llmConfigService: { getProviderConnections: vi.fn(async () => []), getProviderModels: vi.fn(async () => ({ models: [] })) },
}))
vi.mock('../../utils/agentProfileCapabilities', async importOriginal => ({
  ...await importOriginal<typeof import('../../utils/agentProfileCapabilities')>(),
  loadAgentProfileProviderOptions: vi.fn(async () => state.options),
}))

import { WorkModelsPanel as WorkModelsPanelInner } from './WorkModelsPanel'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TooltipProvider } from '../../components/ui/tooltip'
const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
function WorkModelsPanel(props: React.ComponentProps<typeof WorkModelsPanelInner>) { return <QueryClientProvider client={queryClient}><TooltipProvider><WorkModelsPanelInner {...props} /></TooltipProvider></QueryClientProvider> }
import { llmConfigService } from '../../services/llm-config-api'
import { agentApi } from '../../services/api'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let root: Root | undefined
afterEach(() => {
  act(() => root?.unmount()); root = undefined; document.body.innerHTML = ''
  vi.mocked(llmConfigService.getProviderModels).mockReset()
})

function museFixture() {
  state.options = [{
    id: 'muse-cli', provider: 'muse-cli', label: 'Muse', default: true,
    model_id: 'muse-spark-1.3-contributor', reasoning_efforts: ['medium', 'high', 'xhigh', 'max'],
    options: { reasoning_effort: 'max' },
  }]
  state.providerManifest = [{
    id: 'muse-cli', integration_kind: 'coding_agent', runtime_available: true, usable: true,
    models: ['muse-spark-1.3-contributor', 'muse-spark-1.3'].map(model_id => ({
      model_id, model_name: model_id, provider: 'muse-cli', context_window: 1000000,
      input_cost_per_1m: 0, output_cost_per_1m: 0,
    })),
  } as ProviderManifestEntry]
}

async function render(config?: PresetLLMConfig, profileId?: string) {
  const host = document.createElement('div'); document.body.append(host)
  root = createRoot(host)
  const onRuntimeChange = vi.fn()
  await act(async () => root?.render(<WorkModelsPanel tabId="project" workspacePath="/project" hideHeader
    projectLLMConfig={config} profileId={profileId} onRuntimeChange={onRuntimeChange} />))
  const expand = host.querySelector<HTMLButtonElement>('button[aria-expanded]')!
  await act(async () => expand.click())
  return { host, onRuntimeChange }
}

describe('project reasoning settings', () => {
  it('uses the explicit profile for account lookup outside a Crew or Code context', async () => {
    museFixture()
    state.product.profileId = 'work'
    vi.mocked(llmConfigService.getProviderConnections).mockClear()
    const host = document.createElement('div'); document.body.append(host)
    root = createRoot(host)
    await act(async () => root?.render(<WorkModelsPanel tabId="project" workspacePath="Chats/CapLayer"
      profileId="caplayer" profileVersion={0} accountProduct="mcp-gateway" onRuntimeChange={vi.fn()} />))
    expect(llmConfigService.getProviderConnections).toHaveBeenCalledWith({ workspacePath: 'Chats/CapLayer', product: 'mcp-gateway' })
  })
  it.each(['caplayer', 'knowledgebase'])('uses the explicit %s profile for accounts and preserves the selected account', async profileId => {
    museFixture(); state.product.profileId = 'work'
    state.chatTabs.project.metadata.agentProfileConnectionID = 'personal-muse'
    try {
      const { host, onRuntimeChange } = await render(undefined, profileId)
      expect(llmConfigService.getProviderConnections).toHaveBeenCalledWith({ workspacePath: '/project', product: profileId })
      expect(state.configurationProps.product).toBe(profileId)
      expect(state.configurationProps.llmConfig?.connection_id).toBe('personal-muse')
      const high = Array.from(host.querySelectorAll<HTMLButtonElement>('[aria-label="Reasoning effort"] button')).find(button => button.textContent === 'High')!
      await act(async () => high.click())
      expect(onRuntimeChange).toHaveBeenCalledWith(expect.objectContaining({ connectionId: 'personal-muse', reasoningEffort: 'high' }))
    } finally { state.chatTabs.project.metadata.agentProfileConnectionID = undefined }
  })
  it.each(['work', 'code'])('allows Muse effort changes in %s with the same saved account and model', async profileId => {
    museFixture(); state.product.profileId = profileId
    const { host, onRuntimeChange } = await render({ schema_version: 2, mode: 'explicit', builder_llm: {
      provider: 'muse-cli', model_id: 'muse-spark-1.3-contributor', connection_id: 'global:muse-cli',
      options: { reasoning_effort: 'max' },
    } })
    const levels = Array.from(host.querySelectorAll<HTMLButtonElement>('[aria-label="Reasoning effort"] button'))
    expect(levels.map(button => button.textContent)).toEqual(['Medium', 'High', 'Xhigh', 'Max'])
    expect(levels.find(button => button.textContent === 'Max')?.getAttribute('aria-pressed')).toBe('true')
    expect(onRuntimeChange).not.toHaveBeenCalled()
    await act(async () => levels.find(button => button.textContent === 'High')?.click())
    expect(onRuntimeChange).toHaveBeenCalledWith({
      engine: 'muse-cli', provider: 'muse-cli', modelId: 'muse-spark-1.3-contributor',
      connectionId: 'global:muse-cli', reasoningEffort: 'high',
    })
  })

  it('preserves saved effort over stale tab metadata when changing the model', async () => {
    museFixture()
    const { host, onRuntimeChange } = await render({ schema_version: 2, mode: 'explicit', builder_llm: {
      provider: 'muse-cli', model_id: 'muse-spark-1.3-contributor', connection_id: 'personal-muse',
      options: { reasoning_effort: 'high' },
    } })
    const high = Array.from(host.querySelectorAll<HTMLButtonElement>('[aria-label="Reasoning effort"] button')).find(button => button.textContent === 'High')!
    expect(high.getAttribute('aria-pressed')).toBe('true')
    const otherModel = Array.from(host.querySelectorAll('button')).find(button => button.querySelector('.font-medium')?.textContent === 'muse-spark-1.3')!
    await act(async () => otherModel.click())
    expect(onRuntimeChange).toHaveBeenCalledWith(expect.objectContaining({
      modelId: 'muse-spark-1.3', connectionId: 'personal-muse', reasoningEffort: 'high',
    }))
  })

  it('uses the profile effort default when the project has no saved effort or matching metadata', async () => {
    museFixture()
    state.chatTabs.project.metadata.agentProfileEngine = 'codex-cli'
    try {
      const { host } = await render()
      const max = Array.from(host.querySelectorAll<HTMLButtonElement>('[aria-label="Reasoning effort"] button')).find(button => button.textContent === 'Max')!
      expect(max.getAttribute('aria-pressed')).toBe('true')
    } finally { state.chatTabs.project.metadata.agentProfileEngine = 'muse-cli' }
  })

  it('keeps Antigravity effort tied to its model id', async () => {
    state.options = [{ id: 'agy-cli', provider: 'agy-cli', model_id: 'gemini-3.8-flash-high', reasoning_efforts: ['low', 'medium', 'high'] }]
    state.providerManifest = [{ id: 'agy-cli', integration_kind: 'coding_agent', runtime_available: true, usable: true, models: [] } as unknown as ProviderManifestEntry]
    const { host } = await render()
    expect(host.querySelector('[aria-label="Reasoning effort"]')).toBeNull()
  })
})

function cursorFixture() {
  state.options = [{ id: 'cursor-cli', provider: 'cursor-cli', model_id: 'composer-2.5' }]
  state.providerManifest = [{ id: 'cursor-cli', integration_kind: 'coding_agent', runtime_available: true, usable: true, models: [] } as unknown as ProviderManifestEntry]
}

// PLAT-872-64: a failed refresh keeps the selected account's models visible for
// reference, but stale inventory cannot be used to change the running model.
it('shows cached Cursor models without allowing selection after discovery fails', async () => {
  cursorFixture()
  state.providerManifest[0].usable = false
  vi.mocked(llmConfigService.getProviderModels).mockResolvedValue({
    provider: 'cursor-cli', model_selection_mode: 'dynamic', source: 'account_cache', cached_at: '', cache_ttl_seconds: 0,
    selection_disabled: true, error: 'Check this account in Providers.',
    models: [{ model_id: 'composer-2.5', model_name: 'Composer 2.5', family: 'Composer 2.5', group: 'Cursor' }],
  })
  const { host, onRuntimeChange } = await render({ schema_version: 2, mode: 'explicit', builder_llm: {
    provider: 'cursor-cli', model_id: 'composer-2.5', connection_id: 'global:cursor-cli', options: {},
  } })
  expect(host.textContent).toContain('Check this account in Providers.')
  const choice = Array.from(host.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.trim() === 'Composer 2.5')
  expect(choice?.disabled).toBe(true)
  await act(async () => choice?.click())
  expect(onRuntimeChange).not.toHaveBeenCalled()
})

// PLAT-872: a paid Cursor account lists ~270 models; the picker shows the server's families with variant chips,
// folds older versions and sends the exact ID of the chosen variant.
it('groups the Cursor account models into families with variant chips', async () => {
  cursorFixture()
  vi.mocked(llmConfigService.getProviderModels).mockResolvedValue({
    provider: 'cursor-cli', model_selection_mode: 'dynamic', source: 'cli_account', cached_at: '', cache_ttl_seconds: 0,
    models: [
      { model_id: 'auto', model_name: 'Auto', family: 'Auto', group: 'Cursor', is_default: true },
      { model_id: 'claude-opus-5-1m', model_name: 'Claude Opus 5 1M', family: 'Claude Opus 5', variant: 'Standard', family_note: '1M context', group: 'Anthropic' },
      { model_id: 'claude-opus-5-1m-max-thinking', model_name: 'Claude Opus 5 1M Max Thinking', family: 'Claude Opus 5', variant: 'Max · Thinking', family_note: '1M context', group: 'Anthropic' },
      { model_id: 'claude-opus-4-8-1m', model_name: 'Claude Opus 4.8 1M', family: 'Claude Opus 4.8', variant: 'Standard', group: 'Anthropic', older: true },
    ],
  })
  const { host, onRuntimeChange } = await render({ schema_version: 2, mode: 'explicit', builder_llm: {
    provider: 'cursor-cli', model_id: 'auto', connection_id: 'global:cursor-cli', options: {},
  } })
  expect(host.textContent).toContain('Anthropic')
  expect(host.textContent).not.toContain('Claude Opus 4.8')
  const button = (text: string) => Array.from(host.querySelectorAll<HTMLButtonElement>('button')).find(b => b.textContent?.trim().startsWith(text))!
  await act(async () => button('Claude Opus 5').click())
  await act(async () => button('Max · Thinking').click())
  expect(onRuntimeChange).toHaveBeenLastCalledWith(expect.objectContaining({ modelId: 'claude-opus-5-1m-max-thinking', provider: 'cursor-cli', connectionId: 'global:cursor-cli' }))
  await act(async () => button('Show older models').click())
  expect(host.textContent).toContain('Claude Opus 4.8')
})

it('shows Claude effort while the model picker is collapsed and preserves its account', async () => {
  state.options = [{ id: 'claude-code', provider: 'claude-code', model_id: 'claude-sonnet-5-5', reasoning_efforts: ['low', 'medium', 'high', 'max'] }]
  state.providerManifest = [{ id: 'claude-code', integration_kind: 'coding_agent', runtime_available: true, usable: true,
    models: [{ provider: 'claude-code', model_id: 'claude-sonnet-5-5', model_name: 'Sonnet 5.5',
      supports_reasoning_effort: true, reasoning_effort_levels: ['low', 'medium', 'high', 'max'] }],
  } as ProviderManifestEntry]
  const { host, onRuntimeChange } = await render({ schema_version: 2, mode: 'explicit', builder_llm: {
    provider: 'claude-code', model_id: 'claude-sonnet-5-5', connection_id: 'global:claude-code', options: { reasoning_effort: 'high' },
  } })
  await act(async () => host.querySelector<HTMLButtonElement>('button[aria-expanded]')!.click())
  const low = Array.from(host.querySelectorAll<HTMLButtonElement>('[aria-label="Reasoning effort"] button')).find(button => button.textContent === 'Low')!
  expect(low).toBeDefined()
  await act(async () => low.click())
  expect(onRuntimeChange).toHaveBeenCalledWith(expect.objectContaining({ reasoningEffort: 'low', modelId: 'claude-sonnet-5-5', connectionId: 'global:claude-code' }))
})

// Owner 2026-10-07: Models names the account the chat actually runs on. With no project choice that is
// the account the chat recorded, else the person's own (the server's default), never a guess of shared.
it('shows the account the chat runs on: recorded, else the own default', async () => {
  museFixture()
  const metadata = state.chatTabs.project.metadata as Record<string, unknown>
  vi.mocked(llmConfigService.getProviderConnections).mockResolvedValue([
    { id: 'mine', provider: 'muse-cli', display_name: 'Mine', scope: 'user', auth_method: 'cli_login', relation: 'own' },
  ] as never)
  const own = await render()
  expect(own.host.querySelector('[data-testid="account-limits"]')?.textContent).toBe('Your own account · not limited')
  expect(own.host.querySelector('[data-testid="account-limits"]')?.closest('[data-testid="provider-configuration"]')).not.toBeNull()
  await act(async () => { root?.unmount() }); document.body.innerHTML = ''
  Object.assign(metadata, { agentProfileChatProvider: 'muse-cli', agentProfileChatConnectionID: 'global:muse-cli' })
  const shared = await render()
  expect(shared.host.querySelector('[data-testid="account-limits"]')?.textContent).toMatch(/^Shared account/)
  expect(state.configurationProps.llmConfig?.connection_id).toBe('global:muse-cli')
  delete metadata.agentProfileChatProvider; delete metadata.agentProfileChatConnectionID
  vi.mocked(llmConfigService.getProviderConnections).mockResolvedValue([])
})


describe('caller-owned chat models', () => {
  const models = vi.fn()
  const chats = vi.fn()
  const save = vi.fn()
  async function renderChatModels(mainKey = 'crew') {
    museFixture()
    state.product.profileId = 'work'
    Object.assign(agentApi, { listAgentProfileChatModels: models, listAgentProfileSideChats: chats, setAgentProfileChatModel: save })
    models.mockResolvedValue([{ conversation_key: mainKey, engine: 'muse-cli', model_id: 'muse-spark-1.3', reasoning_effort: 'high', display_label: 'Muse · Spark 1.3' }])
    chats.mockResolvedValue([{ conversation_key: 'crew:chat:review', name: 'Review', session_id: 'review-session' }])
    save.mockResolvedValue([])
    const host = document.createElement('div'); document.body.append(host)
    root = createRoot(host)
    const onRuntimeChange = vi.fn()
    await act(async () => root?.render(<WorkModelsPanel tabId="project" projectId="crew" workspacePath="/crew" hideHeader
      projectLLMConfig={{ schema_version: 2, mode: 'explicit', builder_llm: { provider: 'muse-cli', model_id: 'muse-spark-1.3-contributor', options: { reasoning_effort: 'max' } } }} onRuntimeChange={onRuntimeChange} />))
    return { host, onRuntimeChange }
  }

  it('lists main and side chats, distinguishing an own model from the Crew default', async () => {
    const { host } = await renderChatModels()
    expect(host.querySelector('[aria-label="Models by chat"]')?.textContent).toContain('Main chatMuse · Spark 1.3')
    expect(host.querySelector('[aria-label="Models by chat"]')?.textContent).toContain("ReviewCrew's model")
    expect(save).not.toHaveBeenCalled()
  })

  it('clears an own main-chat choice through the registry without changing the Crew default', async () => {
    save.mockClear()
    const { host, onRuntimeChange } = await renderChatModels()
    const main = Array.from(host.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.startsWith('Main chat'))!
    await act(async () => main.click())
    const inherit = Array.from(host.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === "Use Crew's model")!
    await act(async () => inherit.click())
    expect(save).toHaveBeenCalledWith('work', 'crew', { conversation_key: 'crew', engine: '', model_id: undefined, reasoning_effort: undefined, connection_id: undefined })
    expect(onRuntimeChange).not.toHaveBeenCalled()
    expect(host.querySelector('[aria-label="Models by chat"]')?.textContent).toContain("Main chatCrew's model")
  })
  it('saves a configured personal or company account for this chat through the provider picker', async () => {
    save.mockClear()
    const { host, onRuntimeChange } = await renderChatModels()
    const main = Array.from(host.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.startsWith('Main chat'))!
    await act(async () => main.click())
    expect(host.querySelector('[aria-label="Chat coding agent"]')).toBeNull()
    for (const connection_id of ['personal-muse', 'global:muse-cli']) {
      await act(async () => state.configurationProps.onChange({ schema_version: 2, mode: 'provider_profile', provider: 'muse-cli', connection_id }))
      expect(save).toHaveBeenLastCalledWith('work', 'crew', expect.objectContaining({ conversation_key: 'crew', engine: 'muse-cli', connection_id }))
    }
    expect(onRuntimeChange).not.toHaveBeenCalled()
  })

  it('edits the fixed physical main-chat ID after migration', async () => {
    const metadata = state.chatTabs.project.metadata
    const original = { ...metadata }
    Object.assign(metadata, { agentProfileId: 'work', agentProfileProjectId: 'crew', agentProfileIsMainChat: true, agentProfileConversationKey: 'crew:chat:root-original' })
    save.mockClear()
    try {
      const { host, onRuntimeChange } = await renderChatModels('crew:chat:root-original')
      expect(host.querySelector('[aria-label="Models by chat"]')?.textContent).toContain('Main chatMuse · Spark 1.3')
      const reset = Array.from(host.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === "Use Crew's model")!
      await act(async () => reset.click())
      expect(save).toHaveBeenCalledWith('work', 'crew', expect.objectContaining({ conversation_key: 'crew:chat:root-original', engine: '' }))
      expect(onRuntimeChange).not.toHaveBeenCalled()
    } finally {
      for (const key of Object.keys(metadata)) Reflect.deleteProperty(metadata, key)
      Object.assign(metadata, original)
    }
  })

})
