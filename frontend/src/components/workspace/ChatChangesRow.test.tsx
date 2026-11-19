// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import { ChatChangesRow, gitAgo, gitStaleDays } from './ChatChangesRow'
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
  act(() => root.render(<ChatChangesRow section={section} storageKey="test.git-row" onOpen={(repo, changed) => opened.push(`${repo}:${changed}`)} />))
  expect(host.textContent).toContain('site')
  const title = [...host.querySelectorAll('button')].find(b => b.textContent?.includes('Git'))!
  act(() => title.click())
  expect(opened).toEqual([])
  expect(host.textContent).not.toContain('site')
  act(() => title.click())
  const repo = [...host.querySelectorAll('button')].find(b => b.textContent?.includes('site'))!
  expect(repo.querySelector('[aria-label="Files"]')).not.toBeNull()
  act(() => repo.click())
  expect(opened).toEqual(['site:true'])
  const app = [...host.querySelectorAll('button')].find(b => b.textContent?.includes('app'))!
  act(() => app.click())
  expect(opened).toEqual(['site:true', 'app:false'])
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
