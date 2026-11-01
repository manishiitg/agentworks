// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
vi.mock('../services/api', () => ({ default: { post: vi.fn() }, agentApi: { getAgentProfileChatModelState: vi.fn() } }))
import api, { agentApi } from '../services/api'
import { HomeAgentModel } from './HomeAgentModel'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let root: Root
afterEach(() => { act(() => root?.unmount()); vi.resetAllMocks() })

// The missing-model fallback used to appear after "Runs on", making a
// settings instruction look like the selected model (PLAT-746-292).
it.each([false, true])('shows only a resolved canonical model, available=%s', async available => {
  vi.mocked(api.post).mockResolvedValue({ data: { crew_id: 'personal', conversation_key: 'main' } })
  vi.mocked(agentApi.getAgentProfileChatModelState).mockResolvedValue({
    chat_models: [], can_open_models: true,
    effective_chat_models: available ? [{ conversation_key: 'main', provider: 'example', engine: 'example', model_id: 'test-model', display_label: 'Test model', own_model: false }] : [],
  })
  const host = document.createElement('div')
  root = createRoot(host)
  await act(async () => root.render(<HomeAgentModel />))
  expect(host.textContent).toBe(available ? 'Runs on Test model' : '')
})
