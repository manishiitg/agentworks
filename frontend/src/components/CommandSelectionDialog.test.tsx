// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import CommandSelectionDialog from './CommandSelectionDialog'
import { ComposerActionsMenu } from './chat/ComposerActionsMenu'
import type { CommandDefinition } from '../commands/types'
vi.mock('../commands', () => ({loadAndRegisterUserCommands: async () => {},findCommand: () => undefined,findProductOrUserCommand: () => undefined,getCommands: () => [],getProductAndUserCommands: () => []}))
vi.mock('../commands/registry', () => ({subscribeCommands: () => () => {},getCommandRevision: () => 0}))
vi.mock('../hooks/useComposerPicker', () => ({useComposerPicker: () => {},isComposerPickerEvent: () => true}))

// The / menu lists commands first and the Skills entry last; skills whose name
// matches the typed text sit between them (#3302: never by description).
it('keeps app commands first, name-matched skills next and the Skills entry last', async () => {
 const host = document.createElement('div'); document.body.append(host)
 const root = createRoot(host)
 const cmd = (command:string,label?:string):CommandDefinition => ({command,label,description:command,icon:null,source:'builtin',execute:()=>{}})
 const select=vi.fn()
 const props={isOpen:true,onClose:()=>{},onSelectCommand:select,position:{bottom:0,left:0},commands:[cmd('fork'),cmd('compact')],skillCommands:[cmd('__skills__','Skills…')]}
 await act(async () => root.render(<CommandSelectionDialog {...props} searchQuery="" />))
 const options = [...document.querySelectorAll('[role="option"]')]
 expect(options.map(row => row.textContent)).toEqual([expect.stringContaining('/fork'),expect.stringContaining('/compact'),expect.stringContaining('Skills…')])
 const skills=options[2] as HTMLElement
 await act(async () => skills.click())
 expect(select).toHaveBeenCalledWith('__skills__')
 await act(async () => root.render(<CommandSelectionDialog {...props} searchQuery="under" skillCommands={[cmd('__native_skill:opaque','plugin:understand'),cmd('__skills__','Skills…')]} />))
 const skill=document.querySelector<HTMLElement>('[role="option"]')!
 expect(skill.textContent).toContain('plugin:understand')
 await act(async () => skill.click())
 expect(select).toHaveBeenCalledWith('__native_skill:opaque')
 act(() => root.unmount()); host.remove()
})

// Hover must never execute a command or open the full picker. A choice runs
// the existing handler only after menu focus has been released.
it('previews admitted commands on hover and uses the existing search and select actions', async () => {
 Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
 const host = document.createElement('div'); document.body.append(host)
 const root = createRoot(host)
 const search = vi.fn()
 const choose = vi.fn()
 const command = (name: string): CommandDefinition => ({ command: name, description: name, icon: null, source: 'builtin', execute: vi.fn() })
 const render = async () => {
  await act(async () => root.render(<ComposerActionsMenu uploading={false} onAttach={vi.fn()}
    commands={{ catalog: { commands: [command('fork'), command('goal')] }, onSelectCommand: choose, onOpenCommands: search }} />))
  await act(async () => host.querySelector('button')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })))
 }
 const item = (label: string) => Array.from(document.querySelectorAll<HTMLElement>('[role="menuitem"]')).find(row => row.textContent?.includes(label))
 const hover = async () => {
  const trigger = item('Slash commands')!
  await act(async () => {
   trigger.dispatchEvent(new PointerEvent('pointermove', { pointerType: 'mouse', bubbles: true }))
   await new Promise(resolve => setTimeout(resolve, 120))
  })
  expect(trigger.getAttribute('aria-expanded')).toBe('true')
 }
 try {
  await render()
  await hover()
  expect(item('/fork')).toBeDefined()
  expect(item('/goal')).toBeUndefined()
  expect(choose).not.toHaveBeenCalled()
  expect(search).not.toHaveBeenCalled()
  await act(async () => item('/fork')!.click())
  expect(choose).toHaveBeenCalledTimes(1)
  expect(choose).toHaveBeenCalledWith('fork')
  expect(search).not.toHaveBeenCalled()

  await render()
  await act(async () => item('Slash commands')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true })))
  await act(async () => item('Search all commands')!.click())
  expect(search).toHaveBeenCalledTimes(1)
  expect(choose).toHaveBeenCalledTimes(1)
 } finally { await act(async () => root.unmount()); host.remove() }
})
