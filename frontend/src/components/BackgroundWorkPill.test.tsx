// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { BackgroundWorkPill } from './BackgroundWorkPill'
import { SessionStopButton } from './SessionStopButton'
import { agentApi } from '../services/api'
import { useChatStore } from '../stores/useChatStore'
import type { ChatTab } from '../stores/useChatStore'
import type { SessionBackgroundWorkResponse } from '../services/api-types'

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

it('keeps the new chat tasks visible when a previous chat stop completes late', async () => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  const task = (id: string) => ({ id, kind: 'sub_agent' as const, label: id, started_at: new Date().toISOString(), can_stop: true })
  const list = vi.spyOn(agentApi, 'getSessionBackgroundWork').mockImplementation(async sessionId => ({ session_id: sessionId, items: [task(sessionId)] } satisfies SessionBackgroundWorkResponse))
  let finishStop!: () => void
  const stopItem = vi.spyOn(agentApi, 'stopSessionBackgroundWork').mockImplementation(() => new Promise<void>(resolve => { finishStop = resolve }))
  useChatStore.setState({ activeSessionsCache: [], tabEvents: {}, chatTabs: {
    a: { tabId: 'a', sessionId: 'session-a', hasRunningBgAgents: true } as ChatTab,
    b: { tabId: 'b', sessionId: 'session-b', hasRunningBgAgents: true } as ChatTab,
  } })
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<BackgroundWorkPill tabId="a" />))
    await act(async () => host.querySelector<HTMLButtonElement>('[data-testid="background-work-pill"]')!.click())
    await act(async () => host.querySelector<HTMLButtonElement>('[data-testid="background-work-stop"]')!.click())
    expect(stopItem).toHaveBeenCalledWith('session-a', 'session-a')
    await act(async () => root.render(<BackgroundWorkPill tabId="b" />))
    expect(host.querySelector('[data-testid="background-work-item"]')?.textContent).toContain('session-b')
    list.mockClear()
    await act(async () => finishStop())
    expect(host.querySelector('[data-testid="background-work-item"]')?.textContent).toContain('session-b')
    expect(host.textContent).not.toContain('session-a')
    expect(list).not.toHaveBeenCalledWith('session-a')
  } finally { await act(async () => root.unmount()); host.remove() }
})
