// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import { ChatChangesRow } from './ChatChangesRow'
import type { CrewPanelSection } from '../../products/work/useCrewPanel'

const section = { id: 'changes', title: 'Git', detail: '2 repositories', rows: [
  { id: 'a', label: 'app', detail: 'main', count: 'up to date', path: 'app' },
  { id: 'b', label: 'site', detail: 'main', count: '3 behind', path: 'site' },
] } as unknown as CrewPanelSection

it('the Git title shows or hides the repositories; only a repository opens files', () => {
  ;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
  localStorage.clear()
  const opened: Array<string | undefined> = []
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  act(() => root.render(<ChatChangesRow section={section} storageKey="test.git-row" onOpen={(repo) => opened.push(repo)} />))
  expect(host.textContent).toContain('site')
  const title = [...host.querySelectorAll('button')].find(b => b.textContent?.includes('Git'))!
  act(() => title.click())
  expect(opened).toEqual([])
  expect(host.textContent).not.toContain('site')
  act(() => title.click())
  const repo = [...host.querySelectorAll('button')].find(b => b.textContent?.includes('site'))!
  expect(repo.querySelector('[aria-label="Files"]')).not.toBeNull()
  act(() => repo.click())
  expect(opened).toEqual(['site'])
  act(() => root.unmount())
})
