// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'
const state = vi.hoisted(() => ({ sessionId: 'chat-a', reviewed: false, fileCount: 2 }))
const replies = [{ snapshot_id: 'first', end_sequence: 2 }, { snapshot_id: 'last', end_sequence: 4 }]
vi.mock('../../services/api', () => ({ default: { get: vi.fn(async (_path: string, options: { params: Record<string, unknown> }) => {
  if (options.params.line) return { data: { context: 'main.ts, saved after line 2: new' } }
  return { data: { history: { replies, reviews: [], available: true, last_snapshot_id: 'last', latest_snapshot_id: 'last', baseline_snapshot_id: state.reviewed ? 'last' : 'start', can_review: true }, comparison: { changes: Array.from({ length: state.fileCount }, (_, index) => ({ path: index === 0 ? 'main.ts' : index === 1 ? 'second.ts' : `file-${index}.ts`, status: 'M', added: 1, removed: 1 })), snapshot: { status: 'complete' }, diff: options.params.file === 'second.ts' ? '@@ -1 +1 @@\n-old second\n+new second\n' : '@@ -1,2 +1,2 @@\n same\n-old\n+new\n', note: 'Saved boundaries', skipped_count: 0 }, file_replies: ['last'] } }
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
import { workspaceGitApi } from '../../services/workspaceGit'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

describe('Studio review comments', () => {
  it('shows all 50 saved diffs, keeping the picker for 51 files', async () => {
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    try {
      state.fileCount = 50
      await act(async () => root.render(<ChatFileReview key="50" workspacePath="Crew/example" sessionId="chat-a" tabId="exact" onBrowse={() => undefined} />))
      expect(host.querySelectorAll('[aria-label="File versions"]')).toHaveLength(50)
      expect(api.get).toHaveBeenCalledWith('/api/workspace-snapshots', expect.objectContaining({ params: expect.objectContaining({ file: 'file-49.ts', session_id: 'chat-a', through_snapshot_id: 'last', baseline_snapshot_id: 'start' }) }))
      state.fileCount = 51
      await act(async () => root.render(<ChatFileReview key="51" workspacePath="Crew/example" sessionId="chat-a" tabId="exact" onBrowse={() => undefined} />))
      expect(host.querySelectorAll('[aria-label="File versions"]')).toHaveLength(1)
      const last = Array.from(host.querySelectorAll('button')).find(item => item.textContent?.includes('file-50.ts'))!
      await act(async () => last.click())
      expect(host.querySelector('[data-review-file]')?.getAttribute('data-review-file')).toBe('file-50.ts')
    } finally { state.fileCount = 2; await act(async () => root.unmount()); host.remove() }
  })

  it('pins the displayed historical line and sends only to the originating chat; refuses a replaced chat', async () => {
    const host = document.createElement('div'); document.body.append(host)
    let root = createRoot(host)
    try {
      await act(async () => root.render(<ChatFileReview workspacePath="Crew/example" sessionId="chat-a" tabId="exact" onBrowse={() => undefined} />))
      const button = (text: string) => Array.from(host.querySelectorAll('button')).find(item => item.textContent?.includes(text))!
      expect(host.textContent).toContain('Changes in this chat')
      expect(host.textContent).not.toContain('since your last review')
      // Small sets show every file without a click, with the same pinned comparison.
      // The file list only jumps within that view.
      expect(api.get).toHaveBeenCalledWith('/api/workspace-snapshots', expect.objectContaining({ params: expect.objectContaining({ session_id: 'chat-a', through_snapshot_id: 'last', baseline_snapshot_id: 'start', file: 'main.ts' }) }))
      expect(host.textContent).toContain('old')
      expect(host.textContent).not.toContain('Looks good')
      expect(host.querySelector('select')?.value).toBe('changes')
      await act(async () => button('second.ts').click())
      expect(host.textContent).toContain('new second')
      expect(api.get).toHaveBeenLastCalledWith('/api/workspace-snapshots', expect.objectContaining({ params: expect.objectContaining({ file: 'second.ts', through_snapshot_id: 'last', baseline_snapshot_id: 'start' }) }))
      expect(openChatFiles).not.toHaveBeenCalled()
      expect(host.querySelectorAll('[aria-label="File versions"]')).toHaveLength(2)
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
      vi.mocked(sendWorkspacePaneMessageToChat).mockClear()
      await act(async () => (host.querySelector('[aria-label="Comment on after line 2"]') as HTMLButtonElement).click())
      await act(async () => {
        const nextBox = host.querySelector('textarea')!
        Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(nextBox, 'Do not send elsewhere')
        nextBox.dispatchEvent(new Event('input', { bubbles: true }))
      })
      state.sessionId = 'replacement'
      await act(async () => button('Send to this chat').click())
      expect(sendWorkspacePaneMessageToChat).not.toHaveBeenCalled()
      expect(host.textContent).toContain('This chat changed')
      state.sessionId = 'chat-a'

      const chooseVersion = async (value: string) => act(async () => {
        const select = host.querySelector('select')!
        select.value = value; select.dispatchEvent(new Event('change', { bubbles: true }))
      })
      // Only two views: Current is the live file (never a saved copy), Changes the diff.
      expect(Array.from(host.querySelectorAll('select option')).map(option => option.textContent)).toEqual(['Current', 'Changes'])
      await chooseVersion('current')
      expect(agentApi.getPlannerFileContent).toHaveBeenCalledWith('Crew/example/main.ts')
      expect(host.querySelector('[data-testid="file-content"]')?.textContent).toBe('current contents')
      expect(host.querySelector('[data-testid="file-content"]')?.getAttribute('data-editable')).toBe('true')
      await chooseVersion('changes')
      vi.mocked(agentApi.getPlannerFileContent).mockRejectedValueOnce(Object.assign(new Error('Request failed'), { response: { status: 404 } }))
      await chooseVersion('current')
      expect(host.querySelector('[role="alert"]')?.textContent).toBe('This file no longer exists in the project.')
      expect(host.querySelector('[data-testid="file-content"]')).toBeNull()
      await act(async () => root.render(<ChatFileReview workspacePath="Crew/example" sessionId="chat-a" tabId="exact" onBrowse={() => undefined} />))
      // PLAT-746-200: coding review has no review state of its own.
      expect(host.textContent).not.toContain('Mark as reviewed')

      // A folder with a Git repository is reviewed with Git: the first changed
      // file's diff against the latest commit opens without a click.
      await act(async () => root.unmount())
      root = createRoot(host)
      vi.mocked(workspaceGitApi.status).mockResolvedValueOnce([{ root: 'app', branch: 'feature', ahead: 0, behind: 0, files: [{ path: 'lib.py', status: 'modified' }] }])
      vi.mocked(workspaceGitApi.diff).mockResolvedValueOnce({ diff: '@@ -1 +1 @@\n-old git\n+new git\n', truncated: false })
      await act(async () => root.render(<ChatFileReview workspacePath="Crew/example" sessionId="chat-a" tabId="exact" onBrowse={() => undefined} />))
      // This chat opens first, matching the reply card's count; Git scopes stay one click away.
      expect(host.textContent).toContain('Every file this chat changed')
      await act(async () => button('Uncommitted').click())
      expect(host.textContent).toContain('Branch / PR')
      expect(host.textContent).toContain('feature · 1 file not committed yet')
      expect(workspaceGitApi.diff).toHaveBeenCalledWith('Crew/example', 'app', 'lib.py')
      expect(host.textContent).toContain('new git')
      expect(host.textContent).not.toContain('Changes in this chat')
      expect(host.querySelector('select[aria-label="File version"]')).not.toBeNull()
      // A clicked line goes to this chat naming repo, file, side, line and the comparison.
      await act(async () => (host.querySelector('[aria-label="Comment on after line 1"]') as HTMLButtonElement).click())
      await act(async () => {
        const gitBox = host.querySelector('textarea')!
        Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(gitBox, 'Keep the old name')
        gitBox.dispatchEvent(new Event('input', { bubbles: true }))
      })
      await act(async () => button('Send to this chat').click())
      expect(sendWorkspacePaneMessageToChat).toHaveBeenCalledWith({ tabId: 'exact', message: 'In app, file lib.py, after line 1 (changes not committed yet, compared with the latest commit):\n\nKeep the old name' })
    } finally { state.sessionId = 'chat-a'; state.reviewed = false; await act(async () => root.unmount()); host.remove() }
  })
})
