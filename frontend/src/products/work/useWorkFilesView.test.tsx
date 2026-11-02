// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import { useWorkFilesView } from './useWorkFilesView'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
it('keeps Changes across chat switches without moving saved replies or files into another chat', async () => {
  // PLAT-746-286: changing chat reset reviewOpen, leaving Files open in Browse.
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  let choose!: (target: string) => void
  const reference = { workspacePath: 'Crew/example', sessionId: 'chat-a', snapshotId: 'reply-a' }
  function Pane({ scope, sessionId }: { scope: string; sessionId: string }) {
    const [view, select] = useWorkFilesView(scope, 'Crew/example', sessionId)
    choose = select
    return <div data-view={view.kind}>{sessionId}:{view.kind}{view.kind === 'reply' ? `:${view.reference.snapshotId}` : view.kind === 'saved' ? `:${view.selection.change.path}` : ''}</div>
  }
  const show = async (sessionId: string, scope = 'server-a:owner-a:project-a') => act(async () => root.render(<Pane scope={scope} sessionId={sessionId} />))
  try {
    await show('chat-a')
    await act(async () => choose('review'))
    await show('chat-b')
    expect(host.textContent).toBe('chat-b:changes')
    await act(async () => choose('browse'))
    await show('chat-a')
    expect(host.textContent).toBe('chat-a:changes')
    await act(async () => choose(`reply:${JSON.stringify(reference)}`))
    await show('chat-b')
    expect(host.textContent).toBe('chat-b:browse')
    await show('chat-c')
    expect(host.textContent).toBe('chat-c:changes')
    await act(async () => choose(`reply:${JSON.stringify(reference)}`))
    expect(host.textContent).toBe('chat-c:changes') // a foreign reply target is ignored
    await show('chat-a')
    expect(host.textContent).toBe('chat-a:reply:reply-a')
    await act(async () => choose(`saved:${JSON.stringify({ reference, change: { path: 'a.txt', status: 'M' } })}`))
    await show('replacement-session')
    expect(host.textContent).toBe('replacement-session:changes')
    await show('chat-a')
    expect(host.textContent).toBe('chat-a:saved:a.txt')
    for (const scope of ['server-b:owner-a:project-a', 'server-a:owner-b:project-a', 'server-a:owner-a:project-b']) {
      await show('chat-a', scope)
      expect(host.textContent).toBe('chat-a:browse')
    }
  } finally { await act(async () => root.unmount()); host.remove() }
})
