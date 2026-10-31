// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'
const state = vi.hoisted(() => ({ canBuild: true, total: 2, undoError: '', phone: false, extraCommands: [] as string[], pr: {} as Record<string, unknown>, nextStep: 'none' }))
const pr = { number: 305, url: 'https://github.com/o/r/pull/305', state: 'open', checks: 'passing', review: 'none' }
const group = { kind: 'repo' as const, root: 'app', name: 'app', branch: 'fix-tts', github_repo: 'o/r', new_checkout: true, total_files: 696, more_count: 3, files: [{ path: 'app/tts.py', status: 'M', added: 4, removed: 1 }] }
const crewFiles = { kind: 'crew_files' as const, root: '', name: 'Crew', new_checkout: false, total_files: 1, more_count: 0, files: [{ path: 'notes.md', status: 'A', added: 2, removed: 0 }] }
const review = { id: 'review', kind: 'builtin', label: 'Review changes', description: 'See every file this reply changed, line by line.', icon: 'octicon:file-diff', placement: 'row' } as const
// The list the server decides (filesnapshots.CardActions); the card only draws it.
function replyCard(lookedUp: boolean) {
  const status = { ...pr, ...state.pr }
  const actions: Record<string, unknown>[] = [review]
  if (state.canBuild) {
    actions.push({ id: 'commit', kind: 'builtin', label: 'Commit on the current branch', description: 'Ask the agent to commit these files on fix-tts. Nothing is pushed.', icon: 'octicon:git-commit', placement: 'row', prompt: 'commit prompt' },
      { id: 'push', kind: 'builtin', label: 'Push to a branch and open a PR', description: 'Ask the agent to push a branch and open a pull request with a full description.', icon: 'octicon:git-pull-request', placement: 'row', prompt: 'push prompt' })
    if (lookedUp && state.nextStep === 'update_pr') actions.push({ id: 'next:app', kind: 'git_step', label: 'Update the pull request', icon: 'octicon:git-pull-request', placement: 'more', prompt: 'update prompt', group: 'app' })
    if (lookedUp && status.checks === 'failing') actions.push({ id: 'fix-checks:app', kind: 'fix', label: 'Fix failing checks', icon: 'wrench', placement: 'more', prompt: 'fix prompt', group: 'app' })
    const commands = ['check-this-change', ...state.extraCommands]
    commands.forEach((name, i) => actions.push({ id: `command:${name}`, kind: 'command', label: name, aria_label: name === 'check-this-change' ? 'Check this change' : `Run ${name}`, description: `Run ${name}`, placement: i < 2 ? 'row' : 'more', prompt: `${name} prompt` }))
    actions.push({ id: 'add_button', kind: 'builtin', label: 'Add button', description: 'Save a reusable button on this Crew\'s file-change toolbar.', icon: 'plus', placement: 'row', prompt: 'Add an AgentWorks reply-card button using crew-builder and manage_custom_commands with show_on_card=true.' })
    if (lookedUp && status.checks === 'failing') actions.push({ id: 'fix-checks:app:link', kind: 'fix', label: 'Fix failing checks', icon: 'wrench', placement: 'pr_line', prompt: 'fix prompt', group: 'app' })
  }
  return { groups: [crewFiles, lookedUp ? { ...group, pr: status } : group], actions, can_edit: state.canBuild }
}
vi.mock('./PRWatcherControls', () => ({ PRWatcherControls: () => null }))
vi.mock('../../services/api', () => ({ default: { post: vi.fn(async () => { if (state.undoError) throw { response: { data: { error: { message: state.undoError } } } }; return { data: { undo: { restore: [{ path: 'app/tts.py' }], skipped: [] } } } }), get: vi.fn(async (_path: string, options: { params: Record<string, unknown> }) => {
  if (options.params.op === 'snapshot_undo_preview') return { data: { undo: { restore: [{ path: 'app/tts.py', status: 'M', action: 'restore' }], skipped: [{ path: 'app/x.py', status: 'M', action: 'restore', reason: 'changed_later' }] } } }
  if (options.params.op === 'reply_card') return { data: replyCard(options.params.pull_requests === true) }
  return { data: { history: { replies: [], reviews: [], available: true, last_snapshot_id: 's', latest_snapshot_id: 's', baseline_snapshot_id: 'x', can_review: true }, total_changes: state.total, comparison: { changes: state.total ? [{ path: 'app/tts.py', status: 'M' }] : [], snapshot: { status: 'complete' }, note: '', skipped_count: 0, uncaptured_changes: 0 } } }
}) } }))
vi.mock('../../products/work/codeLocalFiles', () => ({ useProjectMode: () => 'dev' }))
vi.mock('../../hooks/useMobileViewport', () => ({ useMobileViewport: () => state.phone }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: (s: unknown) => unknown) => select({ user: { id: 'u' } }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: (select: (s: unknown) => unknown) => select({ activeWorkspaceId: 'w' }) }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: { getState: () => ({ chatTabs: { tab1: { tabId: 'tab1', sessionId: 'chat-a' } } }) } }))
vi.mock('../../utils/workspacePaneChat', () => ({ sendWorkspacePaneMessageToChat: vi.fn(async () => ({})), openWorkspacePaneChatWithDraft: vi.fn(async () => ({ tabId: 'tab1' })) }))
vi.mock('../../utils/chatIdentity', () => ({ captureChatIdentity: () => 0, assertChatIdentityCurrent: () => undefined }))
vi.mock('../../utils/projectIntegrationNavigation', () => ({ openProjectIntegrations: vi.fn() }))
vi.mock('../workspace/chatFileNavigation', () => ({ openChatFiles: vi.fn(), openReplyChanges: vi.fn() }))
vi.mock('../workspace/chatFileReviewData', () => ({ markFilesReviewed: vi.fn(), useReviewRevision: () => [0] }))
import { sendWorkspacePaneMessageToChat, openWorkspacePaneChatWithDraft } from '../../utils/workspacePaneChat'
import { openChatFiles, openReplyChanges } from '../workspace/chatFileNavigation'
import api from '../../services/api'
import { openProjectIntegrations } from '../../utils/projectIntegrationNavigation'
import { githubConnectionChanged } from '../../utils/githubConnectionEvents'
import { ReplyFileChanges } from './ReplyFileChanges'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
HTMLElement.prototype.scrollIntoView ??= () => {}

// The reply event carries the summary the server computed at capture time, with the actions every viewer gets.
// Chat shows a compact toolbar; the right panel retains the detailed actions and file list.
async function render(canBuild: boolean, total = 2, latestSnapshotId?: string, withSummary = true, opened = true) {
  state.canBuild = canBuild; state.total = total
  const summary = withSummary ? { files: total, added: 6, removed: 1, groups: total ? [crewFiles, group] : [], actions: [review] } : undefined
  const host = document.createElement('div'); document.body.append(host)
  await act(async () => createRoot(host).render(<ReplyFileChanges reference={{ snapshotId: 's', workspacePath: 'Crew/x', sessionId: 'chat-a', latestSnapshotId, summary }} panel={opened} />))
  return host
}
const openMore = async (host: HTMLElement) => act(async () => { const trigger = button(host, 'More actions')!; trigger.focus(); trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true })) })
const menuItems = () => Array.from(document.querySelectorAll('[role="menuitem"]')).map(item => item.textContent)
const menuItem = (text: string) => Array.from(document.querySelectorAll('[role="menuitem"]')).find(item => item.textContent?.includes(text)) as HTMLElement | undefined
const snapshotOps = () => vi.mocked(api.get).mock.calls.map(call => (call[1] as { params: Record<string, unknown> } | undefined)?.params).filter(params => params?.op).map(params => `${params!.op}${params!.pull_requests ? '+pr' : ''}`)
const button = (host: HTMLElement, text: string) => Array.from(host.querySelectorAll('button')).find(item => item.textContent?.includes(text) || item.getAttribute('aria-label')?.includes(text))
const rowLabels = (host: HTMLElement) => Array.from(host.querySelectorAll('section > div:first-child button[aria-label]')).map(item => item.getAttribute('aria-label'))

describe('reply card groups', () => {
  it('lists Git checkouts before Crew files and sends the server\'s Commit and Push requests to the same chat', async () => {
    const host = await render(true)
    expect(host.textContent).toContain('new copy')
    expect(host.textContent).toContain('+ 3 more')
    expect(host.textContent).toContain('PR #305 · checks passing · no review yet')
    expect(host.textContent!.indexOf('app')).toBeLessThan(host.textContent!.indexOf('not in Git'))
    vi.mocked(sendWorkspacePaneMessageToChat).mockClear()
    await act(async () => button(host, 'Commit')!.click())
    expect(sendWorkspacePaneMessageToChat).toHaveBeenCalledWith({ tabId: 'tab1', message: 'commit prompt' })
    await act(async () => button(host, 'Push to a branch and open a PR')!.click())
    expect(vi.mocked(sendWorkspacePaneMessageToChat).mock.calls[1][0].message).toBe('push prompt')
    await act(async () => button(host, 'Review changes')!.click())
    expect(openChatFiles).toHaveBeenCalledWith('chat-a')
  })
  it('shows no card at all for a reply that changed 0 files', async () => {
    const host = await render(true, 0)
    expect(host.querySelector('section')).toBeNull()
  })
  it('sends a card button\'s prompt as is, and prefills the composer for + Add button', async () => {
    const host = await render(true)
    vi.mocked(sendWorkspacePaneMessageToChat).mockClear()
    await act(async () => button(host, 'Check this change')!.click())
    expect(vi.mocked(sendWorkspacePaneMessageToChat).mock.calls[0][0].message).toBe('check-this-change prompt')
    await act(async () => button(host, 'Add button')!.click())
    expect(openWorkspacePaneChatWithDraft).toHaveBeenCalledWith({ tabId: 'tab1', message: 'Add an AgentWorks reply-card button using crew-builder and manage_custom_commands with show_on_card=true.' })
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
  it('a Run person sees only Review changes in the header, and no undo', async () => {
    const host = await render(false)
    expect(host.textContent).toContain('tts.py')
    expect(rowLabels(host)).toEqual(['Review changes'])
    expect(button(host, 'More actions')).toBeUndefined()
    expect(button(host, 'Undo this reply')).toBeUndefined()
    // PLAT-746-261: shared Run users cannot open the workspace Files panel.
    // Preserve their existing filename inspection without opening that panel.
    const shared = await render(false, 2, undefined, true, false)
    vi.mocked(openReplyChanges).mockClear()
    await act(async () => button(shared, 'Open 2 files changed in this reply')!.click())
    expect(shared.querySelector('[aria-label="Reply file names"]')?.textContent).toContain('app/tts.py')
    expect(openReplyChanges).not.toHaveBeenCalled()
    expect(button(shared, 'Undo this reply')).toBeUndefined()
  })
  it('retains the original action toolbar while file details open in the panel', async () => {
    const closed = await render(true, 2, undefined, true, false)
    expect(closed.querySelector('[aria-label="Reply changes"]')).not.toBeNull()
    expect(button(closed, 'Undo this reply')).toBeUndefined()
    const latest = await render(true)
    expect(latest.textContent).toContain('2 files')
    expect(latest.textContent).toContain('+4')
    expect(rowLabels(latest)).toEqual(['Review changes', 'Commit on the current branch', 'Push to a branch and open a PR', 'Check this change', 'Add button'])
    expect(latest.querySelector('section > div:first-child [aria-hidden].w-px')).not.toBeNull()
    expect(button(latest, 'Undo this reply')).toBeDefined()
    expect(latest.textContent).toContain('PR #305')
    const older = await render(true, 2, 'newer', true, false)
    expect(older.querySelector('a[aria-label*="PR #305"]')).not.toBeNull()
    expect(button(older, 'Check this change')).toBeDefined()
    // Undo lives only inside the opened card, under the file list.
    expect(button(older, 'Undo this reply')).toBeUndefined()
  })
  it('opens the exact saved reply from the compact icon and keeps PR shortcuts visible without an inline file list', async () => {
    vi.mocked(api.get).mockClear()
    const compact = await render(true, 2, 'newer', true, false)
    expect(compact.textContent).toContain('fix-tts')
    expect(compact.textContent).not.toContain('tts.py')
    expect(compact.querySelector('a[title="Review comments"]')).not.toBeNull()
    expect(snapshotOps()).toEqual(['reply_card', 'reply_card+pr'])
    await act(async () => button(compact, 'Open 2 files changed in this reply')!.click())
    expect(openReplyChanges).toHaveBeenLastCalledWith(expect.objectContaining({ snapshotId: 's', sessionId: 'chat-a', workspacePath: 'Crew/x' }))
    expect(compact.textContent).not.toContain('tts.py')
    expect(button(compact, 'Commit on the current branch')).toBeDefined()
    expect(button(compact, 'Check this change')).toBeDefined()
    await act(async () => button(compact, 'Review changes')!.click())
    expect(openReplyChanges).toHaveBeenLastCalledWith(expect.objectContaining({ snapshotId: 's', sessionId: 'chat-a' }))
    const panel = await render(true)
    expect(panel.textContent).toContain('tts.py')
    expect(button(panel, 'Undo this reply')).toBeDefined()
  })
  it('keeps the server\'s row buttons in the row and the rest, with the next Git step first, under More', async () => {
    state.extraCommands = ['second', 'third', 'fourth']; state.nextStep = 'update_pr'
    const host = await render(true)
    vi.mocked(sendWorkspacePaneMessageToChat).mockClear()
    expect(button(host, 'Run second')).toBeDefined()
    expect(button(host, 'Run third')).toBeUndefined()
    await openMore(host)
    expect(menuItems()).toEqual(['Update the pull request', 'third', 'fourth'])
    await act(async () => menuItem('fourth')!.click())
    expect(vi.mocked(sendWorkspacePaneMessageToChat).mock.calls[0][0].message).toBe('fourth prompt')
    state.extraCommands = []; state.nextStep = 'none'
  })
  it('on a phone the row keeps the defaults and the person\'s buttons and Add move under More', async () => {
    state.phone = true; state.extraCommands = ['second', 'third']; state.nextStep = 'update_pr'
    const host = await render(true)
    expect(rowLabels(host)).toEqual(['Review changes', 'Commit on the current branch', 'Push to a branch and open a PR', 'More actions'])
    expect(host.querySelector('section > div:first-child [aria-hidden].w-px')).toBeNull()
    await openMore(host)
    expect(menuItems()).toEqual(['Update the pull request', 'check-this-change', 'second', 'third', 'Add button'])
    state.phone = false; state.extraCommands = []; state.nextStep = 'none'
  })
  it('a fix action shows on the PR line and under More when the server sends it', async () => {
    state.pr = { checks: 'failing', failing_checks: ['build', 'lint'] }
    const host = await render(true)
    vi.mocked(sendWorkspacePaneMessageToChat).mockClear()
    await act(async () => button(host, 'Fix failing checks')!.click())
    expect(vi.mocked(sendWorkspacePaneMessageToChat).mock.calls[0][0].message).toBe('fix prompt')
    await openMore(host)
    expect(menuItem('Fix failing checks')).toBeDefined()
    expect(button(await render(false), 'Fix failing checks')).toBeUndefined()
    state.pr = {}
  })
  it('offers account recovery and retries failed PR status after an explicit connection change', async () => {
    state.pr = { state: 'unavailable', number: undefined, url: undefined }
    try {
      const host = await render(true)
      await act(async () => button(host, 'Change GitHub account')!.click())
      expect(openProjectIntegrations).toHaveBeenCalledWith('github', 'chat-a')
      const lookups = () => snapshotOps().filter(op => op === 'reply_card+pr').length
      const before = lookups()
      await act(async () => button(host, 'Try again')!.click())
      expect(lookups()).toBeGreaterThan(before)
      const retried = lookups()
      await act(async () => window.dispatchEvent(new Event(githubConnectionChanged)))
      expect(lookups()).toBeGreaterThan(retried)
      const shared = await render(false)
      expect(button(shared, 'Change GitHub account')).toBeUndefined()
      expect(shared.textContent).toContain('Ask the owner')
    } finally { state.pr = {} }
  })

  it('refreshes a visible card on return and shows commented reviews without claiming approval', async () => {
    state.pr = {}
    const host = await render(true)
    expect(host.textContent).toContain('no review yet')
    try {
      state.pr = { review: 'commented' }
      await act(async () => window.dispatchEvent(new Event('focus')))
      expect(host.textContent).toContain('review comments')
      expect(host.textContent).not.toContain('no review yet')
      expect(host.textContent).not.toContain('approved')
    } finally { state.pr = {} }
  })

})

it('keeps a visible explanation when saved file changes fail to load', async () => {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  state.total = 2
  vi.mocked(api.get).mockRejectedValueOnce(new Error('unavailable'))
  try {
    await act(async () => root.render(<ReplyFileChanges reference={{ snapshotId: 'missing', workspacePath: 'Crew/x', sessionId: 'chat-a' }} />))
    expect(host.textContent).toContain('Saved file changes for this reply could not be loaded.')
    await act(async () => button(host, 'Review chat changes')!.click())
    expect(openChatFiles).toHaveBeenLastCalledWith('chat-a')
  } finally { await act(async () => root.unmount()); host.remove() }
})
