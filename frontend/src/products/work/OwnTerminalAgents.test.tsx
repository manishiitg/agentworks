// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { OwnTerminalAgents, type OwnTerminalAgent } from './OwnTerminalAgents'

const { open, rows } = vi.hoisted(() => ({
  open: vi.fn(),
  rows: ['Working', 'Waiting for you', 'Idle', 'Offline'].map((activity, i) => ({
    id: String(i), name: `Native ${i}`, activity, in_team: true,
    provider: 'codex-cli', provider_name: 'Codex', address: `agent:native-${i}`,
    online: true, presence: 'online', // Must not override the server activity.
  })) as OwnTerminalAgent[],
}))
vi.mock('./teamApi', () => ({ crewTerminalTarget: async () => ({ project: 'crew' }), teamRequest: async () => ({ agents: rows }) }))
vi.mock('./terminalConversationSelection', () => ({ openTerminalConversation: open, useTerminalConversationSelection:(select:(state:unknown)=>unknown)=>select({selection:{identity:1,workspace:'local',projectId:'crew',agent:{id:'2'}}}) }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: (s: unknown) => unknown) => select({ user: { id: 'owner' } }) }))
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: vi.fn() }))
vi.mock('../../utils/desktopConnection', () => ({ getRawActiveWorkspaceId: () => 'local' }))
vi.mock('../../utils/chatIdentity', () => ({ captureChatIdentity: () => ({}), isChatIdentityCurrent: () => true }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// PLAT-746-1049: terminal rows used to show only online, even during a real turn.
it('shows server activity with the shared working and waiting marks, retaining the main-chat opener', async () => {
  const host = document.createElement('div')
  const root = createRoot(host)
  try {
    await act(async () => root.render(<OwnTerminalAgents projectId="crew" connection="local" />))
    const buttons = [...host.querySelectorAll('button')]
    expect(buttons).toHaveLength(4)
    expect(buttons[2].getAttribute('aria-current')).toBe('page')
    expect(buttons[0].getAttribute('aria-current')).toBeNull()
    for (const [i, row] of buttons.entries()) {
      expect(row.textContent).toContain(rows[i].activity)
      expect(row.querySelector('[aria-label="In team"]')).toBeTruthy()
    }
    expect(buttons[0].querySelector('[data-theme-agent-state="running"]')).toBeTruthy()
    expect(buttons[1].querySelector('[data-theme-agent-state="waiting"]')).toBeTruthy()
    expect(buttons[2].querySelector('[data-theme-agent-state]')).toBeNull()
    expect(buttons[3].querySelector('[data-theme-agent-state]')).toBeNull()
    await act(async () => buttons[0].click())
    expect(open).toHaveBeenCalledWith('crew', { project: 'crew' }, rows[0], true)
  } finally { await act(async () => root.unmount()) }
})
