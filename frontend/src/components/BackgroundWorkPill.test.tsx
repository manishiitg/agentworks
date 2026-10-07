// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { BackgroundWorkPill } from './BackgroundWorkPill'
import { SessionStopButton } from './SessionStopButton'
import { agentApi } from '../services/api'
import { useChatStore } from '../stores/useChatStore'
import type { ChatTab } from '../stores/useChatStore'

const original = useChatStore.getState()
afterEach(() => { useChatStore.setState(original, true); vi.restoreAllMocks(); vi.unstubAllGlobals() })

// PLAT-705: with only background work running (no foreground turn) the
// composer shows "N running" and no Stop; an item's Stop ends only that item.
it('shows the background pill without the composer Stop, and stops one item by id', async () => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  const list = vi.spyOn(agentApi, 'getSessionBackgroundWork').mockResolvedValue({
    session_id: 'session-a',
    items: [
      { id: 'workflow-step-0-a', kind: 'step', label: 'Weekly knowledge refresh', started_at: new Date().toISOString(), can_stop: true, status: 'Running execute_shell_command (4s)' },
      { id: 'revi-0001', kind: 'sub_agent', label: 'code review', started_at: new Date().toISOString(), can_stop: true },
    ],
  })
  const stopItem = vi.spyOn(agentApi, 'stopSessionBackgroundWork').mockResolvedValue()
  const cancelTurn = vi.spyOn(agentApi, 'cancelCurrentTurn').mockResolvedValue()
  useChatStore.setState({
    getActiveSessions: vi.fn().mockResolvedValue([]),
    activeSessionsCache: [],
    tabEvents: {},
    chatTabs: { a: { tabId: 'a', sessionId: 'session-a', isStreaming: false, hasRunningBgAgents: true, isCompleted: false } as ChatTab },
  })
  const host = document.createElement('div'); document.body.appendChild(host)
  const root = createRoot(host)
  await act(async () => { root.render(<><BackgroundWorkPill tabId="a" /><SessionStopButton tabId="a" /></>) })

  expect(host.querySelector('[data-testid="chat-stop-button"]')).toBeNull()
  const pill = host.querySelector<HTMLButtonElement>('[data-testid="background-work-pill"]')
  expect(pill?.textContent).toContain('2 running')
  expect(list).toHaveBeenCalledWith('session-a')

  await act(async () => { pill!.click() })
  const rows = host.querySelectorAll('[data-testid="background-work-item"]')
  expect(rows[0].textContent).toContain('Step: Weekly knowledge refresh')
  expect(rows[0].textContent).toContain('Running execute_shell_command')
  expect(rows[1].textContent).toContain('Sub-agent: code review')

  await act(async () => { rows[1].querySelector<HTMLButtonElement>('[data-testid="background-work-stop"]')!.click() })
  expect(stopItem).toHaveBeenCalledWith('session-a', 'revi-0001')
  expect(cancelTurn).not.toHaveBeenCalled()
  act(() => root.unmount()); host.remove()
})
