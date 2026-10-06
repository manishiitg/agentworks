// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { AgentLLMConfig, PresetLLMConfig, SavedLLM } from '../../services/api-types'
import type { ProviderConnection, ProviderManifestEntry } from '../../services/llm-config-api'
import type { LLMOption } from '../../types/llm'

const { storeState } = vi.hoisted(() => ({
  storeState: {
    availableLLMs: [] as LLMOption[],
    providerManifest: [] as ProviderManifestEntry[],
    providerManifestLoaded: true,
    loadProviderManifest: vi.fn(),
    defaultsLoaded: true,
    loadDefaultsFromBackend: vi.fn(),
    getProviderDynamicModels: vi.fn(),
    isProviderSupported: vi.fn(() => true),
    llmConfigLocked: false,
    lockedProviders: [] as string[],
    savedLLMs: [] as SavedLLM[],
    setShowLLMModal: vi.fn(),
  },
}))

vi.mock('../../stores/useLLMStore', () => ({
  useLLMStore: (selector: (state: typeof storeState) => unknown) => selector(storeState),
}))
vi.mock('../../hooks/useCanWriteWorkflow', () => ({ READ_ONLY_TITLE: 'Read only', useCanWriteWorkflow: () => true }))
vi.mock('../../services/llm-config-api', () => ({
  llmConfigService: { getModelMetadata: vi.fn(async () => ({ models: [] })), getProviderConnections: vi.fn(async () => [] as ProviderConnection[]) },
}))

import WorkflowLLMConfigurationPanel from './WorkflowLLMConfigurationPanel'
import { llmConfigService } from '../../services/llm-config-api'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const claude = (model_id: string, effort?: string): AgentLLMConfig =>
  ({ provider: 'claude-code', model_id, ...(effort ? { options: { reasoning_effort: effort } } : {}) })

const manifest = (): ProviderManifestEntry => ({
  id: 'claude-code', display_name: 'Claude Code', description: '', kind: 'local_cli', integration_kind: 'coding_agent',
  model_selection_mode: 'fixed_tier', auth_description: '', runtime_command: 'claude', runtime_available: true,
  auth_configured: true, usable: true, requires_api_key: false, supports_dynamic_models: false, default_model_id: 'sonnet',
  default_tier_models: {
    high: claude('opus', 'high'),
    medium: claude('sonnet', 'medium'),
    builder: claude('sonnet', 'medium'),
    low: claude('haiku', 'low'),
  },
  models: [], capabilities: [],
})

const options: LLMOption[] = ['opus', 'sonnet', 'haiku'].flatMap(model => ['low', 'medium', 'high'].map(effort => ({
  provider: 'claude-code', model, label: model.toUpperCase(), options: { reasoning_effort: effort }, section: 'published_model' as const,
})))

const uniform = (value: AgentLLMConfig): PresetLLMConfig => ({
  schema_version: 2, mode: 'explicit', builder_llm: value, pulse_llm: value,
  tiered_config: { tier_1: value, tier_2: value, tier_3: value },
})

const differing: PresetLLMConfig = {
  schema_version: 2, mode: 'explicit', builder_llm: claude('sonnet', 'high'), pulse_llm: claude('opus', 'high'),
  tiered_config: { tier_1: claude('opus', 'high'), tier_2: claude('sonnet', 'low'), tier_3: claude('haiku', 'low') },
}

const account = (id: string, name: string): ProviderConnection =>
  ({ id, provider: 'claude-code', display_name: name, scope: 'user', auth_method: 'api_key' }) as ProviderConnection

afterEach(() => {
  storeState.availableLLMs = []
  storeState.providerManifest = []
  vi.mocked(llmConfigService.getProviderConnections).mockResolvedValue([])
  document.body.innerHTML = ''
})

async function mount(llmConfig: PresetLLMConfig, onChange = vi.fn()) {
  storeState.providerManifest = [manifest()]
  storeState.availableLLMs = options
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  await act(async () => root.render(<WorkflowLLMConfigurationPanel workspacePath="/wf" llmConfig={llmConfig} onChange={onChange} />))
  await act(async () => Promise.resolve())
  return { host, onChange, unmount: async () => { await act(async () => root.unmount()); host.remove() } }
}

const select = async (el: HTMLSelectElement, value: string) => {
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!
    setter.call(el, value)
    el.dispatchEvent(new Event('change', { bubbles: true }))
  })
}
const switchEl = (host: HTMLElement) => host.querySelector<HTMLButtonElement>('[role="switch"]')!

describe('workflow role models', () => {
  it('shows one Model card by default and writes the same value to every role', async () => {
    const { host, onChange, unmount } = await mount(uniform(claude('sonnet', 'low')))
    try {
      expect(switchEl(host).getAttribute('aria-checked')).toBe('false')
      expect(host.textContent).toContain('SONNET · Low reasoning')
      expect(host.querySelector('button[aria-label^="High reasoning:"]')).toBeNull()
      await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="Set reasoning effort to High"]')!.click())
      expect(onChange).toHaveBeenCalledTimes(1)
      const next = onChange.mock.calls[0][0] as PresetLLMConfig
      const want = claude('sonnet', 'high')
      for (const role of [next.builder_llm, next.pulse_llm, next.tiered_config?.tier_1, next.tiered_config?.tier_2, next.tiered_config?.tier_3]) {
        expect(role).toMatchObject(want)
      }
    } finally { await unmount() }
  })

  it('shows the managed Builder effort beside chat and applies a new effort to all roles', async () => {
    const { host, onChange, unmount } = await mount({ schema_version: 2, mode: 'provider_profile', provider: 'claude-code', connection_id: 'private-a' })
    try {
      expect(host.textContent).toContain('SONNET · Medium reasoning')
      expect(host.querySelector('button[aria-label="Set reasoning effort to Medium"]')?.getAttribute('aria-pressed')).toBe('true')
      expect(onChange).not.toHaveBeenCalled()
      await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="Set reasoning effort to High"]')!.click())
      const next = onChange.mock.calls[0][0] as PresetLLMConfig
      expect(next.mode).toBe('explicit')
      for (const role of [next.builder_llm, next.pulse_llm, next.tiered_config?.tier_1, next.tiered_config?.tier_2, next.tiered_config?.tier_3]) {
        expect(role).toMatchObject({ ...claude('sonnet', 'high'), connection_id: 'private-a' })
      }
    } finally { await unmount() }
  })

  it('opens the per-role switch for differing roles and lists them compactly', async () => {
    const { host, onChange, unmount } = await mount(differing)
    try {
      expect(switchEl(host).getAttribute('aria-checked')).toBe('true')
      expect(host.textContent).toContain('Main work: first runs and complex steps')
      expect(host.textContent).toContain('Routine runs and upkeep')
      expect(host.textContent).toContain('Quick checks')
      expect(host.querySelector('button[aria-label^="Medium reasoning:"]')?.textContent).toContain('Claude Code · SONNET · Low')
      expect(host.textContent).not.toContain('Customized')
      expect(host.textContent).not.toContain('Reset to provider default')
      expect(onChange).not.toHaveBeenCalled()
    } finally { await unmount() }
  })

  it('edits only one role from its popover', async () => {
    const { host, onChange, unmount } = await mount(differing)
    try {
      await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label^="Low reasoning:"]')!.click())
      const dialog = host.querySelector('[role="dialog"]')!
      expect(dialog).not.toBeNull()
      await select(dialog.querySelector<HTMLSelectElement>('select[aria-label="Model"]')!, 'sonnet')
      expect(onChange).toHaveBeenCalledTimes(1)
      const next = onChange.mock.calls[0][0] as PresetLLMConfig
      expect(next.tiered_config?.tier_3).toMatchObject({ model_id: 'sonnet' })
      expect(next.tiered_config?.tier_1).toEqual(differing.tiered_config?.tier_1)
      expect(next.tiered_config?.tier_2).toEqual(differing.tiered_config?.tier_2)
      expect(next.builder_llm).toEqual(differing.builder_llm)
      expect(next.pulse_llm).toEqual(differing.pulse_llm)
    } finally { await unmount() }
  })

  it('closes the popover on Escape and returns focus to its button', async () => {
    const { host, unmount } = await mount(differing)
    try {
      const button = host.querySelector<HTMLButtonElement>('button[aria-label^="Low reasoning:"]')!
      await act(async () => button.click())
      expect(button.getAttribute('aria-expanded')).toBe('true')
      await act(async () => { host.querySelector('[role="dialog"]')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })) })
      expect(host.querySelector('[role="dialog"]')).toBeNull()
      expect(document.activeElement).toBe(button)
    } finally { await unmount() }
  })

  it('confirms inline before turning the switch off and applies the Builder setting to all roles', async () => {
    const { host, onChange, unmount } = await mount(differing)
    try {
      await act(async () => switchEl(host).click())
      expect(onChange).not.toHaveBeenCalled()
      expect(host.querySelector('[role="status"]')?.textContent).toContain('Every role will use Claude Code · SONNET · High')
      await act(async () => Array.from(host.querySelectorAll('button')).find(b => b.textContent === 'Use for all roles')!.click())
      const next = onChange.mock.calls[0][0] as PresetLLMConfig
      expect(next.tiered_config?.tier_3).toMatchObject(claude('sonnet', 'high'))
      expect(next.builder_llm).toMatchObject(claude('sonnet', 'high'))
    } finally { await unmount() }
  })

  it('hides the account chooser with one usable account and shows it with several', async () => {
    vi.mocked(llmConfigService.getProviderConnections).mockResolvedValue([account('a', 'Personal A')])
    const one = await mount(uniform(claude('sonnet', 'low')))
    try {
      expect(one.host.querySelector('select[aria-label="Provider account"]')).toBeNull()
    } finally { await one.unmount() }

    vi.mocked(llmConfigService.getProviderConnections).mockResolvedValue([account('a', 'Personal A'), account('b', 'Personal B')])
    const two = await mount(uniform(claude('sonnet', 'low')))
    try {
      const chooser = two.host.querySelector<HTMLSelectElement>('select[aria-label="Provider account"]')
      expect(chooser).not.toBeNull()
      expect(chooser!.closest('label')?.textContent).toContain('Account to use')
    } finally { await two.unmount() }
  })

  it('does not rewrite unchanged saved data', async () => {
    for (const config of [differing, uniform(claude('sonnet', 'low'))]) {
      const { onChange, unmount } = await mount(config)
      try { expect(onChange).not.toHaveBeenCalled() } finally { await unmount() }
    }
  })
})
