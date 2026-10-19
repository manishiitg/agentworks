// @vitest-environment happy-dom
import React, { act, createRef } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import CommandSelectionDialog from './CommandSelectionDialog'
import FileSelectionDialog from './FileSelectionDialog'
import { setProductCommands, setUserCommands } from '../commands/registry'
import { toAgentworksCommandDefinitions } from '../commands/agentworksProductCommands'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'

vi.mock('../stores/useWorkspaceStore', async () => {
  const { create } = await import('zustand')
  return { useWorkspaceStore: create(() => ({ files: [] })) }
})

vi.mock('../commands/user-commands', () => ({ loadAndRegisterUserCommands: vi.fn().mockResolvedValue(undefined) }))

let root: Root
let host: HTMLDivElement
let input: HTMLTextAreaElement
const inputRef = createRef<HTMLTextAreaElement>()
const onClose = vi.fn(), onSelect = vi.fn(), onActive = vi.fn(), onNavigate = vi.fn()
const key = async (key: string, init: KeyboardEventInit = {}, target: EventTarget = input) => {
  const event = new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...init })
  await act(async () => { target.dispatchEvent(event) })
  return event
}
const commands = (searchQuery = 'review-plan', isOpen = true) => root.render(<CommandSelectionDialog isOpen={isOpen} searchQuery={searchQuery}
  inputRef={inputRef} listId="commands" onActiveOptionChange={onActive} onClose={onClose} onSelectCommand={onSelect}
  position={{ bottom: 10, left: 10 }} modeCategory="workflow" workshopMode="workshop" />)
const files = (searchQuery = '', isOpen = true) => root.render(<FileSelectionDialog isOpen={isOpen} searchQuery={searchQuery}
  inputRef={inputRef} listId="files" onActiveOptionChange={onActive} onClose={onClose} onSelectFile={onSelect}
  onNavigateIntoFolder={onNavigate} position={{ top: 10, left: 10 }} />)

beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  vi.clearAllMocks()
  host = document.createElement('div'); document.body.append(host)
  input = document.createElement('textarea'); document.body.append(input)
  inputRef.current = input; input.focus()
  root = createRoot(host)
  useWorkspaceStore.setState({ files: [{ filepath: 'Workflow', type: 'folder', children: [{ filepath: 'Workflow/report.md', type: 'file' }] }, { filepath: 'plan.json', type: 'file' }] })
  setProductCommands(toAgentworksCommandDefinitions([
    { name: 'design-plan', description: 'Review', icon: 'check-circle', aliases: ['review-plan'], menuHidden: false, prompt: 'Review {{context}}.' },
  ]))
})
afterEach(async () => {
  await act(async () => { root.unmount(); setUserCommands([]); setProductCommands([]) })
  host.remove(); input.remove()
})

describe.each([['commands', commands], ['files', files]] as const)('%s picker', (_name, render) => {
  it('selects only on plain Enter from its own input', async () => {
    await act(async () => render())
    for (const init of [{ shiftKey: true }, { ctrlKey: true }, { metaKey: true }, { altKey: true }, { isComposing: true }, { keyCode: 229 }]) {
      expect((await key('Enter', init)).defaultPrevented).toBe(false)
    }
    await key('Enter', {}, document)
    expect(onSelect).not.toHaveBeenCalled()
    expect((await key('Enter')).defaultPrevented).toBe(true)
    expect(onSelect).toHaveBeenCalledTimes(1)
  })
  it('preserves left/right editing and lets Tab move focus', async () => {
    await act(async () => render())
    expect((await key('ArrowLeft')).defaultPrevented).toBe(false)
    expect((await key('ArrowRight')).defaultPrevented).toBe(false)
    expect((await key('ArrowDown', { shiftKey: true })).defaultPrevented).toBe(false)
    expect((await key('Tab')).defaultPrevented).toBe(false)
    expect(onClose).toHaveBeenCalledOnce()
  })
  it('dismisses on Escape and outside interaction without dismissing a click in its input', async () => {
    await act(async () => render())
    await act(async () => { input.dispatchEvent(new MouseEvent('mousedown', { bubbles: true })) })
    expect(onClose).not.toHaveBeenCalled()
    await key('Escape')
    expect(onClose).toHaveBeenCalledOnce()
    await act(async () => { document.body.dispatchEvent(new MouseEvent('mousedown', { bubbles: true })) })
    expect(onClose).toHaveBeenCalledTimes(2)
    const other = document.createElement('input'); document.body.append(other)
    await act(async () => { other.focus() })
    expect(onClose).toHaveBeenCalledTimes(3)
    other.remove()
  })
  it('resets selection for an empty query and exposes the active option', async () => {
    await act(async () => render(''))
    await key('ArrowDown')
    expect(host.querySelector('[aria-selected="true"]')?.id).toMatch(/-1$/)
    await act(async () => render('zzzz-no-match'))
    expect(onActive).toHaveBeenLastCalledWith(undefined)
    await key('ArrowDown'); await key('Enter')
    expect(onSelect).not.toHaveBeenCalled()
    await act(async () => render(''))
    expect(host.querySelector('[aria-selected="true"]')?.id).toMatch(/-0$/)
    expect(onActive).toHaveBeenLastCalledWith(host.querySelector('[aria-selected="true"]')?.id)
  })
})

it('shows user commands arriving asynchronously while the menu remains open', async () => {
  await act(async () => commands('late-command'))
  expect(host.querySelector('[role="option"]')).toBeNull()
  await act(async () => setUserCommands([{ command: 'late-command', description: 'Loaded later', modes: ['workflow'], icon: null, source: 'user', execute: vi.fn() }]))
  expect(host.querySelector('[role="option"]')?.textContent).toContain('/late-command')
  await key('Enter')
  expect(onSelect).toHaveBeenCalledWith('late-command')
})
it('uses Alt+Right to expand or enter a folder, and Alt+Left to go up', async () => {
  await act(async () => files())
  await key('ArrowRight', { altKey: true })
  expect(host.textContent).toContain('report.md')
  await act(async () => files('Work'))
  await key('ArrowRight', { altKey: true })
  expect(onNavigate).toHaveBeenCalledWith('Workflow/')
  await act(async () => files('Workflow/'))
  await key('ArrowLeft', { altKey: true })
  expect(onNavigate).toHaveBeenCalledWith('')
})
it('lists only the supplied project files (Crew chats) and selects their crew-relative path', async () => {
  const crewFiles = [{ filepath: 'code', type: 'folder' as const, children: [{ filepath: 'code/app.py', type: 'file' as const }] }, { filepath: 'MEMORY.md', type: 'file' as const }]
  await act(async () => root.render(<FileSelectionDialog isOpen searchQuery="app" inputRef={inputRef} listId="files"
    onActiveOptionChange={onActive} onClose={onClose} onSelectFile={onSelect} position={{ top: 10, left: 10 }} files={crewFiles} />))
  expect(host.textContent).toContain('app.py')
  expect(host.textContent).not.toContain('report.md')
  await key('Enter')
  expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ filepath: 'code/app.py' }))
})
it('keeps the workspace tree when no project files are supplied (workflow chats)', async () => {
  await act(async () => files('report'))
  expect(host.textContent).toContain('report.md')
})
