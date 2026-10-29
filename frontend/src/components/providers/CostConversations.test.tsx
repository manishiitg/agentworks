// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import type { CostConversation } from '../../services/api-types'
import { agentApi } from '../../services/api'
import CostConversations from './CostConversations'

vi.mock('../../services/api', () => ({ agentApi: { listChatHistorySessions: vi.fn(), getUsageTurnMessages: vi.fn() } }))

it('shows the exact user message for a usage turn without embedding full chat', async () => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  vi.mocked(agentApi.listChatHistorySessions).mockResolvedValue({ sessions: [{ session_id: 'product-health', title: 'Five-minute health check' }] } as Awaited<ReturnType<typeof agentApi.listChatHistorySessions>>)
  vi.mocked(agentApi.getUsageTurnMessages).mockResolvedValue({ messages: { q1: 'Check the server' } })
  const usage = { input_tokens: 1000, prompt_tokens: 1000, cache_read_tokens: 800, cache_write_tokens: 0, completion_tokens: 25, reasoning_tokens: 0, call_count: 3, total_cost_usd: .02 }
  const row: CostConversation = { ...usage, session_id: 'product-health', workflow_id: '_users/alice/Chats/Code/projects/test', user_id: 'alice', first_seen: '2026-09-30T06:00:00Z', last_seen: '2026-09-30T06:01:00Z', by_execution: { q1: { ...usage, scope: 'chat', first_seen: '2026-09-30T06:00:00Z', last_seen: '2026-09-30T06:01:00Z', by_model: { muse: usage } } } }
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  try {
    await act(async () => { root.render(<CostConversations rows={[row]} />); await new Promise(resolve => setTimeout(resolve, 10)) })
    expect(host.textContent).toContain('Five-minute health check')
    expect(host.textContent).toContain('1 turn')
    expect(host.textContent).toContain('Fresh input200')
    expect(host.textContent).toContain('Cached input800')
    expect(host.textContent).toContain('80.0% cached')
    expect(agentApi.getUsageTurnMessages).not.toHaveBeenCalled()
    const turns = Array.from(host.querySelectorAll('summary')).find(summary => summary.textContent === 'Per-turn details')!.parentElement as HTMLDetailsElement
    await act(async () => { turns.open = true; turns.dispatchEvent(new Event('toggle')); await new Promise(resolve => setTimeout(resolve, 20)) })
    expect(agentApi.getUsageTurnMessages).toHaveBeenCalledWith('product-health', row.workflow_id, ['q1'])
    expect(host.textContent).toContain('Check the server')
    expect(host.textContent).not.toContain('View chat')
    expect(host.textContent).not.toContain('Chat history')
    expect(host.textContent).not.toContain('Latest prompt size')
    expect(host.textContent).not.toContain('Token details')
    expect(host.textContent).not.toContain('Provider reported')

    vi.mocked(agentApi.getUsageTurnMessages).mockRejectedValueOnce(new Error('denied'))
    await act(async () => { root.render(<CostConversations rows={[{ ...row, by_execution: { other: row.by_execution.q1 } }]} />); await new Promise(resolve => setTimeout(resolve, 20)) })
    expect(host.textContent).toContain('Message not shown')
    expect(host.textContent).not.toContain('Check the server')

  } finally { await act(async () => root.unmount()); host.remove(); vi.unstubAllGlobals() }
})
