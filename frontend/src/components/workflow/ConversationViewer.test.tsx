// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'

vi.mock('../ui/MarkdownRenderer', () => ({ MarkdownRenderer: ({ content }: { content: string }) => <p>{content}</p> }))

import { ConversationViewer, ConversationToolResponseDisplay } from './ConversationViewer'
import { useCapabilitiesStore } from '../../stores/useCapabilitiesStore'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('renders recognized old messages and keeps unsupported backend records out of the conversation', async () => {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<ConversationViewer content={JSON.stringify({ execution_result: 'private backend marker' })} />))
    expect(host.textContent).toContain('This older run’s conversation cannot be shown.')
    expect(host.textContent).not.toContain('private backend marker')
    expect(host.querySelector('pre')).toBeNull()
    await act(async () => root.render(<ConversationViewer content={JSON.stringify({ messages: [{ role: 'user', content: 'Verify the evidence.' }, { role: 'function', content: 'Source checked.' }, { role: 'assistant', content: 'Evidence verified.' }] })} />))
    expect(host.textContent).toContain('Verify the evidence.')
    expect(host.textContent).toContain('Evidence verified.')
    expect(host.textContent).toContain('Source checked.')
    expect(host.textContent).not.toContain('cannot be shown')
  } finally {
    await act(async () => root.unmount())
    host.remove()
  }
})

// Native agents and saved transcripts share this renderer. The human action
// must stay visible even while the long JSON tool result is collapsed.
it('shows the sender review action outside collapsed native tool output', async () => {
  const previous = useCapabilitiesStore.getState().capabilities
  useCapabilitiesStore.setState({ capabilities: { ...previous, local_mode: false } as NonNullable<typeof previous> })
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  try {
    const content = JSON.stringify({ sender_approval: { type: 'gmail_sender_approval', workspace_path: 'Workflow/test' }, details: 'saved rules '.repeat(100) })
    await act(async () => root.render(<ConversationToolResponseDisplay toolName="mcp__agentworks__manage_gmail_trigger" content={content} />))
    const review = [...host.querySelectorAll('button')].find(button => button.textContent === 'Review and approve senders')
    expect(review).toBeDefined()
    expect(review?.getAttribute('aria-expanded')).toBe('false')
    expect(host.querySelector('pre')).toBeNull()
  } finally {
    await act(async () => root.unmount())
    host.remove()
    useCapabilitiesStore.setState({ capabilities: previous })
  }
})
