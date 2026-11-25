// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { ChatModeMenu, openCrewMacFoldersEvent } from './ChatModeMenu'
import { changeChatContext } from '../../products/work/ChatContextItems'
import type { ChatContextView } from '../../services/api-types'

let view: ChatContextView | null = null
vi.mock('../../products/work/useChatContextView', () => ({ useChatContextView: () => view }))
vi.mock('../../products/work/ChatContextItems', () => ({ changeChatContext: vi.fn(async () => null), menuLabelClass: '', menuRowClass: '' }))
vi.mock('../../services/api', () => ({
  default: { get: vi.fn(async () => ({ data: { available: true, home: '/Users/me' } })) },
  workFolderApi: { listWorkFolders: vi.fn(async () => ({ folders: [{ id: '1', alias: 'Downloads', path: '/Users/me/Downloads', access: 'read_write' }] })) },
}))
vi.mock('../../stores/useWorkflowStore', () => ({ useWorkflowStore: { getState: () => ({ openWorkspaceView: vi.fn() }) } }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const codexView = (mode: 'project' | 'auto_review' | 'full_access', effective = mode): ChatContextView => ({
  session_id: 's1', can_compact: false, can_new_chat: true,
  access: { mode, effective, options: [
    { mode: 'project', available: true },
    { mode: 'auto_review', available: false, why: 'This coding app has no reviewer that works here yet.' },
    { mode: 'full_access', available: true },
  ] },
})

let root: ReturnType<typeof createRoot> | undefined
afterEach(async () => { await act(async () => root?.unmount()); document.body.innerHTML = ''; vi.mocked(changeChatContext).mockClear() })

async function openMenu(element: React.ReactElement) {
  const host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
  await act(async () => root!.render(element))
  const trigger = host.querySelector('[data-testid="plan-mode-chip"]') as HTMLElement
  await act(async () => { trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })) })
  return trigger
}

// #3885: the mode menu offers each mode with what it reaches; a mode this app cannot run says why and is off.
it('saves a picked mode and keeps Auto-review off for an app without a reviewer', async () => {
  view = codexView('project')
  const trigger = await openMenu(<ChatModeMenu sessionId="s1" onPlanToggle={() => {}} />)
  expect(trigger.textContent).toContain('Auto')
  const auto = document.querySelector('[data-testid="chat-mode-auto_review"]') as HTMLElement
  expect(auto.getAttribute('data-disabled')).not.toBeNull()
  expect(auto.textContent).toContain('no reviewer')
  expect(document.querySelector('[data-testid="chat-mode-plan"]')).toBeNull()
  await act(async () => { await Promise.resolve() })
  expect(document.querySelector('[data-testid="chat-mode-reach"]')?.textContent).toContain('Can reach: this project · Downloads')
  await act(async () => { (document.querySelector('[data-testid="chat-mode-full_access"]') as HTMLElement).click() })
  expect(changeChatContext).toHaveBeenCalledWith('s1', { access_mode: 'full_access' })
})

it('says when a saved mode runs as This project, and picking Plan only turns Plan on', async () => {
  view = codexView('auto_review', 'project')
  const toggle = vi.fn()
  await openMenu(<ChatModeMenu sessionId="s1" plan={{ on: false }} onPlanToggle={toggle} />)
  expect(document.querySelector('[data-testid="chat-mode-fallback"]')).not.toBeNull()
  await act(async () => { (document.querySelector('[data-testid="chat-mode-plan"]') as HTMLElement).click() })
  expect(toggle).toHaveBeenCalledTimes(1)
  expect(changeChatContext).not.toHaveBeenCalled()
})

// #3946: in a Crew project, Manage folders asks that project's side panel to open Folders on this Mac.
it('Manage folders opens the Crew project side panel', async () => {
  view = codexView('project')
  const seen: unknown[] = []
  const listen = (event: Event) => seen.push((event as CustomEvent).detail)
  window.addEventListener(openCrewMacFoldersEvent, listen)
  try {
    await openMenu(<ChatModeMenu sessionId="s1" projectId="p1" onPlanToggle={() => {}} />)
    await act(async () => { (document.querySelector('[data-testid="chat-mode-manage-folders"]') as HTMLElement).click() })
    expect(seen).toEqual([{ projectId: 'p1' }])
  } finally {
    window.removeEventListener(openCrewMacFoldersEvent, listen)
  }
})
