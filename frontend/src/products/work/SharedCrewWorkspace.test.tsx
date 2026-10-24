// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { SharedCrewWorkspace } from './SharedCrewWorkspace'

const post = vi.hoisted(() => vi.fn())
vi.mock('../../services/api', () => ({ default: { post } }))
vi.mock('../../components/ui/MarkdownRenderer', () => ({ MarkdownRenderer: ({ content }: { content: string }) => <div>{content}</div> }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('opens only published agents and the existing attributed transcript, without Builder controls', async () => {
  post.mockReset()
  post.mockImplementation((_url, body) => {
    if (body.action === 'chats') return Promise.resolve({ data: { chats: [
      { chat_id: 'project:chat:coach', name: 'Coach', purpose: 'Review feedback', shared: true },
      { chat_id: 'own-caller-chat', name: 'Caller chat' },
    ] } })
    if (body.action === 'read_chat' && !body.after) return Promise.resolve({ data: { messages: [{ seq: 1, role: 'you', sender: 'Alex', text: 'Earlier context' }, { seq: 2, role: 'assistant', text: 'Here is my feedback' }], next_after: 2 } })
    return new Promise(() => {}) // one bounded long read; unmount aborts the real transport
  })
  const host = document.createElement('div')
  const root = createRoot(host)
  try {
    await act(async () => root.render(<SharedCrewWorkspace projectId="project" enabled><div>Shared dashboard</div></SharedCrewWorkspace>))
    expect(host.textContent).toContain('Shared dashboard')
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent?.startsWith('Agents'))!.click())
    expect(host.textContent).toContain('Review feedback')
    expect(host.textContent).not.toContain('Caller chat')
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent?.includes('Coach'))!.click())
    expect(host.querySelector('[role="log"]')?.textContent).toContain('Earlier context')
    expect(host.querySelector('[role="log"]')?.textContent).toContain('Alex')
    expect(host.textContent).toContain('Here is my feedback')
    expect(post).toHaveBeenCalledWith('/api/crew-agents', expect.objectContaining({ action: 'read_chat', workspace_id: 'work:project', chat_id: 'project:chat:coach', after: 2, wait_seconds: 25 }), expect.anything())
    expect(host.querySelector('button[aria-label="Stop"]')).toBeNull()
    expect(host.textContent).not.toMatch(/Terminal|Change model|Reset chat/)
  } finally { await act(async () => root.unmount()) }
})
