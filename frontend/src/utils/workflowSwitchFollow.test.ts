import { describe, expect, it, vi } from 'vitest'

vi.hoisted(() => {
  const memory = new Map<string, string>()
  const storage = { getItem: (k: string) => memory.get(k) ?? null, setItem: (k: string, v: string) => { memory.set(k, String(v)) }, removeItem: (k: string) => { memory.delete(k) }, clear: () => memory.clear(), key: (i: number) => [...memory.keys()][i] ?? null, get length() { return memory.size } }
  if (typeof globalThis.localStorage === 'undefined') Object.defineProperty(globalThis, 'localStorage', { value: storage, configurable: true })
  if (typeof globalThis.sessionStorage === 'undefined') Object.defineProperty(globalThis, 'sessionStorage', { value: storage, configurable: true })
})
vi.mock('../services/llm-config-api', () => {
  const service = new Proxy({}, { get: () => vi.fn(async () => ({})) })
  return { llmConfigService: service, default: service }
})

import type { ActiveSessionInfo } from '../services/api-types'
import { followsOnWorkflowSwitch } from './workflowSessionRestore'

const session = (overrides: Partial<ActiveSessionInfo>): ActiveSessionInfo => ({
  session_id: 'sess-1', observer_id: '', agent_mode: 'workflow_phase', status: 'running',
  last_activity: '', created_at: '', ...overrides,
})

// Switching to a workflow lands on the user's main chat; a schedule, webhook,
// bot or external run in progress does not pull them into its chat.
describe('followsOnWorkflowSwitch', () => {
  it('follows the user\'s own conversation', () => {
    expect(followsOnWorkflowSwitch(session({ triggered_by: 'interactive' }))).toBe(true)
    expect(followsOnWorkflowSwitch(session({}))).toBe(true)
  })
  it('does not follow schedule, webhook, bot or external runs', () => {
    expect(followsOnWorkflowSwitch(session({ triggered_by: 'cron' }))).toBe(false)
    expect(followsOnWorkflowSwitch(session({ session_id: 'schedule-webhook--abc_1', triggered_by: 'webhook' }))).toBe(false)
    expect(followsOnWorkflowSwitch(session({ triggered_by: 'bot:slack', bot_platform: 'slack' }))).toBe(false)
    expect(followsOnWorkflowSwitch(session({ triggered_by: 'external' }))).toBe(false)
  })
})
