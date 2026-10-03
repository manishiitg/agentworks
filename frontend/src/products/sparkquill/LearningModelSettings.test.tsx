// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { LearningModelSettings } from './LearningModelSettings'

const mocks = vi.hoisted(() => ({ state: vi.fn(), save: vi.fn(), apply: vi.fn() }))
vi.mock('./api', () => ({ api: { setup: mocks.state, selectEngine: mocks.save } }))
vi.mock('./platform/PlatformChat', () => ({ PARENT_PROFILE_ID: 'sparkquill', applyFamilyEngineToOpenTabs: mocks.apply }))
vi.mock('./platform/ChildPlatformChat', () => ({ CHILD_PROFILE_ID: 'sparkquill-child' }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: { getState: () => ({ chatTabs: {} }) } }))
vi.mock('../../services/llm-config-api', () => ({ llmConfigService: { getModelMetadata: async () => ({ models: [] }) } }))
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
  await act(async () => { (parent.querySelector('button') as HTMLButtonElement).click() })
  await act(async () => { [...parent.querySelectorAll('button')].find(button => button.textContent === 'luna')!.click() })
  expect(mocks.save).toHaveBeenCalledWith('parent', 'codex', 'luna')
  expect(mocks.apply).toHaveBeenCalledWith('parent', 'codex', 'luna', 'high')
  expect(child.querySelector('button')?.textContent).toContain('luna')
})

it('leaves the current model intact and displays a save failure', async () => {
  mocks.state.mockResolvedValue({ engine: 'codex', parent_model: 'sol', child_model: 'luna' })
  mocks.save.mockRejectedValue(new Error('offline'))
  const host = await mount()
  const child = host.querySelector('[aria-label="Child tutor model"]')!
  await act(async () => { (child.querySelector('button') as HTMLButtonElement).click() })
  await act(async () => { [...child.querySelectorAll('button')].find(button => button.textContent === 'sol')!.click() })
  expect(mocks.save).toHaveBeenCalledWith('child', 'codex', 'sol')
  expect(mocks.apply).not.toHaveBeenCalled()
  expect(child.querySelector('button')?.textContent).toContain('luna')
  expect(host.querySelector('[role="alert"]')?.textContent).toContain('Could not save')
})
