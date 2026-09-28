import { afterEach, describe, expect, it, vi } from 'vitest'
import { llmConfigService } from '../services/llm-config-api'
import { useLLMStore } from './useLLMStore'

afterEach(() => {
  vi.restoreAllMocks()
})

describe('AGY library support', () => {
  it('keeps a published AGY model selectable', async () => {
    useLLMStore.persist.setOptions({ storage: { getItem: () => null, setItem: () => {}, removeItem: () => {} } })
    vi.spyOn(llmConfigService, 'getPublishedLLMs').mockResolvedValue([])
    const save = vi.spyOn(llmConfigService, 'savePublishedLLMs').mockResolvedValue({ status: 'ok' })
    const refresh = vi.fn(async () => {})
    useLLMStore.setState({ supportedProviders: ['agy-cli'], savedLLMs: [], refreshAvailableLLMs: refresh })

    await useLLMStore.getState().saveLLM(
      { provider: 'agy-cli', model_id: 'gemini-3.8-flash-high' },
      'AGY Flash',
    )

    expect(save).toHaveBeenCalledWith([
      expect.objectContaining({ provider: 'agy-cli', model_id: 'gemini-3.8-flash-high', name: 'AGY Flash' }),
    ])
    expect(useLLMStore.getState().savedLLMs).toEqual(save.mock.calls[0][0])
    expect(refresh).toHaveBeenCalledOnce()
  })
})
