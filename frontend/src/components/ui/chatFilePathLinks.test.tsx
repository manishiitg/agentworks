// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { ChatFilePaths, ChatFilePathText, createChatFileLookup } from './chatFilePathLinks'
import { ConversationMarkdownRenderer } from './MarkdownRenderer'

import api from '../../services/api'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('links only admitted files in a finished chat, keeps line jumps and drops the old Crew lookup on switch', async () => {
  vi.useFakeTimers()
  const host = document.createElement('div')
  const root = createRoot(host)
  const open = vi.fn()
  const absolute = '/Users/owner/my project/Report.md'
  const reply = [
    'See `' + absolute + '` or [the report](<'+ absolute + '>).',
    'Code: src/main.go:42. Missing: missing.md. Unavailable: `/private/notes.md`.',
    '[Existing share](https://example.com/file?path=existing)',
    '```sh\ncat /elsewhere/demo.md\n```',
  ].join('\n\n')
  const scope = { lookup: createChatFileLookup('Crew/example', 'other-connection'), open }
  const post = vi.spyOn(api, 'post').mockImplementation(async (_route, body) => ({ data: (body as { workspace_path?: string } | undefined)?.workspace_path === 'Crew/example' ? {
    [absolute]: { path: 'Crew/example/_files/Report.md' },
    'src/main.go': { path: 'Crew/example/_files/src/main.go' },
  } : {} }))
  const render = (value: typeof scope, pending = false) => act(async () => root.render(
    <ChatFilePaths.Provider value={value}>
      <ConversationMarkdownRenderer content={reply} chatElementStream={pending ? 'pending' : undefined} />
      {!pending && <ChatFilePathText text={'I opened `' + absolute + '`.'} />}
    </ChatFilePaths.Provider>,
  ))
  const anchors = () => Array.from(host.querySelectorAll('a'))
  const flush = () => act(async () => { await vi.runAllTimersAsync() })
  try {
    await render(scope, true)
    await flush()
    expect(post).not.toHaveBeenCalled()
    await render(scope)
    await flush()
    expect(post).toHaveBeenCalledTimes(1)
    expect(post.mock.calls[0][1]).toEqual({
      workspace_path: 'Crew/example',
      paths: [absolute, 'src/main.go', 'missing.md', '/private/notes.md'],
    })
    expect(post.mock.calls[0][2]).toMatchObject({ connection: 'other-connection', skipSessionContext: true })
    expect(anchors().map(node => node.textContent)).toEqual([absolute, 'the report', 'src/main.go:42', 'Existing share', '`' + absolute + '`'])
    await act(async () => anchors().find(node => node.textContent === 'src/main.go:42')?.click())
    expect(open).toHaveBeenCalledWith({ path: 'src/main.go', line: 42 })
    await render({ lookup: createChatFileLookup('Crew/different'), open })
    await flush()
    expect(anchors().map(node => node.textContent)).toEqual(['Existing share'])
    expect(host.textContent).toContain('missing.md')
  } finally {
    await act(async () => root.unmount())
    post.mockRestore()
    vi.useRealTimers()
  }
})
