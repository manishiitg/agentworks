// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { agentApi } from '../../services/api'
import type { CodeAdminChat, CodeAdminWorkspace } from '../../services/api-types'
import ConversationsOverview from './ConversationsOverview'

vi.mock('../../services/api', () => ({ agentApi: { adminListCodeWorkspaces: vi.fn(), adminListCodeChats: vi.fn(), adminGetCodeChat: vi.fn() } }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let root: Root
let host: HTMLDivElement
const projects: CodeAdminWorkspace[] = ['alice', 'bob'].map(owner => ({ owner_id: owner, owner_username: owner, id: 'same-id', title: `${owner} project`, workspace_path: `_users/${owner}/Chats/Code/projects/app`, shares: [] }))
const chats: CodeAdminChat[] = [{ user_id: 'participant', username: 'Pat', session_id: 'chat-1', title: 'Deployment failure', updated_at: '2026-09-30T10:00:00Z', message_count: 2 }]
const button = (text: string) => [...host.querySelectorAll<HTMLButtonElement>('button')].find(item => item.textContent?.includes(text))!
const deferred = <T,>() => {
  let resolve!: (result: T) => void
  const promise = new Promise<T>(done => { resolve = done })
  return { promise, resolve }
}
beforeEach(() => {
  vi.mocked(agentApi.adminListCodeWorkspaces).mockResolvedValue({ workspaces: projects })
  vi.mocked(agentApi.adminListCodeChats).mockResolvedValue({ chats })
  vi.mocked(agentApi.adminGetCodeChat).mockResolvedValue({ conversation_history: [{ Role: 'human', Parts: [{ Text: 'What failed?' }] }, { role: 'ai', parts: [{ text: 'PTY permission denied.' }, { ToolCall: { Name: 'bash' } }] }] })
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
})
afterEach(async () => { await act(async () => root.unmount()); host.remove(); vi.resetAllMocks() })

it('opens an audited read-only transcript with the actual participant, not the project owner', async () => {
  await act(async () => root.render(<ConversationsOverview />))
  await act(async () => button('alice project').click())
  await act(async () => button('Deployment failure').click())
  expect(agentApi.adminListCodeChats).toHaveBeenCalledWith('alice', 'same-id')
  expect(agentApi.adminGetCodeChat).toHaveBeenCalledWith('alice', 'same-id', 'chat-1', 'participant')
  expect(host.textContent).toContain('What failed?')
  expect(host.textContent).toContain('PTY permission denied.')
  expect(host.textContent).toContain('Assistant')
  expect(host.textContent).toContain('Tool or message details')
  expect(host.querySelector('textarea')).toBeNull()
  expect(host.textContent).toContain('Each review is recorded in the audit log')
})

it('discards a late chat list from another owner even when project IDs match', async () => {
  const late = deferred<{ chats: CodeAdminChat[] }>()
  vi.mocked(agentApi.adminListCodeChats).mockImplementation(owner => owner === 'alice' ? late.promise : Promise.resolve({ chats: [{ ...chats[0], title: 'Bob current chat' }] }))
  await act(async () => root.render(<ConversationsOverview />))
  await act(async () => button('alice project').click())
  await act(async () => button('bob project').click())
  await act(async () => late.resolve({ chats: [{ ...chats[0], title: 'Alice stale chat' }] }))
  expect(host.textContent).toContain('Bob current chat')
  expect(host.textContent).not.toContain('Alice stale chat')
})

it('clears the old transcript and ignores its late response when another chat is opened', async () => {
  const late = deferred<Awaited<ReturnType<typeof agentApi.adminGetCodeChat>>>()
  vi.mocked(agentApi.adminListCodeChats).mockResolvedValue({ chats: [...chats, { ...chats[0], user_id: 'bob', session_id: 'chat-2', title: 'Second chat' }] })
  vi.mocked(agentApi.adminGetCodeChat).mockImplementation((_owner, _project, session) => session === 'chat-1' ? late.promise : Promise.resolve({ conversation_history: [{ Role: 'ai', Parts: [{ Text: 'Current response' }] }] }))
  await act(async () => root.render(<ConversationsOverview />))
  await act(async () => button('alice project').click())
  await act(async () => button('Deployment failure').click())
  await act(async () => button('Second chat').click())
  await act(async () => late.resolve({ conversation_history: [{ Role: 'ai', Parts: [{ Text: 'Stale response' }] }] }))
  expect(host.textContent).toContain('Current response')
  expect(host.textContent).not.toContain('Stale response')
})

it('filters owners and conversation users and clears an open review when filters change', async () => {
  vi.mocked(agentApi.adminListCodeChats).mockResolvedValue({ chats: [...chats, { ...chats[0], user_id: 'bob', username: 'Bob', session_id: 'chat-2', title: 'Bob chat' }] })
  await act(async () => root.render(<ConversationsOverview />))
  const owner = host.querySelector<HTMLSelectElement>('[aria-label="Project owner"]')!
  await act(async () => { owner.value = 'alice'; owner.dispatchEvent(new Event('change', { bubbles: true })) })
  expect(button('bob project')).toBeUndefined()
  await act(async () => button('alice project').click())
  await act(async () => button('Deployment failure').click())
  const user = host.querySelector<HTMLSelectElement>('[aria-label="Conversation user"]')!
  await act(async () => { user.value = 'bob'; user.dispatchEvent(new Event('change', { bubbles: true })) })
  expect(button('Deployment failure')).toBeUndefined()
  expect(button('Bob chat')).toBeTruthy()
  expect(host.querySelector('[aria-label="Conversation transcript"]')).toBeNull()
})

it('shows permission failures and retries through Refresh', async () => {
  vi.mocked(agentApi.adminListCodeWorkspaces).mockRejectedValueOnce({ response: { status: 403 } })
  await act(async () => root.render(<ConversationsOverview />))
  expect(host.querySelector('[role="alert"]')?.textContent).toContain('requires an admin or Code reviewer')
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Refresh conversations"]')!.click())
  expect(host.querySelector('[role="alert"]')).toBeNull()
  expect(button('alice project')).toBeTruthy()
})

it('shows empty project, chat, and transcript states', async () => {
  vi.mocked(agentApi.adminListCodeWorkspaces).mockResolvedValueOnce({ workspaces: [] })
  await act(async () => root.render(<ConversationsOverview />))
  expect(host.textContent).toContain('No Code projects match')
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Refresh conversations"]')!.click())
  vi.mocked(agentApi.adminListCodeChats).mockResolvedValueOnce({ chats: [] })
  await act(async () => button('alice project').click())
  expect(host.textContent).toContain('No conversations match')
  vi.mocked(agentApi.adminGetCodeChat).mockResolvedValue({ conversation_history: [] })
  await act(async () => button('bob project').click())
  await act(async () => button('Deployment failure').click())
  expect(host.textContent).toContain('no recorded messages')
})
