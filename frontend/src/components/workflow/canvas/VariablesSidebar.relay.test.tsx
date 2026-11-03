// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { VariablesSidebar } from './VariablesSidebar'
import { agentApi } from '../../../services/api'

vi.mock('../../../services/api', () => ({ agentApi: { getVariableGroups: vi.fn(), updateVariableGroups: vi.fn() } }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanups: (() => void)[] = []
afterEach(() => { cleanups.splice(0).forEach(cleanup => cleanup()); vi.clearAllMocks() })

async function mount(groups: { name: string; values: Record<string, string>; enabled?: boolean }[], relayMode = true) {
  vi.mocked(agentApi.getVariableGroups).mockResolvedValue({ success: true, manifest: {
    objective: '', extraction_date: '', variables: [{ name: 'ENDPOINT', description: 'Custom service', value: 'old' }], groups,
  } })
  vi.mocked(agentApi.updateVariableGroups).mockResolvedValue({ success: true, message: 'Saved' })
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  await act(async () => { root.render(<VariablesSidebar workspacePath="Workflow/relay" relayMode={relayMode} onClose={() => {}} />) })
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  return host
}

it('saves Flow configuration and sample INPUT without groups, preserving legacy values and refusing ambiguous migration', async () => {
  const host = await mount([{ name: 'old-default', values: { ENDPOINT: 'https://service.example', EXTRA: 'preserved' }, enabled: true }])
  expect(host.textContent).not.toContain('Add Variable Group')
  expect(host.querySelector<HTMLInputElement>('[aria-label="ENDPOINT"]')?.value).toBe('https://service.example')
  expect(host.querySelector<HTMLInputElement>('[aria-label="EXTRA"]')?.value).toBe('preserved')
  const input = host.querySelector<HTMLTextAreaElement>('[aria-label="Sample INPUT JSON"]')!
  await act(async () => {
    Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, 'value')!.set!.call(input, '{"invoice":"sample"}')
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
  const save = [...host.querySelectorAll('button')].find(button => button.textContent?.trim() === 'Save')!
  await act(async () => { save.click() })
  const saved = vi.mocked(agentApi.updateVariableGroups).mock.calls[0][1]
  expect(saved.groups).toBeUndefined()
  expect(saved.variables).toEqual(expect.arrayContaining([
    expect.objectContaining({ name: 'INPUT', value: '{"invoice":"sample"}' }),
    expect.objectContaining({ name: 'ENDPOINT', value: 'https://service.example' }),
    expect.objectContaining({ name: 'EXTRA', value: 'preserved' }),
  ]))

  const ambiguous = await mount([
    { name: 'production', values: { ENDPOINT: 'prod' }, enabled: true },
    { name: 'test', values: { ENDPOINT: 'test' }, enabled: false },
  ])
  expect(ambiguous.textContent).toContain('multiple variable groups')
  expect(ambiguous.querySelector<HTMLInputElement>('[aria-label="ENDPOINT"]')?.disabled).toBe(true)
  expect(ambiguous.querySelector<HTMLTextAreaElement>('[aria-label="Sample INPUT JSON"]')?.disabled).toBe(true)
})

// PLAT-746-265: saving an optional preset must not manufacture a baseline
// group or copy defaults into the preset (which would freeze shared values).
it('saves one sparse input preset without an enable flag or synthetic default group', async () => {
  const host = await mount([], false)
  const add = [...host.querySelectorAll('button')].find(button => button.textContent?.includes('Save another set'))!
  await act(async () => { add.click() })
  expect(host.querySelector('[aria-label="Delete saved inputs"]')).not.toBeNull()
  expect(host.querySelector('[title="Enable group"]')).toBeNull()
  const save = [...host.querySelectorAll('button')].find(button => button.textContent?.trim() === 'Save')!
  await act(async () => { save.click() })
  const saved = vi.mocked(agentApi.updateVariableGroups).mock.calls[0][1]
  expect(saved.variables[0].value).toBe('old')
  expect(saved.groups).toEqual([{ name: 'Saved inputs 1', values: {} }])
  await act(async () => { host.querySelector<HTMLButtonElement>('[aria-label="Delete saved inputs"]')!.click() })
  await act(async () => { save.click() })
  expect(vi.mocked(agentApi.updateVariableGroups).mock.calls[1][1].groups).toEqual([])
})

// PLAT-746-265: the definition editor is the only place to author the input type.
it('saves type and required once on the workflow definition', async () => {
  const host = await mount([], false)
  const type = host.querySelector<HTMLSelectElement>('[aria-label="ENDPOINT type"]')!
  await act(async () => { type.value = 'integer'; type.dispatchEvent(new Event('change', { bubbles: true })) })
  const required = host.querySelector<HTMLInputElement>('[aria-label="ENDPOINT needed to start"]')!
  await act(async () => { required.click() })
  const value = host.querySelector<HTMLTextAreaElement>('[aria-label="ENDPOINT value"]')!
  await act(async () => {
    Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, 'value')!.set!.call(value, '7')
    value.dispatchEvent(new Event('input', { bubbles: true }))
  })
  await act(async () => { [...host.querySelectorAll('button')].find(button => button.textContent?.trim() === 'Save')!.click() })
  expect(vi.mocked(agentApi.updateVariableGroups).mock.calls[0][1].variables[0]).toEqual(expect.objectContaining({ name: 'ENDPOINT', type: 'integer', required: true, value: '7' }))
})
