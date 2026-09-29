// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'

vi.mock('../../services/llm-config-api', () => ({
  llmConfigService: { getProductDefaults: vi.fn(), setProductDefaults: vi.fn() },
  providerApiErrorText: (_error: unknown, fallback: string) => fallback,
}))

import { llmConfigService, type ProviderManifestEntry } from '../../services/llm-config-api'
import ProductDefaults from './ProductDefaults'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let root: Root | undefined
afterEach(() => { act(() => { root?.unmount() }); root = undefined; document.body.innerHTML = ''; vi.clearAllMocks() })

const entry = (id: string, name: string, models: string[]) => ({
  id, display_name: name, default_model_id: models[0], models: models.map(model_id => ({ model_id, model_name: model_id.toUpperCase() })),
}) as unknown as ProviderManifestEntry

it('shows each product default and lets an admin change the ones not pinned', async () => {
  vi.mocked(llmConfigService.getProductDefaults).mockResolvedValue({
    code: { provider: 'muse-cli', model_id: 'muse-1', connection_id: 'global:muse-cli', pinned: true },
    work: { provider: 'claude-code', model_id: 'sonnet', connection_id: 'global:claude-code' },
  })
  const container = document.createElement('div'); document.body.appendChild(container)
  root = createRoot(container)
  await act(async () => { root?.render(<ProductDefaults isAdmin providers={[entry('claude-code', 'Claude Code', ['sonnet', 'opus']), entry('muse-cli', 'Muse', ['muse-1'])]} />) })
  const rows = [...container.querySelectorAll('li')]
  expect(rows.map(row => row.textContent)).toEqual([
    expect.stringContaining('WorkflowsNo default set'),
    expect.stringContaining('Claude Code · SONNET'),
    expect.stringContaining('Set by the installation'),
  ])
  expect(rows[2].querySelector('button')).toBeNull()
  await act(async () => { [...rows[1].querySelectorAll('button')].find(button => button.textContent === 'Change')!.click() })
  const model = container.querySelector<HTMLSelectElement>('select[aria-label="Crews default model"]')!
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!.call(model, 'opus')
    model.dispatchEvent(new Event('change', { bubbles: true }))
  })
  await act(async () => { container.querySelector('form')!.requestSubmit() })
  expect(llmConfigService.setProductDefaults).toHaveBeenCalledWith({ work: { provider: 'claude-code', model: 'opus' } })
})
