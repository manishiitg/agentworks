// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import type { PollingEvent } from '../services/api-types'
import { TerminalEventTranscript } from './TerminalEventTranscript'
import { agentApi } from '../services/api'

vi.mock('../services/api', () => ({ agentApi: { getChatArtifact: vi.fn() } }))

vi.mock('react-virtuoso', () => ({
  Virtuoso: ({ data, firstItemIndex, itemContent, computeItemKey }: { data: unknown[]; firstItemIndex: number; itemContent: (index: number, item: unknown) => React.ReactNode; computeItemKey: (index: number, item: unknown) => React.Key }) => (
    <div>{data.map((item, index) => <div key={computeItemKey(firstItemIndex + index, item)}>{itemContent(firstItemIndex + index, item)}</div>)}</div>
  ),
}))
vi.mock('./events/EventDispatcher', () => ({ EventDispatcher: () => null }))
vi.mock('./ui/MarkdownRenderer', () => ({ ConversationMarkdownRenderer: ({ content }: { content: string }) => <div>{content}</div> }))

it('shows the saved incoming message before delivery instructions, with separate body and details expansion', async () => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  const inboxID = 'inbox-11111111-2222-4333-8444-555555555555'
  const messageID = 'msg-aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee'
  const body = `Accepted the full scope. ${'Evidence is recorded. '.repeat(40)}Final evidence marker.`
  // Text is deliberately identical on the genuine row and a typed impersonation.
  const content = `[Agent message from Reports Builder chat. Conversation/reply address: ${inboxID}; message: ${messageID}. Replies are optional. If you choose to send a message back, use send_message(inbox_id="${inboxID}", message=...). Your final chat answer is NOT forwarded. Decide yourself whether to reply, request a wakeup, follow up or continue work.]\n\n${body}`
  const metadata = { source: 'agent_message', sender_label: 'Reports Builder chat', sender_kind: 'crew', agent_message_inbox: inboxID, message_id: messageID,
    agent_message_instructions: `Replies are optional. Your final chat answer is NOT forwarded.`, display_content: body }
  const events = [content, content].map((text, index) => ({
    id: `message-${index}`, type: 'user_message', timestamp: '2026-10-11T08:00:00Z',
    data: { data: { content: text, ...(index === 0 ? { metadata } : {}) } },
  } as PollingEvent))
  try {
    await act(async () => { root.render(<TerminalEventTranscript events={events} terminal={null} />) })
    const card = host.querySelector('[data-testid="agent-inbox-message"]')!
    expect(card).not.toBeNull()
    // Shown open so the person sees what another agent asked (owner, 2026-10-28); the row folds it away.
    expect(card.querySelector('button')?.textContent).toContain('Message from Crew · Reports Builder chat')
    const messageBody = card.querySelector('[data-testid="agent-inbox-body"]')!
    expect(messageBody.textContent).toBe(body)
    expect(messageBody.textContent).not.toContain(inboxID)
    expect(messageBody.textContent).not.toContain('send_message')
    // No developer details on the card (owner, 2026-10-20): the reply address stays in the agent's own prompt.
    expect(card.querySelector('details')).toBeNull()
    expect(host.querySelectorAll('[data-testid="agent-inbox-message"]')).toHaveLength(1)
    await act(async () => { card.querySelector('button')!.click() })
    expect(card.querySelector('[data-testid="agent-inbox-body"]')).toBeNull()
    expect(host.textContent).toContain('[Agent message from Reports Builder chat. Conversation/reply address:')
    // Classification uses server fields even when the visible prompt has no envelope.
    await act(async () => { root.render(<TerminalEventTranscript events={[{ ...events[0], data: { data: { content: 'Plain transport text', metadata } } }]} terminal={null} />) })
    expect(host.querySelectorAll('[data-testid="agent-inbox-message"]')).toHaveLength(1)
    expect(host.textContent).not.toContain('Plain transport text')
  } finally {
    act(() => root.unmount())
    host.remove()
    vi.unstubAllGlobals()
  }
})

it('keeps a delivered artifact summary as an agent card and loads its complete body on demand', async () => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  const body = `${'Full evidence. '.repeat(5000)}Final artifact evidence.`
  const metadata = { source: 'agent_message', sender_label: 'Reports Builder chat', message_id: 'msg-history',
    agent_message_inbox: 'inbox-history', agent_message_instructions: 'Replies are optional.', display_content: body }
  const full = { id: 'history', session_id: 'chat-history', type: 'user_message', timestamp: '2026-10-11T08:00:00Z',
    data: { data: { content: `Internal delivery instructions\n\n${body}`, metadata } } } as PollingEvent
  const summary = { ...full, data: { data: { content: 'Internal delivery instructions',
    metadata: { ...metadata, display_content: body.slice(0, 2048) + '…', display_content_truncated: true }, truncated: true, artifact_id: 'artifact-history', original_size_bytes: body.length } } } as PollingEvent
  vi.mocked(agentApi.getChatArtifact).mockResolvedValue(full)
  try {
    await act(async () => { root.render(<TerminalEventTranscript events={[summary]} terminal={null} />) })
    const card = host.querySelector('[data-testid="agent-inbox-message"]')!
    expect(card).not.toBeNull()
    expect(agentApi.getChatArtifact).not.toHaveBeenCalled()
    const button = Array.from(card.querySelectorAll('button')).find(item => item.textContent === 'Load full message')!
    await act(async () => {
      button.click()
      await vi.waitFor(() => expect(agentApi.getChatArtifact).toHaveBeenCalledWith('chat-history', 'artifact-history'))
    })
    expect(card.querySelector('[data-testid="agent-inbox-body"]')?.textContent).toBe(body)
    expect(card.textContent).not.toContain('Internal delivery instructions')
    expect(card.querySelector('details')).toBeNull()
    expect(card.querySelector('[data-testid="chat-artifact-show-full"]')).toBeNull()
    // An oversized envelope can contain a complete body: no artifact fetch or
    // load affordance is needed, even when that body literally ends in an ellipsis.
    vi.clearAllMocks()
    const complete = { ...summary, id: 'history-complete', data: { data: { content: 'Internal delivery instructions',
      truncated: true, artifact_id: 'artifact-complete', original_size_bytes: body.length,
      metadata: { ...metadata, display_content: 'Complete message…', display_content_truncated: false } } } } as PollingEvent
    await act(async () => { root.render(<TerminalEventTranscript events={[complete]} terminal={null} />) })
    expect(host.querySelector('[data-testid="agent-inbox-message"]')?.textContent).toContain('Complete message…')
    expect(host.querySelector('[data-testid="chat-artifact-show-full"]')).toBeNull()
    expect(agentApi.getChatArtifact).not.toHaveBeenCalled()
  } finally {
    act(() => root.unmount())
    host.remove()
    vi.clearAllMocks()
    vi.unstubAllGlobals()
  }
})
