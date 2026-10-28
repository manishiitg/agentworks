// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'
const state = vi.hoisted(() => ({ canBuild: true, total: 2, undoError: '' }))
const group = { kind: 'repo', root: 'app', name: 'app', branch: 'fix-tts', new_checkout: true, total_files: 696, more_count: 3, files: [{ path: 'app/tts.py', status: 'M', added: 4, removed: 1 }], pr: { number: 305, url: 'https://github.com/o/r/pull/305', state: 'open', checks: 'passing', review: 'none' } }
vi.mock('../../api/commands', () => ({ commandsApi: { listCommands: vi.fn(async () => ({ commands: [
  { frontmatter: { name: 'check-this-change', description: 'Check this change', show_on_card: true }, content: 'Look over the files.\n{{context}}' },
  { frontmatter: { name: 'other', description: 'Hidden one', show_on_card: false }, content: 'x' }] })) } }))
vi.mock('../../services/api', () => ({ default: { post: vi.fn(async () => { if (state.undoError) throw { response: { data: { error: { message: state.undoError } } } }; return { data: { undo: { restore: [{ path: 'app/tts.py' }], skipped: [] } } } }), get: vi.fn(async (path: string, options: { params: Record<string, unknown> }) => {
  if (options.params.op === 'snapshot_undo_preview') return { data: { undo: { restore: [{ path: 'app/tts.py', status: 'M', action: 'restore' }], skipped: [{ path: 'app/x.py', status: 'M', action: 'restore', reason: 'changed_later' }] } } }
  if (path === '/api/workspace-access') return { data: { can_build: state.canBuild } }
  if (options.params.op === 'snapshot_groups') return { data: { groups: [{ kind: 'crew_files', root: '', name: 'Crew', new_checkout: false, total_files: 1, more_count: 0, files: [{ path: 'notes.md', status: 'A', added: 2, removed: 0 }] }, group] } }
  return { data: { history: { replies: [], reviews: [], available: true, last_snapshot_id: 's', latest_snapshot_id: 's', baseline_snapshot_id: 'x', can_review: true }, total_changes: state.total, comparison: { changes: state.total ? [{ path: 'app/tts.py', status: 'M' }] : [], snapshot: { status: 'complete' }, note: '', skipped_count: 0, uncaptured_changes: 0 } } }
}) } }))
vi.mock('../../products/work/codeLocalFiles', () => ({ useProjectMode: () => 'dev' }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: (s: unknown) => unknown) => select({ user: { id: 'u' } }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: (select: (s: unknown) => unknown) => select({ activeWorkspaceId: 'w' }) }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: { getState: () => ({ chatTabs: { tab1: { tabId: 'tab1', sessionId: 'chat-a' } } }) } }))
vi.mock('../../utils/workspacePaneChat', () => ({ sendWorkspacePaneMessageToChat: vi.fn(async () => ({})), openWorkspacePaneChatWithDraft: vi.fn(async () => ({ tabId: 'tab1' })) }))
vi.mock('../../utils/chatIdentity', () => ({ captureChatIdentity: () => 0, assertChatIdentityCurrent: () => undefined }))
vi.mock('../../utils/projectIntegrationNavigation', () => ({ openProjectIntegrations: vi.fn() }))
vi.mock('../workspace/chatFileNavigation', () => ({ openChatFiles: vi.fn() }))
vi.mock('../workspace/chatFileReview', () => ({ markFilesReviewed: vi.fn(), useReviewRevision: () => [0] }))
import { sendWorkspacePaneMessageToChat, openWorkspacePaneChatWithDraft } from '../../utils/workspacePaneChat'
import { ReplyFileChanges } from './ReplyFileChanges'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

async function render(canBuild: boolean, total = 2, latestSnapshotId?: string) {
  state.canBuild = canBuild; state.total = total
  const host = document.createElement('div'); document.body.append(host)
  await act(async () => createRoot(host).render(<ReplyFileChanges reference={{ snapshotId: 's', workspacePath: 'Crew/x', sessionId: 'chat-a', latestSnapshotId }} />))
  return host
}
const button = (host: HTMLElement, text: string) => Array.from(host.querySelectorAll('button')).find(item => item.textContent?.includes(text) || item.getAttribute('aria-label')?.includes(text))

describe('reply card groups', () => {
  it('lists Git checkouts before Crew files and asks the same chat to commit only this reply, never main', async () => {
    const host = await render(true)
    expect(host.textContent).toContain('new copy')
    expect(host.textContent).toContain('+ 3 more')
    expect(host.textContent).toContain('PR #305 · checks passing · no review yet')
    expect(host.textContent!.indexOf('app')).toBeLessThan(host.textContent!.indexOf('not in Git'))
    await act(async () => button(host, 'Commit')!.click())
    expect(sendWorkspacePaneMessageToChat).toHaveBeenCalledWith({ tabId: 'tab1', message: expect.stringContaining('on branch fix-tts. Stage only the files listed here') })
    await act(async () => button(host, 'Push to a branch and open a PR')!.click())
    const push = vi.mocked(sendWorkspacePaneMessageToChat).mock.calls[1][0].message
    for (const text of ['Never push the default branch', '- app/tts.py', 'a summary of what changed and why based on this chat', 'how it was tested', 'pull request link']) expect(push).toContain(text)
  })
  it('shows no Git buttons to people who cannot edit the Crew', async () => {
    const host = await render(false)
    expect(host.textContent).toContain('tts.py')
    expect(button(host, 'Commit')).toBeUndefined()
    expect(button(host, 'Push')).toBeUndefined()
  })
  it('shows no card at all for a reply that changed 0 files', async () => {
    const host = await render(true, 0)
    expect(host.querySelector('section')).toBeNull()
  })
  it('sends a card button with this reply as context, and prefills the composer for + Add button', async () => {
    const host = await render(true)
    vi.mocked(sendWorkspacePaneMessageToChat).mockClear()
    expect(button(host, 'Hidden one')).toBeUndefined()
    await act(async () => button(host, 'Check this change')!.click())
    expect(vi.mocked(sendWorkspacePaneMessageToChat).mock.calls[0][0].message).toContain('Context: this reply changed these files: notes.md, app/tts.py in app on branch fix-tts; PR https://github.com/o/r/pull/305')
    await act(async () => button(host, 'Add button')!.click())
    expect(openWorkspacePaneChatWithDraft).toHaveBeenCalledWith({ tabId: 'tab1', message: expect.stringContaining('Add a button called') })
  })
  it('undo shows what comes back and what stays, then shows the server error text as is', async () => {
    const host = await render(true)
    await act(async () => button(host, 'Undo this reply')!.click())
    expect(host.textContent).toContain('put back 1 file (tts.py) and leave 1 file alone because a later reply changed them (x.py)')
    state.undoError = 'Wait until this reply finishes, or press Stop, then Undo.'
    await act(async () => button(host, 'Undo 1 file')!.click())
    expect(host.textContent).toContain(state.undoError)
    state.undoError = ''
  })
  it('shows no undo or card buttons to people who cannot edit the Crew', async () => {
    const host = await render(false)
    expect(button(host, 'Undo this reply')).toBeUndefined()
    expect(button(host, 'Add button')).toBeUndefined()
  })
  it('shows totals and the header actions; the latest reply is open, an older one is collapsed', async () => {
    const latest = await render(true)
    expect(latest.textContent).toContain('Files changed during this reply · 2 files')
    expect(latest.textContent).toContain('+4')
    for (const text of ['Review changes', 'Commit on the current branch', 'Push to a branch and open a PR', 'Check this change', 'Add button']) expect(button(latest, text)).toBeDefined()
    for (const text of ['Compare', 'Mark as reviewed']) expect(button(latest, text)).toBeUndefined()
    expect(button(latest, 'Undo this reply')).toBeDefined()
    expect(latest.textContent).toContain('PR #305')
    const older = await render(true, 2, 'newer')
    expect(older.textContent).not.toContain('PR #305')
    expect(button(older, 'Check this change')).toBeDefined()
    // Undo lives only inside the opened card, under the file list.
    expect(button(older, 'Undo this reply')).toBeUndefined()
  })
  it('a Run person sees only Review changes in the header', async () => {
    const host = await render(false)
    expect(button(host, 'Review changes')).toBeDefined()
    expect(button(host, 'Commit on the current branch')).toBeUndefined()
  })
})
