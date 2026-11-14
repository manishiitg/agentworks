// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { ProjectAgentMark } from './SidebarCrewAgentMark'
import { workConversationAgent, workConversationKey } from '../../utils/workConversationKey'

vi.mock('../../stores/useChatStore', () => ({ useChatStore: (select: (state: unknown) => unknown) => select({ chatTabs: {} }) }))
vi.mock('../chat/agentMenuState', () => ({ useAgentMenuState: (_tab: string, activity?: string) => activity || null }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('shows teammate activity before opening a chat and clears it when work ends', async () => {
  const host = document.createElement('div')
  const root = createRoot(host)
  try {
    for (const local of [true, false]) {
      await act(async () => root.render(<ProjectAgentMark projectId="crew" sessionId="unopened" local={local} activity="running" />))
      expect(host.querySelector('[aria-label="Working"]')).not.toBeNull()
      await act(async () => root.render(<ProjectAgentMark projectId="crew" sessionId="unopened" local={local} activity="waiting" />))
      expect(host.querySelector('[aria-label="Needs you"]')).not.toBeNull()
      await act(async () => root.render(<ProjectAgentMark projectId="crew" sessionId="unopened" local={local} activity="" status="Mac off" />))
      expect(host.querySelector('[aria-label="Working"]')).toBeNull()
      expect(host.textContent).toContain('Mac off')
    }
  } finally { await act(async () => root.unmount()) }
})

it('keeps the server chat identity when opening a teammate from a different local Crew', () => {
  const key = 'local-crew:team:server-crew:chat:mac-person-agent'
  expect(workConversationKey('local-crew', workConversationAgent(key))).toBe(key)
  expect(workConversationKey('crew', workConversationAgent('crew:chat:ordinary'))).toBe('crew:chat:ordinary')
})
