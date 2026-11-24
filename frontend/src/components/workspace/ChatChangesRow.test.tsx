// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
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
