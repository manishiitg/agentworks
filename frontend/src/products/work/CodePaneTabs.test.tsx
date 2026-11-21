// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { CodePaneTabs, codePaneTab, fileSearchShortcut } from './CodePaneTabs'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => { document.body.innerHTML = '' })

it('maps existing pane views to one tab', () => {
  expect(codePaneTab('shell', { kind: 'browse' })).toBe('terminal')
  expect(codePaneTab('files', { kind: 'browse' })).toBe('files')
  expect(codePaneTab('files', { kind: 'folder', repoRoot: 'app' })).toBe('files')
  expect(codePaneTab('files', { kind: 'changes' })).toBe('changes')
  expect(codePaneTab('files', { kind: 'pr', repository: 'o/r', number: 1 })).toBe('changes')
  expect(codePaneTab('dashboard', { kind: 'browse' })).toBeNull()
})

it('selects another tab and hides Terminal without a shell', () => {
  const onSelect = vi.fn()
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  act(() => root.render(<CodePaneTabs active="changes" showTerminal onSelect={onSelect} />))
  const tabs = () => [...host.querySelectorAll('[role="tab"]')] as HTMLButtonElement[]
  expect(tabs().map(tab => tab.textContent)).toEqual(['Changes', 'Files', 'Terminal'])
  expect(tabs()[0].getAttribute('aria-selected')).toBe('true')
  act(() => tabs()[2].click())
  expect(onSelect).toHaveBeenCalledWith('terminal')
  act(() => tabs()[0].click())
  expect(onSelect).toHaveBeenCalledTimes(1)
  act(() => root.render(<CodePaneTabs active="files" showTerminal={false} onSelect={onSelect} />))
  expect(tabs().map(tab => tab.textContent)).toEqual(['Changes', 'Files'])
  act(() => root.unmount())
})

it('Ctrl+` is the terminal shortcut, nothing else', async () => {
  const { isTerminalShortcut } = await import('./CodePaneTabs')
  const key = { ctrlKey: true, metaKey: false, altKey: false, shiftKey: false, code: 'Backquote' }
  expect(isTerminalShortcut(key)).toBe(true)
  expect(isTerminalShortcut({ ...key, metaKey: true })).toBe(false)
  expect(isTerminalShortcut({ ...key, ctrlKey: false })).toBe(false)
  expect(isTerminalShortcut({ ...key, code: 'Digit1' })).toBe(false)
})

it('maps the file search shortcuts on Mac and other computers', () => {
  const key = (code: string, extra: Partial<KeyboardEvent> = {}) => ({ ctrlKey: false, metaKey: false, altKey: false, shiftKey: false, code, ...extra })
  expect(fileSearchShortcut(key('KeyP', { metaKey: true }))).toBe('name')
  expect(fileSearchShortcut(key('KeyP', { ctrlKey: true }))).toBe('name')
  expect(fileSearchShortcut(key('KeyF', { metaKey: true, shiftKey: true }))).toBe('text')
  expect(fileSearchShortcut(key('KeyF', { metaKey: true }))).toBeNull()
  expect(fileSearchShortcut(key('KeyP'))).toBeNull()
})
