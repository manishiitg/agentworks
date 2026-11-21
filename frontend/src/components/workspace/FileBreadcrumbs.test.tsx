// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import { FileBreadcrumbs } from './FileTabs'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// Owner, 20 Nov: the path took most of the header. The Crew folder and _files are dropped and a deep path collapses.
it('shows a short one-line path with the file name last', () => {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  act(() => root.render(<FileBreadcrumbs path="Crew/project-1/_files/wt/deploy/rootless/scripts/build.sh" onReveal={() => {}} />))
  const nav = host.querySelector('nav')!
  expect([...nav.querySelectorAll('button')].map(b => b.textContent)).toEqual(['wt', 'rootless', 'scripts'])
  expect(nav.textContent).toContain('…')
  expect(nav.lastElementChild?.textContent).toBe('build.sh')
  expect(nav.getAttribute('title')).toBe('wt/deploy/rootless/scripts/build.sh')
  act(() => root.render(<FileBreadcrumbs path="Crew/project-1/_files/notes/a.md" onReveal={() => {}} />))
  expect([...host.querySelectorAll('nav button')].map(b => b.textContent)).toEqual(['notes'])
  act(() => root.unmount())
})
