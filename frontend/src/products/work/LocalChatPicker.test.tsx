// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { LocalChatPicker } from './LocalChatPicker'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('../../services/api', () => ({ default: api }))

// PLAT-746: reading a history must never start a turn or import the wrong chat
// when an earlier preview finishes after the person selects another one.
it('previews the selected chat and imports only on Continue', async () => {
  api.get.mockReset(); api.post.mockReset()
  let finishFirst!: (data: unknown) => void
  const first = new Promise(resolve => { finishFirst = resolve })
  const chats = ['one', 'two'].map(id => ({ id, title: id, provider: 'claude-code', account: 'Your local login', updated_at: '2026-11-05T10:00:00Z' }))
  api.get.mockImplementation((_path, { params }) => Promise.resolve(params.sessions ? { data: { sessions: chats } } : params.native_id === 'one' ? first : { data: { messages: [{ role: 'human', text: 'Second chat history' }], total_messages: 1 } }))
  const imported = { conversation_key: 'project:chat:import-two', session_id: 'independent', name: 'two' }
  api.post.mockResolvedValue({ data: imported })
  const onImported = vi.fn(), onClose = vi.fn()
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<LocalChatPicker projectId="project" onImported={onImported} onClose={onClose} />))
    expect(api.post).not.toHaveBeenCalled()
    await act(async () => document.querySelectorAll<HTMLButtonElement>('[aria-label="Local conversations"] button')[1].click())
    await act(async () => finishFirst({ data: { messages: [{ role: 'human', text: 'Stale first history' }], total_messages: 1 } }))
    expect(document.querySelector('[aria-label="Conversation preview"]')?.textContent).toContain('Second chat history')
    expect(document.body.textContent).not.toContain('Stale first history')
    expect(api.post).not.toHaveBeenCalled()
    const button = [...document.querySelectorAll<HTMLButtonElement>('button')].find(b => b.textContent?.includes('Continue in AgentWorks'))!
    await act(async () => button.click())
    expect(api.post).toHaveBeenCalledWith('/api/agent-profiles/work/local-project', { project_id: 'project', id: 'two' })
    expect(onImported).toHaveBeenCalledWith(imported)
    expect(onClose).toHaveBeenCalledTimes(1)
  } finally { act(() => root.unmount()); host.remove() }
})

it('selects a folder chat before creation without importing or starting a turn', async () => {
  api.get.mockReset(); api.post.mockReset()
  const chat = { id: 'one', title: 'Existing work', provider: 'cursor-cli', account: 'Your local login', updated_at: '2026-11-05T10:00:00Z' }
  api.get.mockImplementation((_path, { params }) => Promise.resolve({ data: params.sessions ? { sessions: [chat] } : { messages: [{ role: 'human', text: 'The original conversation' }], total_messages: 1 } }))
  const onSelected = vi.fn(), onClose = vi.fn()
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<LocalChatPicker folder="/Users/example/project" onSelected={onSelected} onClose={onClose} />))
    expect(document.body.textContent).toContain('Continues the same Cursor CLI conversation after creation')
    expect(document.body.textContent).not.toContain('Your original conversation stays unchanged')
    expect(document.querySelector('[aria-label="Local conversations"] img')?.getAttribute('src')).toBe('/provider-icons/cursor.svg')
    expect(api.get).toHaveBeenCalledWith('/api/agent-profiles/work/local-project', expect.objectContaining({ params: { root_path: '/Users/example/project', native_id: 'one' } }))
    const button = [...document.querySelectorAll<HTMLButtonElement>('button')].find(b => b.textContent?.includes('Use this chat'))!
    await act(async () => button.click())
    expect(onSelected).toHaveBeenCalledExactlyOnceWith(chat)
    expect(onClose).toHaveBeenCalledOnce()
    expect(api.post).not.toHaveBeenCalled()
  } finally { act(() => root.unmount()); host.remove() }
})
