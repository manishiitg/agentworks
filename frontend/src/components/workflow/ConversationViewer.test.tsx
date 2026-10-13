// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'

vi.mock('../ui/MarkdownRenderer', () => ({ MarkdownRenderer: ({ content }: { content: string }) => <p>{content}</p> }))

import { ConversationViewer } from './ConversationViewer'

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
    await act(async () => root.render(<ConversationViewer content={JSON.stringify({ messages: [{ role: 'user', content: 'Verify the evidence.' }, { role: 'assistant', content: 'Evidence verified.' }] })} />))
    expect(host.textContent).toContain('Verify the evidence.')
    expect(host.textContent).toContain('Evidence verified.')
    expect(host.textContent).not.toContain('cannot be shown')
  } finally {
    await act(async () => root.unmount())
    host.remove()
  }
})
