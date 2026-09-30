// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import type { CostConversation } from '../../services/api-types'
import { agentApi } from '../../services/api'
import CostConversations from './CostConversations'

vi.mock('../../services/api', () => ({ agentApi: { listChatHistorySessions: vi.fn(), getChatHistoryResumeConversation: vi.fn() } }))
vi.mock('../ui/ConversationRenderer', () => ({ ConversationRenderer: ({ content }: { content: string }) => <div>{content}</div> }))

it('shows fresh/cache split and opens the associated chat without starting a run', async () => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  vi.mocked(agentApi.listChatHistorySessions).mockResolvedValue({ sessions: [{ session_id: 'product-health', title: 'Five-minute health check' }] } as Awaited<ReturnType<typeof agentApi.listChatHistorySessions>>)
  vi.mocked(agentApi.getChatHistoryResumeConversation).mockResolvedValue({ session_id: 'product-health', conversation_history: [{ Role: 'human', Parts: [{ Text: 'Check the server' }] }], saved_prompts: [{ role: 'system', text: 'Use the project tools to check health.' }, { role: 'developer', text: 'Work in the sandbox.', truncated: true }], history_pagination: { has_more: true, next_offset: 50, start_turn: 100, total_turns: 150 } })
  const usage = { input_tokens: 1000, prompt_tokens: 1000, cache_read_tokens: 800, cache_write_tokens: 0, completion_tokens: 25, reasoning_tokens: 0, call_count: 3, total_cost_usd: .02 }
  const row: CostConversation = { ...usage, session_id: 'product-health', workflow_id: '_users/alice/Chats/Code/projects/test', user_id: 'alice', first_seen: '2026-09-30T06:00:00Z', last_seen: '2026-09-30T06:01:00Z', by_execution: { q1: { ...usage, scope: 'chat', first_seen: '2026-09-30T06:00:00Z', last_seen: '2026-09-30T06:01:00Z', by_model: { muse: usage } } } }
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  try {
    await act(async () => { root.render(<CostConversations rows={[row]} />); await new Promise(resolve => setTimeout(resolve, 10)) })
    expect(host.textContent).toContain('Five-minute health check')
    expect(host.textContent).toContain('1 recorded turn / agent run')
    expect(host.textContent).toContain('Fresh input200')
    expect(host.textContent).toContain('Cached input (read)800')
    expect(host.textContent).toContain('80.0% of input was read from cache')
    const view = Array.from(host.querySelectorAll('button')).find(button => button.textContent === 'View chat')!
    await act(async () => { view.click(); await new Promise(resolve => setTimeout(resolve, 10)) })
    expect(agentApi.getChatHistoryResumeConversation).toHaveBeenCalledWith('product-health', row.workflow_id, 50, 0, false, true)
    expect(host.textContent).toContain('Check the server')
    const prompts = Array.from(host.querySelectorAll('details')).find(details => details.querySelector('summary')?.textContent === 'System prompt')!
    expect(prompts.open).toBe(false)
    expect(prompts.textContent).toContain('Use the project tools to check health.')
    expect(prompts.textContent).toContain('Developer instructions')
    expect(prompts.textContent).toContain('only the first 64 KiB is shown')
    expect(prompts.textContent).toContain('Earlier runs may have used different instructions')
    const earlier = Array.from(host.querySelectorAll('button')).find(button => button.textContent === 'Earlier messages')!
    await act(async () => { earlier.click(); await new Promise(resolve => setTimeout(resolve, 10)) })
    expect(agentApi.getChatHistoryResumeConversation).toHaveBeenLastCalledWith('product-health', row.workflow_id, 50, 50, false, true)
  } finally { await act(async () => root.unmount()); host.remove(); vi.unstubAllGlobals() }
})
