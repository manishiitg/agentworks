// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
const api = vi.hoisted(() => ({ getLocalProjectContext: vi.fn(), resumeLocalProjectSession: vi.fn() }))
vi.mock('../../services/api', () => ({ agentApi: api }))
import { LocalProjectContext, type LocalProjectContextData } from './LocalProjectContext'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let root: Root
afterEach(() => { act(() => root?.unmount()); document.body.innerHTML = ''; vi.clearAllMocks() })
const data: LocalProjectContextData = {
  folder: 'example-project', instructions: [{ path: 'CLAUDE.md', name: 'CLAUDE.md' }, { path: 'app/AGENTS.md', name: 'AGENTS.md' }],
  skills: [{ name: 'review', path: '.claude/skills/review/SKILL.md' }], partial: false, sessions_available: true, sessions: [],
}
async function render() {
  const host = document.createElement('div'); document.body.append(host); root = createRoot(host)
  await act(async () => root.render(<LocalProjectContext projectId="example" />))
  return host
}
it('stays compact, identifies found paths and opens native history only when requested', async () => {
  api.getLocalProjectContext.mockResolvedValue(data)
  const host = await render()
  expect(host.querySelector('details')!.open).toBe(false)
  expect(host.querySelector('summary')!.textContent).toContain('2 instructions · 1 skill')
  expect(host.textContent).toContain('app/AGENTS.md')
  expect(host.textContent).toContain('.claude/skills/review/SKILL.md')
  expect(host.textContent).toContain('Found in this folder')
  expect(api.getLocalProjectContext).toHaveBeenCalledExactlyOnceWith('example', false)
  api.getLocalProjectContext.mockResolvedValue({ ...data, sessions: [{ id: 'opaque', provider: 'claude-code', account: 'Your local login', title: 'Continue research', updated_at: '2026-10-27T10:00:00Z' }] })
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Past Claude / Codex chats')!.click())
  expect(api.getLocalProjectContext).toHaveBeenLastCalledWith('example', true)
  expect(host.textContent).toContain('Your local login')
  api.resumeLocalProjectSession.mockResolvedValue({ conversation_key: 'example:side:native', session_id: 'private-chat' })
  const event = vi.fn(); window.addEventListener('agentworks:local-chat-opened', event)
  try {
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Open tab')!.click())
    expect(api.resumeLocalProjectSession).toHaveBeenCalledExactlyOnceWith('example', 'opaque')
    expect(event.mock.calls[0][0].detail).toEqual({ projectId: 'example', conversationKey: 'example:side:native' })
  } finally { window.removeEventListener('agentworks:local-chat-opened', event) }
})
it('reports a failed scan and lets the person retry without an empty success claim', async () => {
  api.getLocalProjectContext.mockRejectedValueOnce(new Error('unavailable')).mockResolvedValue(data)
  const host = await render()
  expect(host.querySelector('[role="alert"]')!.textContent).toContain('Couldn’t check')
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Refresh project context"]')!.click())
  expect(host.querySelector('[role="alert"]')).toBeNull()
  expect(host.querySelector('summary')!.textContent).toContain('example-project')
})
