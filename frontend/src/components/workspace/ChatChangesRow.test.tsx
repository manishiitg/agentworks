// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'

const { posted } = vi.hoisted(() => ({ posted: [] as Record<string, string>[] }))
vi.mock('../../services/api', () => ({ default: { post: async (_url: string, _body: unknown, config: { params: Record<string, string> }) => {
  const { op, folder } = config.params
  posted.push(folder ? { op, folder } : { op })
  return { data: { report: { over: false, files: 9200, limit: 10000, checkouts: 3, folders: [], checked_at: '', can_fix: true }, moved_to: 'demo-moved/tmp' } }
} } }))
import { ChatChangesRow, changedLabel, gitAgo, gitStaleDays, sortGitRows } from './ChatChangesRow'
import type { CrewPanelSection } from '../../products/work/useCrewPanel'

const section = { id: 'changes', title: 'Git', detail: '2 repositories', rows: [
  { id: 'a', label: 'app', detail: 'main', count: 'up to date', path: 'app' },
  { id: 'b', label: 'site', detail: 'main', count: '3 behind', path: 'site', changed: true },
] } as unknown as CrewPanelSection

it('the Git title shows or hides the repositories; a repository opens its edits, or its folder when clean', () => {
  ;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
  localStorage.clear()
  const opened: string[] = []
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  act(() => root.render(<ChatChangesRow section={section} storageKey="test.git-row" onOpen={(repo, changed) => opened.push(`${repo}:${changed}`)} onTerminal={() => opened.push('terminal')} />))
  expect(host.textContent).toContain('site')
  const title = [...host.querySelectorAll('button')].find(b => b.textContent?.includes('Git'))!
  act(() => title.click())
  expect(opened).toEqual([])
  expect(host.textContent).not.toContain('site')
  act(() => title.click())
  const repo = [...host.querySelectorAll('button')].find(b => b.textContent?.includes('site'))!
  expect(repo.querySelector('[aria-label="Changes"]')).not.toBeNull()
  act(() => repo.click())
  expect(opened).toEqual(['site:true'])
  const app = [...host.querySelectorAll('button')].find(b => b.textContent?.includes('app'))!
  act(() => app.click())
  expect(opened).toEqual(['site:true', 'app:false'])
  // The terminal opens from the Git line itself, without folding the list.
  act(() => host.querySelector<HTMLButtonElement>('[aria-label="Open terminal"]')!.click())
  expect(opened).toEqual(['site:true', 'app:false', 'terminal'])
  expect(host.textContent).toContain('site')
  act(() => root.unmount())
})

it('shows the last commit and how old the remote check is once it is over a day old', () => {
  const now = Math.floor(Date.now() / 1000)
  const glance = { ...section, rows: [
    { id: 'a', label: 'app', detail: 'main', count: 'up to date', path: 'app', commit_at: now - 2 * 3600, commit: 'Fix login redirect', fetched_at: now - 600 },
    { id: 'b', label: 'site', detail: 'main', count: '144 behind', path: 'site', commit_at: now - 35 * 86400, commit: 'Update pricing', fetched_at: now - 31 * 86400 },
  ] } as unknown as CrewPanelSection
  localStorage.clear()
  const host = document.createElement('div')
  const root = createRoot(host)
  act(() => root.render(<ChatChangesRow section={glance} storageKey="test.git-glance" onOpen={() => {}} />))
  const [app, site] = [...host.querySelectorAll('button')].filter(b => b.querySelector('[aria-label="Files"]'))
  expect(app.textContent).toContain('main · 2h ago')
  expect(app.textContent).toContain('Fix login redirect')
  expect(app.textContent).not.toContain('checked')
  expect(site.textContent).toContain('main · 5w ago')
  expect(site.textContent).toContain('checked 31d ago')
  act(() => root.unmount())
})

it('formats ages', () => {
  expect(gitAgo(1000, 1030)).toBe('now')
  expect(gitAgo(0, 3 * 86400)).toBe('3d ago')
  expect(gitAgo(0, 90 * 86400)).toBe('3mo ago')
  expect(gitStaleDays(undefined, 1e9)).toBe(0)
  expect(gitStaleDays(1e9 - 3600, 1e9)).toBe(0)
  expect(gitStaleDays(1e9 - 2 * 86400, 1e9)).toBe(2)
})

it('shows files not committed yet on the Git line and on each repository', () => {
  ;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
  localStorage.clear()
  const counted = { ...section, detail: '2 repositories · 2 worktrees', changed: 12, rows: [
    { id: 'a', label: 'app', detail: 'main', count: 'up to date', path: 'app' },
    { id: 'b', label: 'site', detail: 'main', count: '1 not pushed', path: 'site', changed: true, changed_files: 12 },
  ] } as unknown as CrewPanelSection
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  const opened: string[] = []
  act(() => root.render(<ChatChangesRow section={counted} storageKey="test.git-count" onOpen={(repo, changed) => opened.push(`${repo}:${changed}`)} />))
  expect(host.textContent).toContain('2 repositories · 2 worktrees')
  // The count is its own button: one review of every uncommitted file, and the list stays open.
  const count = [...host.querySelectorAll('button')].find(b => b.getAttribute('aria-label') === '12 changed files: review them')!
  expect(count.textContent).toBe('±12')
  act(() => count.click())
  expect(opened).toEqual(['undefined:true'])
  expect(host.textContent).toContain('site')
  expect(host.textContent).toContain('12 changed · 1 not pushed')
  act(() => root.render(<ChatChangesRow section={{ ...counted, changed: 0 }} storageKey="test.git-count" onOpen={() => {}} />))
  expect(host.querySelector('[aria-label$="review them"]')).toBeNull()
  expect(changedLabel(1, 'changed files')).toBe('1 changed file')
  expect(changedLabel(0, 'changed')).toBe('')
  act(() => root.unmount())
})

it('sorts repositories with edits first, then by latest commit, keeping worktrees together', () => {
  const rows = [
    { id: 'a', label: 'old', commit_at: 100 },
    { id: 'b', label: 'dev', group: 'app', commit_at: 50 },
    { id: 'c', label: 'fix', group: 'app', commit_at: 40 },
    { id: 'd', label: 'new', commit_at: 900 },
    { id: 'e', label: 'edited', commit_at: 10, changed: true, changed_files: 2 },
  ] as unknown as CrewPanelSection['rows']
  expect(sortGitRows(rows).map(r => r.label)).toEqual(['edited', 'new', 'old', 'dev', 'fix'])
  // A worktree with edits brings its whole repository up, main checkout still first.
  rows[2] = { ...rows[2], changed: true }
  expect(sortGitRows(rows).map(r => r.label)).toEqual(['dev', 'fix', 'edited', 'new', 'old'])
})

it('#3878: over the file limit the Git row says so; Move out asks the agent in chat, Check again asks the server; teammates get no buttons', async () => {
  ;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
  localStorage.clear()
  const limit = { over: true, files: 14200, limit: 10000, checkouts: 3, folders: [{ path: 'tmp', files: 5000 }, { path: 'downloads', files: 1400 }], checked_at: '', can_fix: true }
  posted.length = 0
  const asked: string[] = []
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  act(() => root.render(<ChatChangesRow section={{ ...section, changed: 4, file_limit: limit }} workspacePath="Crew/demo" onAskAgent={async message => { asked.push(message) }} storageKey="test.git-limit" onOpen={() => {}} />))
  expect(host.textContent).toContain('not fully tracked')
  expect(host.textContent).not.toContain('±4')
  expect(host.textContent).toContain('about 14,200 files')
  const move = [...host.querySelectorAll('button')].find(b => b.textContent === 'Move out')!
  await act(async () => { move.click() })
  // Nothing moves from the click: the person and the agent decide in chat.
  expect(posted).toEqual([])
  expect(asked).toHaveLength(1)
  expect(asked[0]).toContain('"tmp"')
  expect(asked[0]).toContain("Don't move or delete anything until I say so")
  const check = [...host.querySelectorAll('button')].find(b => b.textContent === 'Check again')!
  await act(async () => { check.click() })
  expect(posted).toEqual([{ op: 'file_limit_check' }])
  expect(host.textContent).toContain('Checked: all changes are tracked again.')
  act(() => root.render(<ChatChangesRow section={{ ...section, file_limit: { ...limit, can_fix: false } }} workspacePath="Crew/demo" onAskAgent={async () => {}} storageKey="test.git-limit-2" onOpen={() => {}} />))
  expect([...host.querySelectorAll('button')].some(b => b.textContent === 'Move out')).toBe(false)
  expect(host.textContent).toContain('Only the project owner can move or ignore folders')
  expect([...host.querySelectorAll('button')].some(b => b.textContent === 'Ignore')).toBe(false)
  act(() => root.unmount())
})

it('#3979: expanded Git row shows active GitHub account strip without speculative org warnings and opens GitHub settings', () => {
  ;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
  localStorage.clear()
  const gitSection = { ...section, rows: [
    { id: 'a', label: 'platform', detail: 'dev', count: 'up to date', path: 'platform', github_repo: 'acme-core/platform' },
    { id: 'b', label: 'portal-web', detail: 'main', count: 'up to date', path: 'portal-web', github_repo: 'partner-org/portal-web' },
  ] } as unknown as CrewPanelSection
  let openedGitHub = 0
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  act(() => root.render(<ChatChangesRow section={gitSection} storageKey="test.git-gh" onOpen={() => {}} github={{ connected: true, login: 'alex-dev', orgs: ['acme-core'], onOpen: () => { openedGitHub++ } }} />))
  expect(host.textContent).toContain('GitHub: @alex-dev')
  expect(host.textContent).not.toContain('needs another account')
  expect(host.textContent).not.toContain('Not in @alex-dev’s organizations')
  const switchBtn = [...host.querySelectorAll('button')].find(b => b.textContent === 'Switch')!
  act(() => switchBtn.click())
  expect(openedGitHub).toBe(1)

  act(() => root.render(<ChatChangesRow section={gitSection} storageKey="test.git-gh" onOpen={() => {}} github={{ connected: false, onOpen: () => { openedGitHub++ } }} />))
  expect(host.textContent).toContain('GitHub: Not connected')
  const connectBtn = [...host.querySelectorAll('button')].find(b => b.textContent === 'Connect')!
  act(() => connectBtn.click())
  expect(openedGitHub).toBe(2)
  act(() => root.unmount())
})

it('#3878: Ignore adds the folder to the project .gitignore on the server and counts again', async () => {
  ;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
  localStorage.clear()
  posted.length = 0
  const limit = { over: true, files: 14200, limit: 10000, checkouts: 3, folders: [{ path: 'reviews', files: 5000 }], checked_at: '', can_fix: true }
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  act(() => root.render(<ChatChangesRow section={{ ...section, file_limit: limit }} workspacePath="Crew/demo" onAskAgent={async () => {}} storageKey="test.git-ignore" onOpen={() => {}} />))
  const ignore = [...host.querySelectorAll('button')].find(b => b.textContent === 'Ignore')!
  await act(async () => { ignore.click() })
  expect(posted).toEqual([{ op: 'file_limit_ignore', folder: 'reviews' }])
  expect(host.textContent).toContain(".gitignore")
  act(() => root.unmount())
})
