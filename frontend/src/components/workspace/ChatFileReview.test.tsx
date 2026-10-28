// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'
const state = vi.hoisted(() => ({ sessionId: 'chat-a', reviewed: false }))
const replies = [{ snapshot_id: 'first', end_sequence: 2 }, { snapshot_id: 'last', end_sequence: 4 }]
vi.mock('../../services/api', () => ({ default: { get: vi.fn(async (_path: string, options: { params: Record<string, unknown> }) => {
  if (options.params.op === 'snapshot_file') return { data: new TextEncoder().encode('saved contents').buffer }
  if (options.params.line) return { data: { context: 'main.ts, saved after line 2: new' } }
  return { data: { history: { replies, reviews: [], available: true, last_snapshot_id: 'last', latest_snapshot_id: 'last', baseline_snapshot_id: state.reviewed ? 'last' : 'start', can_review: true }, comparison: { changes: [{ path: 'main.ts', status: 'M', added: 1, removed: 1 }], snapshot: { status: 'complete' }, diff: '@@ -1,2 +1,2 @@\n same\n-old\n+new\n', note: 'Saved boundaries', skipped_count: 0 }, file_replies: ['last'] } }
}), post: vi.fn(async () => { state.reviewed = true; return { data: {} } }) }, agentApi: { getPlannerFileContent: vi.fn(async () => ({ success: true, data: { content: 'current contents' } })) }, workspaceApi: { get: vi.fn() } }))
vi.mock('../../services/workspaceGit', () => ({ workspaceGitApi: { status: vi.fn(async () => []), diff: vi.fn() } }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: { getState: () => ({ chatTabs: { exact: { sessionId: state.sessionId } } }) } }))
vi.mock('../../utils/workspacePaneChat', () => ({ sendWorkspacePaneMessageToChat: vi.fn(async () => ({})) }))
vi.mock('../../utils/chatIdentity', () => ({ captureChatIdentity: () => ({}), assertChatIdentityCurrent: () => undefined }))
vi.mock('../../hooks/useTheme', () => ({ useTheme: () => ({ theme: 'dark' }) }))
vi.mock('../FileContentViewer', () => ({ FileContentViewerBody: ({ source, editable }: { source: { fileContent: string }; editable: boolean }) => <pre data-testid="file-content" data-editable={String(Boolean(editable))}>{source.fileContent}</pre> }))
vi.mock('./chatFileNavigation', () => ({ openChatFiles: vi.fn() }))
vi.mock('./FileDiffView', () => ({ FileDiffView: () => null }))
import api, { agentApi } from '../../services/api'
import { openChatFiles } from './chatFileNavigation'
import { ReplyFileViewer } from './ReplyFileViewer'
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
      expect(host.textContent).toContain('Changes in this chat')
      expect(host.textContent).not.toContain('since your last review')
      await act(async () => button('main.ts').click())
      expect(openChatFiles).toHaveBeenCalledWith('chat-a', expect.objectContaining({ query: { session_id: 'chat-a', through_snapshot_id: 'last', baseline_snapshot_id: 'start' } }))
      expect(host.querySelector('select')).toBeNull()
      expect(host.textContent).not.toContain('Uncommitted')
      const selection = { reference: { workspacePath: 'Crew/example', sessionId: 'chat-a', snapshotId: '' }, change: { path: 'main.ts', status: 'M' }, query: { session_id: 'chat-a', through_snapshot_id: 'last', baseline_snapshot_id: 'start' } }
      await act(async () => root.render(<ReplyFileViewer selection={selection} tabId="exact" onBack={() => undefined} />))
      expect(host.querySelector('dialog')).toBeNull()
      await act(async () => (host.querySelector('[aria-label="Comment on after line 2"]') as HTMLButtonElement).click())
      const box = host.querySelector('textarea')!
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(box, 'Use the cache')
        box.dispatchEvent(new Event('input', { bubbles: true }))
      })
      await act(async () => button('Send to this chat').click())
      expect(api.get).toHaveBeenLastCalledWith('/api/workspace-snapshots', expect.objectContaining({ params: expect.objectContaining({ session_id: 'chat-a', through_snapshot_id: 'last', baseline_snapshot_id: 'start', file: 'main.ts', side: 'after', line: 2 }) }))
      expect(sendWorkspacePaneMessageToChat).toHaveBeenCalledWith({ tabId: 'exact', message: 'main.ts, saved after line 2: new\n\nUse the cache' })
      const chooseVersion = async (value: string) => act(async () => {
        const select = host.querySelector('select')!
        select.value = value; select.dispatchEvent(new Event('change', { bubbles: true }))
      })
      await chooseVersion('after')
      expect(host.querySelector('[data-testid="file-content"]')?.textContent).toBe('saved contents')
      expect(host.querySelector('[data-testid="file-content"]')?.getAttribute('data-editable')).toBe('false')
      await chooseVersion('current')
      expect(agentApi.getPlannerFileContent).toHaveBeenCalledWith('Crew/example/main.ts')
      expect(host.querySelector('[data-testid="file-content"]')?.textContent).toBe('current contents')
      expect(host.querySelector('[data-testid="file-content"]')?.getAttribute('data-editable')).toBe('true')
      await chooseVersion('before')
      vi.mocked(agentApi.getPlannerFileContent).mockRejectedValueOnce(new Error('This file no longer exists. Browse files or choose a saved version.'))
      await chooseVersion('current')
      expect(host.querySelector('[role="alert"]')?.textContent).toContain('no longer exists')
      await act(async () => root.render(<ChatFileReview workspacePath="Crew/example" sessionId="chat-a" tabId="exact" onBrowse={() => undefined} />))
      await act(async () => button('Mark as reviewed').click())
      expect(api.post).toHaveBeenCalledWith('/api/workspace-snapshots', null, { params: { workspace_path: 'Crew/example', session_id: 'chat-a', snapshot_id: 'last', op: 'snapshot_review' } })
      expect(host.textContent).toContain('Reviewed up to here')
      vi.mocked(sendWorkspacePaneMessageToChat).mockClear()
      state.sessionId = 'replacement'
      await act(async () => button('Looks good').click())
      expect(sendWorkspacePaneMessageToChat).not.toHaveBeenCalled()
      expect(host.textContent).toContain('This chat changed')
    } finally { state.sessionId = 'chat-a'; state.reviewed = false; await act(async () => root.unmount()); host.remove() }
  })
})
