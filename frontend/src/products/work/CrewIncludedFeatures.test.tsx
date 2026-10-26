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
async function open(host: HTMLElement) {
  await act(async () => host.querySelector('summary')!.click())
}

it('starts compact and explains server-enabled capabilities only when opened', async () => {
  const { host } = await render()
  expect(host.querySelector('details')!.open).toBe(false)
  expect(host.textContent).toContain('What’s included')
  expect(featureButtons(host)).toHaveLength(0)
  expect(mocks.get).toHaveBeenCalledWith('/api/agent-profiles/work', { params: { version: 3 }, signal: expect.any(AbortSignal), timeout: 10000 })
  await open(host)
  expect(featureButtons(host)).toHaveLength(ids.length)
  await act(async () => featureButtons(host).find(item => item.textContent === 'Schedules')!.click())
  expect(host.textContent).toContain('Set up a weekday briefing at 9 AM.')
  expect(host.textContent).toContain('Set up first')
  await open(host)
  expect(featureButtons(host)).toHaveLength(0)
})

it('keeps unknown server features and never advertises omitted ones', async () => {
  mocks.get.mockResolvedValue({ data: { resolved_features: [{ id: 'files' }, { id: 'new-feature' }] } })
  const { host } = await render(undefined)
  await open(host)
  expect(featureButtons(host).map(item => item.textContent)).toEqual(['Files', 'New feature'])
  await act(async () => featureButtons(host).find(item => item.textContent === 'New feature')!.click())
  expect(host.textContent).toContain('Ask how to use new feature in this Crew.')
  expect(host.textContent).not.toContain('Terminal')
  expect(crewFeaturePresentation([{ id: 'terminal' }], 'code', 'cowork')[0].location).toBe('Chat')
})

it('folds and discards stale inventory when account or server changes', async () => {
  const { host, update } = await render()
  await open(host)
  const first = mocks.get.mock.calls[0][1].signal as AbortSignal
  let resolve: (value: unknown) => void = () => {}
  mocks.get.mockReturnValueOnce(new Promise(done => { resolve = done }))
  mocks.user = { id: 'bob', allowed_products: ['work'] }; await update()
  expect(first.aborted).toBe(true)
  expect(host.querySelector('details')!.open).toBe(false)
  expect(featureButtons(host)).toHaveLength(0)
  mocks.server = 'remote'; mocks.get.mockResolvedValue({ data: { resolved_features: [{ id: 'files' }] } }); await update()
  await act(async () => resolve({ data: { resolved_features: ids.map(id => ({ id })) } }))
  await open(host)
  expect(featureButtons(host).map(item => item.textContent)).toEqual(['Files'])
})

it('keeps failure and empty inventory explicit inside the optional details', async () => {
  mocks.get.mockRejectedValueOnce(new Error('offline'))
  const { host } = await render()
  await open(host)
  expect(host.textContent).toContain('Could not load default features')
  mocks.get.mockResolvedValueOnce({ data: { resolved_features: [] } })
  await act(async () => [...host.querySelectorAll<HTMLButtonElement>('button')].find(item => item.textContent === 'Retry')!.click())
  expect(host.textContent).toContain('No default features reported')
})
