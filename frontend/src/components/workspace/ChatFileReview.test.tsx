// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'
const state = vi.hoisted(() => ({ sessionId: 'chat-a' }))
const replies = [{ snapshot_id: 'first', end_sequence: 2 }, { snapshot_id: 'last', end_sequence: 4 }]
vi.mock('../../services/api', () => ({ default: { get: vi.fn(async (_path: string, options: { params: Record<string, unknown> }) => {
  if (options.params.line) return { data: { context: 'main.ts, saved after line 2: new' } }
  return { data: { history: { replies, available: true, last_snapshot_id: 'last' }, comparison: { changes: [{ path: 'main.ts', status: 'M', added: 1, removed: 1 }], snapshot: { status: 'complete' }, diff: '@@ -1,2 +1,2 @@\n same\n-old\n+new\n', note: 'Saved boundaries', skipped_count: 0 }, file_replies: ['last'] } }
}) } }))
vi.mock('../../services/workspaceGit', () => ({ workspaceGitApi: { status: vi.fn(async () => []), diff: vi.fn() } }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: { getState: () => ({ chatTabs: { exact: { sessionId: state.sessionId } } }) } }))
vi.mock('../../utils/workspacePaneChat', () => ({ sendWorkspacePaneMessageToChat: vi.fn(async () => ({})) }))
vi.mock('../../utils/chatIdentity', () => ({ captureChatIdentity: () => ({}), assertChatIdentityCurrent: () => undefined }))
vi.mock('../../hooks/useTheme', () => ({ useTheme: () => ({ theme: 'dark' }) }))
vi.mock('../chat/ReplyFileChanges', () => ({ SavedFile: () => null }))
vi.mock('./FileDiffView', () => ({ FileDiffView: () => null }))
import api from '../../services/api'
import { sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'
import { ChatFileReview } from './ChatFileReview'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

describe('Studio review comments', () => {
  it('pins the displayed historical line and sends only to the originating chat; refuses a replaced chat', async () => {
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<ChatFileReview workspacePath="Crew/example" sessionId="chat-a" tabId="exact" onBrowse={() => undefined} />))
      const button = (text: string) => Array.from(host.querySelectorAll('button')).find(item => item.textContent?.includes(text))!
      await act(async () => button('main.ts').click())
      expect(host.textContent).toContain('Saved reply 2')
      await act(async () => (host.querySelector('[aria-label="Comment on after line 2"]') as HTMLButtonElement).click())
      const box = host.querySelector('textarea')!
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(box, 'Use the cache')
        box.dispatchEvent(new Event('input', { bubbles: true }))
      })
      await act(async () => button('Send to this chat').click())
      expect(api.get).toHaveBeenLastCalledWith('/api/workspace-snapshots', expect.objectContaining({ params: expect.objectContaining({ session_id: 'chat-a', through_snapshot_id: 'last', file: 'main.ts', side: 'after', line: 2 }) }))
      expect(sendWorkspacePaneMessageToChat).toHaveBeenCalledWith({ tabId: 'exact', message: 'main.ts, saved after line 2: new\n\nUse the cache' })
      vi.mocked(sendWorkspacePaneMessageToChat).mockClear()
      state.sessionId = 'replacement'
      await act(async () => button('Looks good').click())
      expect(sendWorkspacePaneMessageToChat).not.toHaveBeenCalled()
      expect(host.textContent).toContain('This chat changed')
    } finally { state.sessionId = 'chat-a'; await act(async () => root.unmount()); host.remove() }
  })
})
