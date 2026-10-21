// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { it, expect, vi } from 'vitest'
vi.mock('../../services/api', () => ({ getApiBaseUrl: () => 'https://example.agentworks.test' }))
vi.mock('./HowBullpenWorks', () => ({ HowBullpenWorks: () => <p>Network help</p> }))
import { AgentMessagingSetupPanel } from './AgentMessagingSetupPanel'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
it('starts with familiar apps, keeps coding setup optional and preserves trust instructions', async () => {
 const host=document.createElement('div'); document.body.append(host); const root=createRoot(host); const open=vi.fn()
 try {
  await act(async()=>root.render(<AgentMessagingSetupPanel onOpenMcp={open}/>))
  expect(host.textContent).toContain('ChatGPT.com');expect(host.querySelector('code')).toBeNull()
  await act(async()=>host.querySelector<HTMLButtonElement>('[aria-label="Codex"]')!.click())
  const setup=[...host.querySelectorAll('details')].find(d=>d.querySelector('summary')?.textContent==='1. Set up this app once')!
  expect(setup.open).toBe(false);expect(setup.textContent).toContain('Settings → Hooks')
  expect(host.textContent).toContain('2Connect this chat');expect(host.textContent).toContain('3Try a message')
  expect(host.querySelector('[aria-label="Install in the Codex app"]')).not.toBeNull()
  await act(async()=>[...host.querySelectorAll('button')].find(button=>button.textContent==='Change app')!.click())
  await act(async()=>host.querySelector<HTMLButtonElement>('[aria-label="ChatGPT"]')!.click())
  expect(host.querySelector('[aria-label="Coding agent setup"]')).toBeNull()
  expect(host.querySelector('[aria-label="Register an app inbox"]')?.textContent).toContain('register an inbox')
  expect(host.textContent).toContain('when the app checks them')
  expect(open).not.toHaveBeenCalled()
 } finally {await act(async()=>root.unmount());host.remove()}
})
