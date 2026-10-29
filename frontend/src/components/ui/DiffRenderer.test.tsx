// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
vi.mock('../../hooks/useTheme', () => ({ useTheme: () => ({ theme: 'light' }) }))
import { DiffRenderer } from './DiffRenderer'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

afterEach(() => { document.body.innerHTML = ''; try { localStorage.clear() } catch { /* unavailable */ } })

const diff = '@@ -1,4 +1,4 @@\n same\n-old\n+new\n-  indented\n+\tindented\n'

it('aligns old and new lines side by side, hides whitespace-only changes, and keeps line comments', async () => {
  const host = document.createElement('div'); document.body.append(host)
  const onSelectLine = vi.fn()
  await act(async () => createRoot(host).render(<DiffRenderer content={diff} onSelectLine={onSelectLine} />))
  const press = (label: string) => act(async () => Array.from(host.querySelectorAll('button')).find(item => item.textContent === label)!.click())
  const rows = () => Array.from(host.querySelectorAll('[data-split-row]')).map(row => Array.from(row.children).map(side => side.textContent))

  await press('Side by side')
  expect(rows()).toEqual([['1same', '1same'], ['2old', '2new'], ['3  indented', '3\tindented']])
  expect(localStorage.getItem('diff-view:split')).toBe('1')

  await press('Hide whitespace')
  expect(rows()).toEqual([['1same', '1same'], ['2old', '2new'], ['3\tindented', '3\tindented']])
  expect(host.textContent).toContain('+1')
  expect(host.textContent).toContain('-1')

  await act(async () => (host.querySelector('[aria-label="Comment on before line 2"]') as HTMLButtonElement).click())
  expect(onSelectLine).toHaveBeenCalledWith({ side: 'before', line: 2 })
  expect(host.querySelectorAll('[aria-label="Comment on after line 1"]')).toHaveLength(1)
})

it('hides file headers and shows the unchanged lines between sections as a count', async () => {
  const host = document.createElement('div'); document.body.append(host)
  const twoSections = '--- a/app.ts\n+++ b/app.ts\n@@ -1,1 +1,1 @@\n-a\n+b\n@@ -10,1 +10,1 @@\n-c\n+d\n'
  await act(async () => createRoot(host).render(<DiffRenderer content={twoSections} />))
  expect(host.textContent).not.toContain('+++')
  expect(host.textContent).not.toContain('@@')
  expect(host.textContent).toContain('8 unmodified lines')
})
