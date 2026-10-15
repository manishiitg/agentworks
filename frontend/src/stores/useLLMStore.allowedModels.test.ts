import { afterEach, describe, expect, it, vi } from 'vitest'
import { llmConfigService } from '../services/llm-config-api'
import { useLLMStore } from './useLLMStore'

afterEach(() => {
  vi.restoreAllMocks()
})

// The server flags (per person) the models the account does not allow; the picker hides them, and they are never
// written back with the shared list.
describe('models the account does not allow', () => {
  it('are not offered in the picker', async () => {
    useLLMStore.persist.setOptions({ storage: { getItem: () => null, setItem: () => {}, removeItem: () => {} } })
    vi.spyOn(llmConfigService, 'getModelMetadata').mockResolvedValue({ models: [] } as never)
    useLLMStore.setState({
      defaultsLoaded: true,
      supportedProviders: ['pi-cli'],
      savedLLMs: [
        { id: 'a', name: 'Allowed', provider: 'pi-cli', model_id: 'example/allowed' },
        { id: 'b', name: 'Flagged', provider: 'pi-cli', model_id: 'example/other', not_allowed: true },
      ],
    })
    await useLLMStore.getState().refreshAvailableLLMs()
    expect(useLLMStore.getState().availableLLMs.map(llm => llm.model)).toEqual(['example/allowed'])
  })

  it('never go back to the server with the shared list', async () => {
    useLLMStore.persist.setOptions({ storage: { getItem: () => null, setItem: () => {}, removeItem: () => {} } })
    vi.spyOn(llmConfigService, 'getPublishedLLMs').mockResolvedValue([
      { id: 'b', name: 'Flagged', provider: 'pi-cli', model_id: 'example/other', not_allowed: true },
    ] as never)
    const save = vi.spyOn(llmConfigService, 'savePublishedLLMs').mockResolvedValue({ status: 'ok' })
    useLLMStore.setState({ supportedProviders: ['pi-cli'], savedLLMs: [], refreshAvailableLLMs: async () => {} })
    await useLLMStore.getState().saveLLM({ provider: 'pi-cli', model_id: 'example/allowed' }, 'New')
    const sent = save.mock.calls[0][0] as unknown as Array<Record<string, unknown>>
    expect(sent).toHaveLength(2)
    expect(sent.some(llm => 'not_allowed' in llm)).toBe(false)
  })
})
