// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import CommandSelectionDialog from './CommandSelectionDialog'
import type { CommandDefinition } from '../commands/types'
vi.mock('../commands', () => ({loadAndRegisterUserCommands: async () => {},findCommand: () => undefined,findProductOrUserCommand: () => undefined,getCommands: () => [],getProductAndUserCommands: () => []}))
vi.mock('../commands/registry', () => ({subscribeCommands: () => () => {},getCommandRevision: () => 0}))
vi.mock('../hooks/useComposerPicker', () => ({useComposerPicker: () => {},isComposerPickerEvent: () => true}))

// One consolidated Skills entry must not displace the existing commands.
it('keeps app commands first and searchable skill choices separate', async () => {
 const host = document.createElement('div'); document.body.append(host)
 const root = createRoot(host)
 const cmd = (command:string,label?:string):CommandDefinition => ({command,label,description:command,icon:null,source:'builtin',execute:()=>{}})
 const select=vi.fn()
 const props={isOpen:true,onClose:()=>{},onSelectCommand:select,position:{bottom:0,left:0},commands:[cmd('fork'),cmd('compact')],skillCommands:[cmd('__skills__','Skills…')]}
 await act(async () => root.render(<CommandSelectionDialog {...props} searchQuery="" />))
 const options = [...document.querySelectorAll('[role="option"]')]
 expect(options.map(row => row.textContent)).toEqual([expect.stringContaining('/fork'),expect.stringContaining('/compact'),expect.stringContaining('Skills…')])
 await act(async () => root.render(<CommandSelectionDialog {...props} searchQuery="review" skillCommands={[cmd('__native_skill:opaque','review'),cmd('__skills__','Skills…')]} />))
 const review=document.querySelector<HTMLElement>('[role="option"]')!
 expect(review.textContent).toContain('review')
 await act(async () => review.click())
 expect(select).toHaveBeenCalledWith('__native_skill:opaque')
 act(() => root.unmount()); host.remove()
})
