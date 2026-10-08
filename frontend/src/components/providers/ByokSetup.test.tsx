// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'

vi.mock('../../services/llm-config-api', () => ({
  llmConfigService: { byokTestKey: vi.fn(), byokModels: vi.fn(), byokTryModel: vi.fn(), addProviderConnection: vi.fn() },
  providerApiErrorText: (_error: unknown, fallback: string) => fallback,
}))

import { llmConfigService } from '../../services/llm-config-api'
import ByokSetup from './ByokSetup'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let root: Root | undefined
afterEach(() => { act(() => { root?.unmount() }); root = undefined; document.body.innerHTML = ''; vi.clearAllMocks() })

const settle = () => act(async () => { await Promise.resolve(); await Promise.resolve() })
const click = async (element?: Element | null) => { expect(element).toBeTruthy(); await act(async () => { (element as HTMLElement).click() }); await settle() }
const button = (text: string | RegExp) => [...document.querySelectorAll('button')].find(item => typeof text === 'string' ? item.textContent?.trim() === text : text.test(item.textContent || ''))

// PLAT-717: the guided flow a person uses to bring their own OpenRouter key: tile -> key -> Test key -> models -> save,
// saving a private Pi account on provider "openrouter" with the starred free tool model as its model.
it('adds an OpenRouter key with a free agent model in three steps', async () => {
  vi.mocked(llmConfigService.byokTestKey).mockResolvedValue({ state: 'ok', identity: 'OpenRouter key · free tier' })
  vi.mocked(llmConfigService.byokModels).mockResolvedValue({
    default_model: 'openrouter/vendor/agent:free',
    models: [
      { model_id: 'openrouter/vendor/agent:free', model_name: 'Agent', is_free: true, supports_tools: true, recommended: true, context_window: 262144 },
      { model_id: 'openrouter/vendor/paid-model', model_name: 'Paid', supports_tools: true, cost_input: 2, cost_output: 10 },
    ],
  })
  vi.mocked(llmConfigService.addProviderConnection).mockImplementation(async request => ({ ...request, id: 'acct-1', scope: 'user' as const, auth_method: 'api_key' }))
  const onSaved = vi.fn()
  const container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
  await act(async () => { root?.render(<ByokSetup onSaved={onSaved} onCancel={() => undefined} />) })

  await click(button(/^OpenRouterRecommended/))
  expect(container.textContent).toContain('openrouter.ai/keys')
  const key = container.querySelector('input[type="password"]') as HTMLInputElement
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(key, 'sk-or-v1-abc')
    key.dispatchEvent(new Event('input', { bubbles: true }))
  })
  await click(button('Test key'))
  expect(llmConfigService.byokTestKey).toHaveBeenCalledWith({ service: 'openrouter', credential: 'sk-or-v1-abc' })
  expect(container.querySelector('[role="status"]')?.textContent).toContain('Key works · OpenRouter key · free tier')

  await click(button('Next: choose models'))
  await settle()
  // Free models first, the default free tool model starred; the paid one only when the Free filter is off.
  expect(container.querySelector('[aria-label="Unpick Agent"]')?.getAttribute('aria-pressed')).toBe('true')
  expect(container.textContent).toContain('Recommended for agents')
  expect(container.textContent).not.toContain('Paid')
  await click(container.querySelector('button[aria-pressed="true"].rounded-full'))
  expect(container.textContent).toContain('$2.00 / $10.00 per 1M')

  await click(button('Save key and 1 model'))
  expect(llmConfigService.addProviderConnection).toHaveBeenCalledWith({
    provider: 'pi-cli', display_name: 'OpenRouter key', credential: 'sk-or-v1-abc', underlying_provider: 'openrouter',
    allowed_models: ['openrouter/vendor/agent:free'],
  })
  expect(onSaved).toHaveBeenCalledWith(expect.objectContaining({ id: 'acct-1', relation: 'own' }), ['openrouter/vendor/agent:free'])
})
