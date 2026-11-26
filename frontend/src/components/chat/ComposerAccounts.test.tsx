// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { ComposerAccountButton } from './ComposerActionsMenu'
import { openWorkspaceSettings } from '../../utils/workspaceSettingsNavigation'
vi.mock('../../utils/workspaceSettingsNavigation',()=>({openWorkspaceSettings:vi.fn()}))
vi.mock('./ChatSendModeButton',()=>({ChatSendOptions:()=>null,ComposerSendKeys:()=>null}))
Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
// #2826: sign-in remains reachable from chat without creating a chat terminal.
it('opens the existing account sign-in page from chat',async()=>{
 const host=document.createElement('div'),root=createRoot(host)
 await act(async()=>root.render(<ComposerAccountButton/>))
 await act(async()=>host.querySelector('button')!.click())
 expect(openWorkspaceSettings).toHaveBeenCalledWith('providers')
 await act(async()=>root.unmount())
})
