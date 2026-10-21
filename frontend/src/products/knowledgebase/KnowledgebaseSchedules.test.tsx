// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('../../services/api', () => ({ default: api }))
import { KnowledgebaseSchedules } from './KnowledgebaseSchedules'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const path = '/api/scheduler/jobs/product%3Aknowledgebase%3Aorganize'
const instructions = 'Existing custom instructions: only propose changes.'
const job = { id: 'product:knowledgebase:organize', name: 'Organize Brain', enabled: false, cadence_hours: 168, messages: [instructions] }
let unmount = () => {}
afterEach(() => { unmount(); document.body.innerHTML = ''; vi.resetAllMocks() })
async function mount() {
  api.get.mockResolvedValue({ data: job })
  api.post.mockImplementation(async (url: string, body: any) => ({ data: url.endsWith('/cadence') ? { ...job, cadence_hours: body.cadence_hours } : url.endsWith('/enable') ? { ...job, enabled: true } : url.endsWith('/message') ? { ...job, messages: [body.message || 'Server default instructions'] } : job }))
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host); unmount = () => act(() => root.unmount())
  await act(async () => root.render(<KnowledgebaseSchedules />))
  return host
}
const button = (host: HTMLElement, label: string) => [...host.querySelectorAll('button')].find(button => button.textContent === label)!
it('shows a simple schedule and keeps existing instructions under Advanced without saving on load', async () => {
  const host = await mount()
  expect(host.querySelector('details')?.open).toBe(false)
  expect(host.querySelector('summary')?.textContent).toBe('Advanced instructions')
  expect(host.querySelector('textarea')?.value).toBe(instructions)
  expect(host.textContent).toContain('Off')
  expect(host.textContent).toContain('Paused')
  expect(api.post).not.toHaveBeenCalled()
  const cadence = host.querySelector('select')!
  await act(async () => { cadence.value = '24'; cadence.dispatchEvent(new Event('change', { bubbles: true })) })
  expect(api.post).toHaveBeenCalledExactlyOnceWith(`${path}/cadence`, { cadence_hours: 24 })
  expect(host.querySelector('textarea')?.value).toBe(instructions)
})
it('preserves enable, custom instructions, server defaults and manual runs through the original endpoints', async () => {
  const host = await mount()
  await act(async () => host.querySelector<HTMLButtonElement>('[role="switch"]')!.click())
  expect(api.post).toHaveBeenLastCalledWith(`${path}/enable`, {})
  expect(host.textContent).toContain('On')
  await act(async () => host.querySelector('summary')!.click())
  const input = host.querySelector('textarea')!
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(input, 'New scoped instructions'); input.dispatchEvent(new Event('input', { bubbles: true })) })
  await act(async () => button(host, 'Save').click())
  expect(api.post).toHaveBeenLastCalledWith(`${path}/message`, { message: 'New scoped instructions' })
  await act(async () => button(host, 'Use default').click())
  expect(api.post).toHaveBeenLastCalledWith(`${path}/message`, { message: '' })
  expect(input.value).toBe('Server default instructions')
  await act(async () => button(host, 'Run now').click())
  expect(api.post).toHaveBeenLastCalledWith(`${path}/trigger`, {})
})
it('shows a rejected schedule change without claiming it was saved', async () => {
  const host = await mount()
  api.post.mockRejectedValue({ response: { data: 'Permission denied' } })
  await act(async () => host.querySelector<HTMLButtonElement>('[role="switch"]')!.click())
  expect(host.querySelector('[role="alert"]')?.textContent).toContain('Permission denied')
  expect(host.querySelector('[role="switch"]')?.getAttribute('aria-checked')).toBe('false')
})
