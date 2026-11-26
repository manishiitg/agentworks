// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { ProjectAgentRow } from './SidebarCrewAgentMark'
import { workConversationAgent, workConversationKey } from '../../utils/workConversationKey'

vi.mock('../../stores/useChatStore', () => ({ useChatStore: (select: (state: unknown) => unknown) => select({ chatTabs: {} }) }))
vi.mock('../chat/agentMenuState', () => ({ useAgentMenuState: (_tab: string, activity?: string) => activity || null }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('shows teammate activity before opening a chat and clears it when work ends', async () => {
  const host = document.createElement('div')
  const root = createRoot(host)
  try {
    for (const local of [true, false]) {
      await act(async () => root.render(<ProjectAgentRow name="Helper" onOpen={() => {}} projectId="crew" sessionId="unopened" local={local} activity="running" />))
      expect(host.querySelector('[aria-label="Working"][data-theme-agent-state="running"]')).not.toBeNull()
      await act(async () => root.render(<ProjectAgentRow name="Helper" onOpen={() => {}} projectId="crew" sessionId="unopened" local={local} activity="waiting" />))
      expect(host.querySelector('[aria-label="Needs you"]')).not.toBeNull()
      await act(async () => root.render(<ProjectAgentRow name="Helper" onOpen={() => {}} projectId="crew" sessionId="unopened" local={local} activity="" status="Mac off" />))
      expect(host.querySelector('[aria-label="Working"]')).toBeNull()
      expect(host.textContent).toContain('Mac off')
      // #3378: a quiet agent in another project still shows when it was last active.
      const session = { session_id: 'unopened', last_activity: new Date(Date.now() - 2 * 60_000).toISOString() } as never
      await act(async () => root.render(<ProjectAgentRow name="Helper" onOpen={() => {}} projectId="crew" sessionId="unopened" session={session} local={local} activity="" />))
      expect(host.textContent).toContain('2m')
      // #3406: after an app restart no session is live; the saved last message still shows when it was active.
      await act(async () => root.render(<ProjectAgentRow name="Helper" onOpen={() => {}} projectId="crew" sessionId="unopened" lastMessageAt={new Date(Date.now() - 5 * 60_000).toISOString()} local={local} activity="" />))
      expect(host.textContent).toContain('5m')
      // The same row as the open project's: one theme box, one listitem button.
      expect(host.querySelectorAll('[data-theme-row="agent"] > [role="listitem"]')).toHaveLength(1)
    }
  } finally { await act(async () => root.unmount()) }
})

it('keeps the server chat identity when opening a teammate from a different local Crew', () => {
  const key = 'local-crew:team:server-crew:chat:mac-person-agent'
  expect(workConversationKey('local-crew', workConversationAgent(key))).toBe(key)
  expect(workConversationKey('crew', workConversationAgent('crew:chat:ordinary'))).toBe('crew:chat:ordinary')
})

it('shows a recent age always, an older one and the coding program only on hover (owner, 20 Nov)', async () => {
  const host = document.createElement('div')
  const root = createRoot(host)
  const ago = (hours: number) => new Date(Date.now() - hours * 60 * 60_000).toISOString()
  try {
    await act(async () => root.render(<ProjectAgentRow name="Helper" onOpen={() => {}} projectId="crew" sessionId="s" lastMessageAt={ago(2)} local activity="" provider="claude-code" />))
    const recent = [...host.querySelectorAll('span')].find(span => span.textContent === '2h')
    expect(recent?.className).not.toContain('hidden')
    const program = host.querySelector('[aria-label="Claude Code"]')
    expect(program?.className).toContain('hidden')
    expect(program?.className).toContain('group-hover:inline-flex')
    await act(async () => root.render(<ProjectAgentRow name="Helper" onOpen={() => {}} projectId="crew" sessionId="s" lastMessageAt={ago(5)} local activity="" />))
    const older = [...host.querySelectorAll('span')].find(span => span.textContent === '5h')
    expect(older?.className).toContain('hidden group-hover:inline')
  } finally { await act(async () => root.unmount()) }
})
