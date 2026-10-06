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

async function mount(groups: { name: string; values: Record<string, string>; enabled: boolean }[]) {
  vi.mocked(agentApi.getVariableGroups).mockResolvedValue({ success: true, manifest: {
    objective: '', extraction_date: '', variables: [{ name: 'ENDPOINT', description: 'Custom service', value: 'old' }], groups,
  } })
  vi.mocked(agentApi.updateVariableGroups).mockResolvedValue({ success: true, message: 'Saved' })
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  await act(async () => { root.render(<VariablesSidebar workspacePath="Workflow/relay" relayMode onClose={() => {}} />) })
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  return host
}

it('saves Relay configuration and sample INPUT without groups, preserving legacy values and refusing ambiguous migration', async () => {
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
