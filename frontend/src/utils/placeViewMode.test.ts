// @vitest-environment happy-dom
import { beforeEach, expect, it, vi } from 'vitest'

vi.hoisted(() => {
  const memory = new Map<string, string>()
  const storage = { getItem: (k: string) => memory.get(k) ?? null, setItem: (k: string, v: string) => { memory.set(k, String(v)) }, removeItem: (k: string) => { memory.delete(k) }, clear: () => memory.clear(), key: (i: number) => [...memory.keys()][i] ?? null, get length() { return memory.size } }
  Object.defineProperty(globalThis, 'localStorage', { value: storage, configurable: true })
  Object.defineProperty(globalThis, 'sessionStorage', { value: storage, configurable: true })
})
vi.mock('../services/llm-config-api', () => {
  const service = new Proxy({}, { get: () => vi.fn(async () => ({})) })
  return { llmConfigService: service, default: service }
})
import { placeViewKey, rememberPlaceViewMode, rememberedPlaceViewMode } from './placeViewMode'
import { useAuthStore } from '../stores/useAuthStore'

beforeEach(() => {
  globalThis.localStorage?.clear()
  useAuthStore.setState({ user: { id: 'u1' } as never })
})

const crew = { metadata: { agentProfileId: 'work', agentProfileProjectId: 'p1' } } as never
const code = { metadata: { agentProfileId: 'code', agentProfileProjectId: 'p1' } } as never
const workflow = { metadata: { presetQueryId: 'wf_1' } } as never

it('keys a tab by its Crew, Code or workflow', () => {
  expect(placeViewKey(crew)).toBe('work:p1')
  expect(placeViewKey(code)).toBe('code:p1')
  expect(placeViewKey(workflow)).toBe('workflow:wf_1')
  expect(placeViewKey({ metadata: {} } as never)).toBeNull()
})

it('remembers the choice per place and per person', () => {
  rememberPlaceViewMode(crew, 'terminal')
  rememberPlaceViewMode(workflow, 'formatted')
  expect(rememberedPlaceViewMode(crew)).toBe('terminal')
  expect(rememberedPlaceViewMode(code)).toBeNull()
  expect(rememberedPlaceViewMode(workflow)).toBe('formatted')
  useAuthStore.setState({ user: { id: 'u2' } as never })
  expect(rememberedPlaceViewMode(crew)).toBeNull()
})
