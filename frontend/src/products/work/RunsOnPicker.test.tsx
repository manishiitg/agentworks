// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { RunsOnPicker, type RunsOnSelection } from './RunsOnPicker'
import { rememberRunsOn } from './runsOnMemory'

const accounts = vi.hoisted(() => ({ list: [] as unknown[] }))
vi.mock('../../services/llm-config-api', () => ({
  llmConfigService: { getProviderConnections: vi.fn(async () => accounts.list) },
}))
vi.mock('../../utils/agentProfileCapabilities', () => ({
  loadAgentProfileProviderOptions: vi.fn(async () => [
    { id: 'claude-code', label: 'Claude Code', provider: 'claude-code', model_id: 'claude-sonnet-5-5', default: true },
    { id: 'codex-cli', label: 'Codex', provider: 'codex-cli', model_id: 'gpt-6' },
  ]),
}))

// This test runner has no working localStorage; a small in-memory one stands in.
const store = new Map<string, string>()
vi.stubGlobal('localStorage', { getItem: (k: string) => store.get(k) ?? null, setItem: (k: string, v: string) => { store.set(k, v) }, removeItem: (k: string) => { store.delete(k) }, clear: () => store.clear() })
const cleanups: (() => void)[] = []
afterEach(() => { cleanups.splice(0).forEach(fn => fn()); store.clear() })

async function render() {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  const seen: (RunsOnSelection | undefined)[] = []
  await act(async () => { root.render(<RunsOnPicker profileId="work" onChange={value => { seen.push(value) }} />) })
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)) })
  return { host, last: () => seen[seen.length - 1] }
}

describe('RunsOnPicker', () => {
  it('starts on your own signed-in account and saves it with the project', async () => {
    accounts.list = [
      { id: 'global:claude-code', provider: 'claude-code', scope: 'global', relation: 'server', configured: false, usable: true },
      { id: 'mine', provider: 'codex-cli', scope: 'user', relation: 'own', configured: true, usable: true, identity: 'v@x.com' },
    ]
    const { host, last } = await render()
    expect(host.querySelector('select')?.value).toBe('codex-cli')
    // A private account is never saved on the new project (others in a Crew could not use it):
    // the server uses your own account for your own chats.
    expect(last()).toMatchObject({ provider: 'codex-cli', modelId: 'gpt-6', connectionId: undefined })
    expect(host.textContent).toContain('your account (v@x.com)')
  })

  it('offers Sign in when nothing usable is signed in, and still lets you create', async () => {
    accounts.list = [{ id: 'global:claude-code', provider: 'claude-code', scope: 'global', relation: 'server', configured: false, usable: true }]
    const { host, last } = await render()
    expect(host.textContent).toContain('You are not signed in to Claude Code')
    expect(host.textContent).toContain('Sign in')
    expect(last()).toMatchObject({ provider: 'claude-code', connectionId: undefined })
  })

  it('starts on the CLI used last when it is ready', async () => {
    accounts.list = [
      { id: 'global:claude-code', provider: 'claude-code', scope: 'global', relation: 'server', configured: true, usable: true },
      { id: 'mine', provider: 'codex-cli', scope: 'user', relation: 'own', configured: true, usable: true },
    ]
    rememberRunsOn('work', 'claude-code')
    const { host, last } = await render()
    expect(host.querySelector('select')?.value).toBe('claude-code')
    expect(last()).toMatchObject({ provider: 'claude-code', connectionId: undefined })
  })
})
