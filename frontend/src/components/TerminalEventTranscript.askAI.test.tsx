// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { PollingEvent } from '../services/api-types'
import { buildAskAIMessage } from '../utils/askAIMessage'
import { TerminalEventTranscript } from './TerminalEventTranscript'

vi.mock('react-virtuoso', () => ({
  Virtuoso: ({ data, firstItemIndex, itemContent }: { data: unknown[]; firstItemIndex: number; itemContent: (index: number, item: unknown) => React.ReactNode }) => (
    <div>{data.map((item, index) => <div key={index}>{itemContent(firstItemIndex + index, item)}</div>)}</div>
  ),
}))
vi.mock('./events/EventDispatcher', () => ({ EventDispatcher: () => null }))
vi.mock('./ui/MarkdownRenderer', () => ({ ConversationMarkdownRenderer: ({ content }: { content: string }) => <div>{content}</div> }))

const cleanups: Array<() => void> = []
afterEach(() => { cleanups.splice(0).forEach(cleanup => cleanup()); vi.unstubAllGlobals() })

describe('transcript Ask AI rows', () => {
  it('collapses Ask AI blocks to their plain-words request', async () => {
    vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
    const host = document.createElement('div')
    document.body.appendChild(host)
    const root = createRoot(host)
    const event = {
      id: 'user-ask-ai', type: 'user_message', timestamp: '2026-09-20T09:55:47+05:30',
      data: {
        data: {
          content: buildAskAIMessage({
            view: 'Autopilot',
            summary: 'Help me understand my check-ups.',
            instructions: 'First read the guide with read_skill(skills=[{"name":"builder-reference"}]).',
          }),
        },
      },
    } as PollingEvent
    await act(async () => { root.render(<TerminalEventTranscript events={[event]} terminal={null} />) })
    cleanups.push(() => { act(() => root.unmount()); host.remove() })

    expect(host.textContent).toContain('Ask AI · Autopilot — Help me understand my check-ups.')
    expect(host.textContent).not.toContain('read_skill')
    expect(host.textContent).toContain('Show full message')
  })
})
