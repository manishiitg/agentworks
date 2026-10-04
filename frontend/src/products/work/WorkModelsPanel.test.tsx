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
  chatTabs: { project: { metadata: { agentProfileEngine: 'muse-cli', agentProfileReasoningEffort: 'medium' } } },
}))
vi.mock('../../stores/useLLMStore', () => ({ useLLMStore: (selector: (value: typeof state) => unknown) => selector(state) }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: (selector: (value: typeof state) => unknown) => selector(state) }))
vi.mock('./projectProduct', () => ({ useProjectProduct: () => state.product }))
vi.mock('../../components/workflow/WorkflowLLMConfigurationPanel', () => ({ default: () => null }))
vi.mock('../../components/providers/GuidedProviderTerminal', () => ({ default: () => null }))
vi.mock('../../services/api', () => ({ getApiBaseUrl: () => '', getAuthToken: () => null, agentApi: {} }))
vi.mock('../../services/llm-config-api', () => ({
  llmConfigService: { getProviderConnections: vi.fn(async () => []), getProviderModels: vi.fn(async () => ({ models: [] })) },
}))
vi.mock('../../utils/agentProfileCapabilities', async importOriginal => ({
  ...await importOriginal<typeof import('../../utils/agentProfileCapabilities')>(),
  loadAgentProfileProviderOptions: vi.fn(async () => state.options),
}))

import { WorkModelsPanel } from './WorkModelsPanel'
import { llmConfigService } from '../../services/llm-config-api'

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

async function render(config?: PresetLLMConfig) {
  const host = document.createElement('div'); document.body.append(host)
  root = createRoot(host)
  const onRuntimeChange = vi.fn()
  await act(async () => root?.render(<WorkModelsPanel tabId="project" workspacePath="/project" hideHeader
    projectLLMConfig={config} onRuntimeChange={onRuntimeChange} />))
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

const cursorIds = ['auto', 'composer-2.5', 'grok-4.7', 'grok-4.6', 'glm-5.3', 'glm-5.3-flash']
function cursorFixture() {
  state.options = [{ id: 'cursor-cli', provider: 'cursor-cli', model_id: 'auto', options: { reasoning_effort: 'high' } }]
  state.providerManifest = [{
    id: 'cursor-cli', integration_kind: 'coding_agent', runtime_available: true, usable: true,
    models: cursorIds.map(model_id => ({ model_id, model_name: model_id, provider: 'cursor-cli', context_window: 0, input_cost_per_1m: 0, output_cost_per_1m: 0 })),
  } as ProviderManifestEntry]
}

it.each(['work', 'code'])('shows curated and live Cursor choices in %s, without duplicate IDs or changing the selection', async profileId => {
  cursorFixture(); state.product.profileId = profileId
  const extra = 'grok-4.6[effort=xhigh,fast=false]'
  vi.mocked(llmConfigService.getProviderModels).mockResolvedValue({
    provider: 'cursor-cli', model_selection_mode: 'dynamic', source: 'cli_dynamic', cached_at: '', cache_ttl_seconds: 300,
    models: [{ model_id: 'glm-5.3', model_name: 'Duplicate GLM' }, { model_id: extra, model_name: 'Grok extra high' }],
  })
  const { host, onRuntimeChange } = await render({ schema_version: 2, mode: 'explicit', builder_llm: {
    provider: 'cursor-cli', model_id: 'auto', connection_id: 'global:cursor-cli', options: { reasoning_effort: 'high' },
  } })
  expect(llmConfigService.getProviderModels).toHaveBeenCalledWith('cursor-cli')
  const titles = Array.from(host.querySelectorAll('button .font-medium')).map(title => title.textContent)
  expect(titles).toEqual([...cursorIds, 'Grok extra high'])
  expect(onRuntimeChange).not.toHaveBeenCalled()
  const choice = Array.from(host.querySelectorAll('button')).find(button => button.querySelector('.font-medium')?.textContent === 'Grok extra high')!
  await act(async () => choice.click())
  expect(onRuntimeChange).toHaveBeenCalledWith(expect.objectContaining({ modelId: extra, provider: 'cursor-cli', connectionId: 'global:cursor-cli' }))
})

it('offers only the models the account allows and shows a single allowed model selected', async () => {
  cursorFixture()
  const account = { id: 'global:cursor-cli', provider: 'cursor-cli', display_name: 'Admin-managed account', scope: 'global', auth_method: 'server', allowed_models: ['composer-2.5', 'glm-5.3'] }
  vi.mocked(llmConfigService.getProviderConnections).mockResolvedValue([account] as never)
  const config: PresetLLMConfig = { schema_version: 2, mode: 'explicit', builder_llm: { provider: 'cursor-cli', model_id: 'grok-4.6', connection_id: 'global:cursor-cli', options: {} } }
  const { host, onRuntimeChange } = await render(config)
  const titles = () => Array.from(host.querySelectorAll('button .font-medium')).map(title => title.textContent)
  expect(titles()).toEqual(['composer-2.5', 'glm-5.3'])
  // The saved grok-4.6 is not allowed: the Model card reads as the first allowed model.
  expect(host.textContent).toContain('composer-2.5')
  expect(onRuntimeChange).not.toHaveBeenCalled()
  // One allowed model: that one is offered and selected.
  vi.mocked(llmConfigService.getProviderConnections).mockResolvedValue([{ ...account, allowed_models: ['glm-5.3'] }] as never)
  await act(async () => { root?.unmount() }); document.body.innerHTML = ''
  const second = await render(config)
  expect(Array.from(second.host.querySelectorAll('button .font-medium')).map(title => title.textContent)).toEqual(['glm-5.3'])
  expect(second.host.querySelector('button[aria-expanded] p')?.textContent).toContain('glm-5.3')
  vi.mocked(llmConfigService.getProviderConnections).mockResolvedValue([])
})

it('keeps curated Cursor choices when the live model list cannot be loaded', async () => {
  cursorFixture()
  vi.mocked(llmConfigService.getProviderModels).mockRejectedValue(new Error('Not signed in'))
  const { host } = await render()
  expect(Array.from(host.querySelectorAll('button .font-medium')).map(title => title.textContent)).toEqual(cursorIds)
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

it('restricts Cursor effort to the selected model and drops an incompatible effort on model change', async () => {
  cursorFixture()
  state.options[0].reasoning_efforts = ['low', 'medium', 'high', 'xhigh', 'max']
  state.providerManifest[0].models!.forEach(model => {
    model.supports_reasoning_effort = model.model_id.startsWith('grok') || model.model_id.startsWith('glm')
    model.reasoning_effort_levels = model.model_id.startsWith('grok') ? ['low', 'medium', 'high', 'xhigh'] : model.model_id.startsWith('glm') ? ['low', 'high', 'max'] : []
  })
  const { host, onRuntimeChange } = await render({ schema_version: 2, mode: 'explicit', builder_llm: {
    provider: 'cursor-cli', model_id: 'grok-4.6', connection_id: 'private-cursor', options: { reasoning_effort: 'xhigh' },
  } })
  expect(Array.from(host.querySelectorAll('[aria-label="Reasoning effort"] button')).map(button => button.textContent)).toEqual(['Low', 'Medium', 'High', 'Xhigh'])
  const choice = (id: string) => Array.from(host.querySelectorAll('button')).find(button => button.querySelector('.font-medium')?.textContent === id)!
  await act(async () => choice('glm-5.3').click())
  expect(onRuntimeChange).toHaveBeenLastCalledWith(expect.objectContaining({ modelId: 'glm-5.3', reasoningEffort: 'high', connectionId: 'private-cursor' }))
  await act(async () => choice('auto').click())
  expect(onRuntimeChange).toHaveBeenLastCalledWith(expect.objectContaining({ modelId: 'auto', reasoningEffort: undefined }))
})
