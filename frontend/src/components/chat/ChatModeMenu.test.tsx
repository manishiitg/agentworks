// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { ChatModeMenu, openCrewMacFoldersEvent } from './ChatModeMenu'
import { changeChatContext } from '../../products/work/ChatContextItems'
import { workFolderApi } from '../../services/api'
import type { ChatContextView } from '../../services/api-types'

let view: ChatContextView | null = null
vi.mock('../../products/work/useChatContextView', () => ({ useChatContextView: () => view }))
vi.mock('../../products/work/ChatContextItems', () => ({ changeChatContext: vi.fn(async () => null), menuLabelClass: '', menuRowClass: '' }))
vi.mock('../../services/api', () => ({
  default: { get: vi.fn(async () => ({ data: { available: true, home: '/Users/me' } })) },
  workFolderApi: { listWorkFolders: vi.fn(async () => ({ folders: [{ id: '1', alias: 'Downloads', path: '/Users/me/Downloads', access: 'read_write' }] })), addWorkFolder: vi.fn(async () => ({ folder: {} })) },
}))
vi.mock('../../stores/useWorkflowStore', () => ({ useWorkflowStore: { getState: () => ({ openWorkspaceView: vi.fn() }) } }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const codexView = (mode: 'project' | 'auto_review', effective = mode): ChatContextView => ({
  session_id: 's1', can_compact: false, can_new_chat: true,
  access: { mode, effective, options: [
    { mode: 'project', available: true },
    { mode: 'auto_review', available: false, why: 'This coding app has no reviewer that works here yet.' },
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

// #3885, split 26 Nov: the menu offers Auto / Auto-review / Plan; what the chat can reach is one line with Change.
it('offers the chat modes, keeps Auto-review off for an app without a reviewer and shows the project access', async () => {
  view = codexView('project')
  const trigger = await openMenu(<ChatModeMenu sessionId="s1" onPlanToggle={() => {}} />)
  expect(trigger.textContent).toContain('Auto')
  expect(document.querySelector('[data-testid="chat-mode-full_access"]')).toBeNull()
  const auto = document.querySelector('[data-testid="chat-mode-auto_review"]') as HTMLElement
  expect(auto.getAttribute('data-disabled')).not.toBeNull()
  expect(auto.textContent).toContain('no reviewer')
  expect(document.querySelector('[data-testid="chat-mode-plan"]')).toBeNull()
  await act(async () => { await Promise.resolve() })
  expect(document.querySelector('[data-testid="chat-mode-reach"]')?.textContent).toContain('Can reach: this project · Downloads')
  expect(document.querySelector('[data-testid="chat-mode-manage-folders"]')?.textContent).toContain('Change')
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

it('saves a picked mode', async () => {
  view = { ...codexView('auto_review'), access: { mode: 'auto_review', effective: 'auto_review', options: [{ mode: 'project', available: true }, { mode: 'auto_review', available: true }] } }
  await openMenu(<ChatModeMenu sessionId="s1" onPlanToggle={() => {}} />)
  await act(async () => { (document.querySelector('[data-testid="chat-mode-project"]') as HTMLElement).click() })
  expect(changeChatContext).toHaveBeenCalledWith('s1', { access_mode: 'project' })
})

// Owner, 26 Nov: one click from the menu gives the whole Mac, the same grant as Folders on this Mac.
it('gives the whole Mac from the menu', async () => {
  view = codexView('project')
  await openMenu(<ChatModeMenu sessionId="s1" onPlanToggle={() => {}} />)
  await act(async () => { await Promise.resolve() })
  await act(async () => { (document.querySelector('[data-testid="chat-mode-whole-mac"]') as HTMLElement).click() })
  expect(workFolderApi.addWorkFolder).toHaveBeenCalledWith({ path: '/Users/me', alias: 'mac', access: 'read_write' })
})

// Owner, 26 Nov: reply style and the summary card, per chat, for every coding app.
it('turns the summary and next-steps card off from the menu', async () => {
  view = { ...codexView('project'), replies: { style: 'default', styles: ['default', 'concise', 'explanatory', 'learning', 'proactive'], next_steps: true } }
  await openMenu(<ChatModeMenu sessionId="s1" onPlanToggle={() => {}} />)
  expect(document.querySelector('[data-testid="chat-reply-style"]')?.textContent).toContain('Default')
  const card = document.querySelector('[data-testid="chat-next-steps"]') as HTMLElement
  expect(card.getAttribute('aria-checked')).toBe('true')
  await act(async () => { card.click() })
  expect(changeChatContext).toHaveBeenCalledWith('s1', { next_steps: false })
})

// Owner, 26 Nov: what the chat does when the person asks for unrelated work.
it('shows what the chat does with an unrelated task', async () => {
  view = { ...codexView('project'), replies: { style: 'default', styles: ['default', 'concise', 'explanatory', 'learning', 'proactive'], next_steps: true, unrelated_task: 'new_tab', unrelated_tasks: ['ask', 'new_tab', 'here'] } }
  await openMenu(<ChatModeMenu sessionId="s1" onPlanToggle={() => {}} />)
  expect(document.querySelector('[data-testid="chat-unrelated-task"]')?.textContent).toContain('New tab')
})

it('hides the unrelated-task row on a server that does not offer it', async () => {
  view = { ...codexView('project'), replies: { style: 'default', styles: ['default'], next_steps: true } }
  await openMenu(<ChatModeMenu sessionId="s1" onPlanToggle={() => {}} />)
  expect(document.querySelector('[data-testid="chat-unrelated-task"]')).toBeNull()
})

// #4604: Goal shows only for an app with its own goal command; picking it turns Plan off and arms the goal.
it('offers Goal only when the app has a goal command, and picking it leaves Plan', async () => {
  view = codexView('project')
  const planToggle = vi.fn()
  const goalToggle = vi.fn()
  await openMenu(<ChatModeMenu sessionId="s1" plan={{ on: true }} onPlanToggle={planToggle} goal={{ on: false }} onGoalToggle={goalToggle} />)
  const goal = document.querySelector('[data-testid="chat-mode-goal"]') as HTMLElement
  expect(goal).not.toBeNull()
  await act(async () => { goal.click() })
  expect(planToggle).toHaveBeenCalledTimes(1)
  expect(goalToggle).toHaveBeenCalledTimes(1)
  expect(changeChatContext).not.toHaveBeenCalled()
})

it('has no Goal row when the app has no goal command', async () => {
  view = codexView('project')
  await openMenu(<ChatModeMenu sessionId="s1" onPlanToggle={() => {}} />)
  expect(document.querySelector('[data-testid="chat-mode-goal"]')).toBeNull()
})

// #4623 (approved mockup): while a goal works, the chip says so and the menu shows it with the bar's actions.
it('shows a running goal with Pause and Clear goal, sending the same /goal commands as the goal bar', async () => {
  view = codexView('project')
  const command = vi.fn()
  const trigger = await openMenu(<ChatModeMenu sessionId="s1" onPlanToggle={() => {}} goal={{ on: false, running: { objective: 'Every workspace test passes' } }} onGoalToggle={() => {}} onGoalCommand={command} />)
  expect(trigger.textContent).toContain('Goal running')
  expect(document.querySelector('[data-testid="chat-mode-goal"]')).toBeNull()
  expect(document.querySelector('[data-testid="chat-mode-goal-running"]')?.textContent).toContain('Running: Every workspace test passes')
  await act(async () => { (document.querySelector('[data-testid="chat-mode-goal-pause"]') as HTMLElement).click() })
  expect(command).toHaveBeenCalledWith('/goal pause')
})
