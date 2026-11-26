// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import NativeSkillPicker, { useNativeSkillCatalog } from './NativeSkillPicker'
const fixture = vi.hoisted(() => ({ invocation: vi.fn() }))
vi.mock('../../api/skills', () => ({ skillsApi: fixture }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: (value: unknown) => unknown) => select({ user: { id: 'owner' } }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: (select: (value: unknown) => unknown) => select({ activeWorkspaceId: 'server' }) }))
const host = document.createElement('div'); document.body.append(host)
const root = createRoot(host)
afterEach(() => { act(() => root.render(null)); fixture.invocation.mockReset() })

// PLAT-746-1008: a pending response from the old CLI/chat cannot replace the
// new CLI's verdict. Selecting a skill only returns its opaque ID, not a send.
it('chooses an explicit skill and discards a late catalog from another CLI', async () => {
  const skill = { id:'current-review',name:'review',description:'Review actual changes',source:'Prepared for this chat',provider:'codex-cli' }
  let finishOld: ((value: unknown) => void) | undefined
  fixture.invocation.mockImplementation((_workspace, _session, provider) => provider === 'claude-code' ? new Promise(resolve => { finishOld = resolve }) : Promise.resolve({skills:[skill],provider,prepared:true,supported:true,message:'Choose a skill'}))
  const choose = vi.fn()
  const Harness = ({provider}: {provider:string}) => {
    const state = useNativeSkillCatalog('Crew/example','current',provider,true)
    return <NativeSkillPicker {...state} refresh={state.refresh} choose={choose} close={() => {}} />
  }
  await act(async () => root.render(<Harness provider="claude-code" />))
  await act(async () => root.render(<Harness provider="codex-cli" />))
  await act(async () => finishOld?.({skills:[{...skill,id:'wrong-old-skill',name:'old-private'}],provider:'claude-code',prepared:true,supported:true,message:'Old'}))
  expect(document.body.textContent).toContain('Review actual changes')
  expect(document.body.textContent).not.toContain('old-private')
  await act(async () => [...document.querySelectorAll('button')].find(button => button.textContent?.includes('Review actual changes'))!.click())
  expect(choose).toHaveBeenCalledWith(skill)
  expect(fixture.invocation).toHaveBeenLastCalledWith('Crew/example','current','codex-cli')
})
