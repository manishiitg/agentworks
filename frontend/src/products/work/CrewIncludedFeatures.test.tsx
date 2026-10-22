// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({
  get: vi.fn(), user: { id: 'alice', allowed_products: null as string[] | null }, server: 'local',
}))
vi.mock('../../services/api', () => ({ default: { get: mocks.get }, getApiBaseUrl: () => 'http://' + mocks.server }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: any) => select({ user: mocks.user }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: (select: any) => select({ activeWorkspaceId: mocks.server }) }))
import { CrewIncludedFeatures } from './CrewIncludedFeatures'
import { crewFeaturePresentation } from './crewFeaturePresentation'
import { CREW_STARTER_TYPES } from './crewStarterTypes'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let root: Root
const ids = ['live-chat', 'files', 'memory', 'dashboard', 'schedules', 'skills', 'workflow-references', 'condition-triggers', 'knowledgebase', 'triggers', 'coding']
beforeEach(() => {
  mocks.user = { id: 'alice', allowed_products: null }; mocks.server = 'local'
  mocks.get.mockResolvedValue({ data: { resolved_features: ids.map(id => ({ id })) } })
})
afterEach(() => { act(() => root?.unmount()); document.body.innerHTML = ''; vi.resetAllMocks() })
async function render(type: Parameters<typeof CrewIncludedFeatures>[0]['type'] = 'automation') {
  const host = document.createElement('div'); document.body.append(host); root = createRoot(host)
  const update = async () => { await act(async () => root.render(<CrewIncludedFeatures type={type} mode="dev" profileVersion={3} />)) }
  await update()
  return { host, update }
}
const featureButtons = (host: HTMLElement) => [...host.querySelectorAll<HTMLButtonElement>('button[aria-controls]:not([aria-pressed])')]
it('places enabled features directly under their automation step and explains how to use them', async () => {
  const { host } = await render()
  expect(mocks.get).toHaveBeenCalledWith('/api/agent-profiles/work', { params: { version: 3 }, signal: expect.any(AbortSignal), timeout: 10000 })
  const group = (label: string) => [...host.querySelectorAll<HTMLButtonElement>(`li[aria-label="${label}"] button`)].map(item => item.textContent)
  expect(group('Time or event')).toEqual(['Schedules', 'Watchers', 'Webhooks'])
  expect(group('Skills + code')).toEqual(['Skills', 'Functions', 'Code'])
  expect(group('Results')).toEqual(['Dashboard', 'Files'])
  expect(host.textContent).not.toContain('Try in chat:')
  await act(async () => featureButtons(host).find(item => item.textContent === 'Schedules')!.click())
  expect(host.textContent).toContain('Set up a weekday briefing at 9 AM.')
  expect(host.textContent).toContain('Use → Schedules')
  expect(host.textContent).toContain('Set up first')
  await act(async () => featureButtons(host).find(item => item.textContent === 'Functions')!.click())
  expect(host.textContent).toContain('Try in chat: Help me create a reusable function')
  expect(host.querySelector('input')).toBeNull()
})
it('shows the full server inventory including unknown features without inventing enabled features', async () => {
  mocks.get.mockResolvedValue({ data: { resolved_features: [...ids, 'new-feature'].map(id => ({ id })) } })
  const { host } = await render()
  await act(async () => [...host.querySelectorAll('button')].find(item => item.textContent?.startsWith('All '))!.click())
  expect(featureButtons(host)).toHaveLength(ids.length + 1)
  expect(featureButtons(host).some(item => item.textContent === 'New feature')).toBe(true)
  expect(featureButtons(host).some(item => item.textContent === 'Terminal')).toBe(false)
  await act(async () => featureButtons(host).find(item => item.textContent === 'New feature')!.click())
  expect(host.textContent).toContain('Ask how to use new feature in this Crew.')
})
it('does not advertise defaults omitted by the server', async () => {
  mocks.get.mockResolvedValue({ data: { resolved_features: [{ id: 'files' }] } })
  const { host } = await render()
  expect(featureButtons(host).map(item => item.textContent)).toEqual(['Files'])
  expect(host.querySelectorAll('li').length).toBe(3)
  expect(host.textContent).toContain('No matching defaults')
  await act(async () => featureButtons(host)[0].click())
  expect(host.textContent).toContain('Summarize the document I attached.')
  expect(featureButtons(host).some(item => item.textContent === 'Skills')).toBe(false)
})
it('clears old defaults and cancels stale reads when account or server changes', async () => {
  const { host, update } = await render()
  const first = mocks.get.mock.calls[0][1].signal as AbortSignal
  let resolve: (value: unknown) => void = () => {}
  mocks.get.mockReturnValueOnce(new Promise(done => { resolve = done }))
  mocks.user = { id: 'bob', allowed_products: ['work'] }; await update()
  expect(first.aborted).toBe(true)
  expect(host.textContent).toContain('Checking included features')
  expect(featureButtons(host)).toHaveLength(0)
  mocks.server = 'remote'; mocks.get.mockResolvedValue({ data: { resolved_features: [{ id: 'files' }] } }); await update()
  await act(async () => resolve({ data: { resolved_features: ids.map(id => ({ id })) } }))
  expect(featureButtons(host).map(item => item.textContent)).toEqual(['Files'])
})
it('keeps failures and an empty inventory explicit, with retry', async () => {
  mocks.get.mockRejectedValueOnce(new Error('offline'))
  const { host } = await render()
  expect(host.textContent).toContain('Could not load default features')
  expect(featureButtons(host)).toHaveLength(0)
  mocks.get.mockResolvedValueOnce({ data: { resolved_features: [] } })
  await act(async () => [...host.querySelectorAll<HTMLButtonElement>('button')].find(item => item.textContent === 'Retry')!.click())
  expect(host.textContent).toContain('No default features reported')
})
it('uses chat instructions for the terminal in Cowork mode', () => {
  expect(crewFeaturePresentation([{ id: 'terminal' }], 'code', 'cowork')[0].location).toBe('Chat')
})

it.each(CREW_STARTER_TYPES.map(starter => starter.id))('keeps every %s step linked without inventing unavailable defaults', async type => {
  const { host } = await render(type)
  const stages = [...host.querySelectorAll<HTMLElement>('li[aria-label]')]
  expect(stages).toHaveLength(3)
  for (const stage of stages) {
    expect(stage.querySelector('h4')?.textContent).toBeTruthy()
    expect(featureButtons(host).every(button => button.getAttribute('aria-label')?.endsWith(': how to use'))).toBe(true)
    expect(featureButtons(host).some(button => button.textContent === 'Terminal')).toBe(false)
  }
  await act(async () => [...host.querySelectorAll<HTMLButtonElement>('button')].find(item => item.textContent?.startsWith('All '))!.click())
  expect(featureButtons(host)).toHaveLength(ids.length)
})

it('explains Chief of Staff coordination through existing enabled project references', async () => {
  const { host } = await render('chief-of-staff')
  expect(host.querySelector('li[aria-label="Connect your work"]')?.textContent).toContain('Crews & workflows')
  expect(host.querySelector('li[aria-label="Coordinate the work"]')?.textContent).toContain('Memory')
  expect(host.querySelector('li[aria-label="Stay informed"]')?.textContent).toContain('Schedules')
  await act(async () => featureButtons(host).find(item => item.textContent === 'Crews & workflows')!.click())
  expect(host.textContent).toContain('Crews and workflows I can access')
  expect(host.textContent).toContain('Set up first')
  expect(mocks.get).toHaveBeenCalledTimes(1)
})
