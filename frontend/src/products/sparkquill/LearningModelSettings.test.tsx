// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { LearningModelSettings } from './LearningModelSettings'

const mocks = vi.hoisted(() => ({ state: vi.fn(), save: vi.fn(), apply: vi.fn() }))
vi.mock('./api', () => ({ api: { setup: mocks.state, selectEngine: mocks.save } }))
vi.mock('./platform/PlatformChat', () => ({ PARENT_PROFILE_ID: 'sparkquill', FAMILY_WORKSPACE: 'Chats/SparkQuill', applyFamilyEngineToOpenTabs: mocks.apply }))
vi.mock('./platform/ChildPlatformChat', () => ({ CHILD_PROFILE_ID: 'sparkquill-child' }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: { getState: () => ({ chatTabs: {} }) } }))
vi.mock('../../services/llm-config-api', () => ({ llmConfigService: { getProviderManifest: async () => ({ providers: [{ id: 'codex-cli', models: ['luna', 'sol'].map(id => ({ provider: 'codex-cli', model_id: id, model_name: id, context_window: 100000, supports_reasoning_effort: true, reasoning_effort_levels: ['low', 'high'] })) }] }) } }))
vi.mock('../../components/workflow/WorkflowLLMConfigurationPanel', () => ({ default: ({ onChange }: { onChange: (value: unknown) => void }) => <button type="button" onClick={() => onChange({ provider: 'codex-cli', connection_id: 'family-account' })}>Use family account</button> }))
vi.mock('../../utils/agentProfileCapabilities', async importOriginal => ({
  ...await importOriginal<typeof import('../../utils/agentProfileCapabilities')>(),
  loadAgentProfileProviderOptions: async () => [{ id: 'codex', provider: 'codex-cli', model_id: 'luna', models: ['luna', 'sol'], reasoning_efforts: ['low', 'high'], options: { reasoning_effort: 'high' } }],
}))

const cleanups: (() => void)[] = []
afterEach(() => { cleanups.splice(0).forEach(fn => fn()); vi.unstubAllGlobals(); vi.clearAllMocks() })

async function mount() {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  await act(async () => { root.render(<LearningModelSettings engine="codex" childName="Maya" />) })
  return host
}

it('loads saved role choices and saves changes to the selected role', async () => {
  mocks.state.mockResolvedValue({ engine: 'codex', parent_model: 'sol', child_model: 'luna' })
  mocks.save.mockResolvedValue(undefined)
  const host = await mount()
  const parent = host.querySelector('[aria-label="Parent chat model"]')!
  const child = host.querySelector('[aria-label="Child tutor model"]')!
  expect(parent.textContent).toContain('sol')
  expect(child.textContent).toContain('luna')
  await act(async () => { [...parent.querySelectorAll('button')].find(button => button.textContent?.startsWith('luna'))!.click() })
  expect(mocks.save).toHaveBeenCalledWith('parent', 'codex', 'luna', undefined, 'high')
  expect(mocks.apply).toHaveBeenCalledWith('parent', 'codex', 'luna', 'high')
  expect(child.querySelector('[aria-pressed="true"]')?.textContent).toContain('luna')
})

it('leaves the current model intact and displays a save failure', async () => {
  mocks.state.mockResolvedValue({ engine: 'codex', parent_model: 'sol', child_model: 'luna' })
  mocks.save.mockRejectedValue(new Error('offline'))
  const host = await mount()
  const child = host.querySelector('[aria-label="Child tutor model"]')!
  await act(async () => { [...child.querySelectorAll('button')].find(button => button.textContent?.startsWith('sol'))!.click() })
  expect(mocks.save).toHaveBeenCalledWith('child', 'codex', 'sol', undefined, 'high')
  expect(mocks.apply).not.toHaveBeenCalled()
  expect(child.querySelector('[aria-pressed="true"]')?.textContent).toContain('luna')
  expect(host.querySelector('[role="alert"]')?.textContent).toContain('Could not save')
})

it('binds the platform account to both chats while preserving role models', async () => {
  mocks.state.mockResolvedValue({ engine: 'codex', parent_model: 'sol', child_model: 'luna', connection_id: 'family-account' })
  mocks.save.mockResolvedValue(undefined)
  const host = await mount()
  await act(async () => { [...host.querySelectorAll('button')].find(button => button.textContent === 'Use family account')!.click() })
  expect(mocks.save).toHaveBeenCalledWith('parent', 'codex', 'sol', 'family-account', 'high')
  expect(mocks.apply).toHaveBeenCalledWith('parent', 'codex', 'sol', 'high', 'family-account')
  expect(host.querySelector('[aria-label="Child tutor model"] [aria-pressed="true"]')?.textContent).toContain('luna')
})
