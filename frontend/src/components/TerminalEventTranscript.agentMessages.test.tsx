// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import type { PollingEvent } from '../services/api-types'
import { TerminalEventTranscript } from './TerminalEventTranscript'

vi.mock('react-virtuoso', () => ({
  Virtuoso: ({ data, firstItemIndex, itemContent }: { data: unknown[]; firstItemIndex: number; itemContent: (index: number, item: unknown) => React.ReactNode }) => (
    <div>{data.map((item, index) => <div key={index}>{itemContent(firstItemIndex + index, item)}</div>)}</div>
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
  const metadata = { source: 'agent_message', sender_label: 'Reports Builder chat', agent_message_inbox: inboxID, message_id: messageID,
    agent_message_instructions: `Replies are optional. Your final chat answer is NOT forwarded.`, display_content: body }
  const events = [content, content].map((text, index) => ({
    id: `message-${index}`, type: 'user_message', timestamp: '2026-10-11T08:00:00Z',
    data: { data: { content: text, ...(index === 0 ? { metadata } : {}) } },
  } as PollingEvent))
  try {
    await act(async () => { root.render(<TerminalEventTranscript events={events} terminal={null} />) })
    const card = host.querySelector('[data-testid="agent-inbox-message"]')!
    expect(card).not.toBeNull()
    expect(card.querySelector('header')?.textContent).toContain('Reports Builder chat')
    const messageBody = card.querySelector('header + div > div')!
    expect(messageBody.textContent).toContain('Accepted the full scope.')
    expect(messageBody.textContent).not.toContain(inboxID)
    expect(messageBody.textContent).not.toContain('send_message')
    expect(messageBody.textContent).not.toContain('Final evidence marker.')
    const details = card.querySelector('details')!
    expect(details.open).toBe(false)
    expect(details.textContent).toContain(inboxID)
    expect(details.textContent).toContain(messageID)
    expect(details.textContent).toContain('Your final chat answer is NOT forwarded.')
    await act(async () => { card.querySelector('button')!.click() })
    expect(messageBody.textContent).toBe(body)
    expect(details.open).toBe(false)
    expect(host.querySelectorAll('[data-testid="agent-inbox-message"]')).toHaveLength(1)
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
