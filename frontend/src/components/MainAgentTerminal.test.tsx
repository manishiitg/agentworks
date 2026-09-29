// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'

const { getMainTerminal } = vi.hoisted(() => ({
  getMainTerminal: vi.fn(),
}))

vi.mock('../services/api', () => ({
  agentApi: {
    getMainTerminal,
    getMainTerminalStreamUrl: vi.fn(() => 'ws://localhost/terminal'),
  },
}))
vi.mock('../hooks/useTheme', () => ({ useTheme: () => ({ theme: 'dark' }) }))
vi.mock('./TerminalCenter', () => ({
  LiveAttachXtermPane: () => <div data-testid="live-terminal" />,
  StaticXtermPane: () => <div data-testid="static-terminal" />,
  RAW_XTERM_THEMES: { dark: {} },
}))

import { MainAgentTerminal, MAIN_AGENT_TERMINAL_MIN_WIDTH_PX } from './MainAgentTerminal'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

afterEach(() => {
  vi.clearAllMocks()
  document.body.innerHTML = ''
})

describe('MainAgentTerminal sizing', () => {
  it('says the live view has not started instead of switching back to the chat', async () => {
    getMainTerminal.mockRejectedValue({ response: { status: 404 } })
    const onUnavailable = vi.fn()
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<MainAgentTerminal sessionId="session-1" onUnavailable={onUnavailable} />))
      await act(async () => Promise.resolve())
      const notice = host.querySelector('[data-testid="main-agent-terminal-not-started"]')
      expect(notice?.textContent).toContain('appears once the agent starts working')
      expect(onUnavailable).not.toHaveBeenCalled()
      await act(async () => (notice!.querySelector('button') as HTMLButtonElement).click())
      expect(onUnavailable).toHaveBeenCalledTimes(1)
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it('keeps the debug terminal near 80 columns and scrolls when the chat pane is narrower', async () => {
    getMainTerminal.mockResolvedValue({
      terminal_id: 'terminal-1',
      session_id: 'session-1',
      tmux_session: 'tmux-1',
      content: '',
      rows: [],
      chunk_index: 1,
      active: true,
      state: 'running',
      status: {},
      created_at: '2026-09-11T00:00:00Z',
      updated_at: '2026-09-11T00:00:00Z',
    })

    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<MainAgentTerminal sessionId="session-1" />))
      await act(async () => Promise.resolve())

      const scroller = host.querySelector('[data-testid="main-agent-terminal-scroll-container"]')
      const terminalGrid = host.querySelector<HTMLElement>('[data-testid="main-agent-terminal-grid"]')

      expect(scroller?.classList.contains('overflow-x-auto')).toBe(true)
      expect(terminalGrid?.style.minWidth).toBe(`${MAIN_AGENT_TERMINAL_MIN_WIDTH_PX}px`)
      expect(MAIN_AGENT_TERMINAL_MIN_WIDTH_PX).toBe(680)
      expect(host.querySelector('[data-testid="live-terminal"]')).not.toBeNull()
      expect(getMainTerminal).toHaveBeenCalledWith('session-1', { content: 'none' })
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it('loads only 1,000 history lines once when the pane is already settled', async () => {
    getMainTerminal
      .mockResolvedValueOnce({
        terminal_id: 'terminal-1',
        session_id: 'session-1',
        tmux_session: 'tmux-1',
        content: '',
        rows: [],
        chunk_index: 2,
        active: false,
        state: 'completed',
        status: {},
        created_at: '2026-09-11T00:00:00Z',
        updated_at: '2026-09-11T00:01:00Z',
      })
      .mockResolvedValueOnce({
        terminal_id: 'terminal-1',
        session_id: 'session-1',
        tmux_session: 'tmux-1',
        content: 'final output',
        rows: [],
        chunk_index: 2,
        active: false,
        state: 'completed',
        status: {},
        created_at: '2026-09-11T00:00:00Z',
        updated_at: '2026-09-11T00:01:00Z',
      })

    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<MainAgentTerminal sessionId="session-1" />))
      await act(async () => Promise.resolve())
      await act(async () => Promise.resolve())

      expect(getMainTerminal).toHaveBeenNthCalledWith(1, 'session-1', { content: 'none' })
      expect(getMainTerminal).toHaveBeenNthCalledWith(2, 'session-1', { content: 'history', lines: 1000 })
      expect(host.querySelector('[data-testid="static-terminal"]')).not.toBeNull()
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it('refetches settled history when the retained pane moves to a new revision between polls', async () => {
    vi.useFakeTimers()
    const settled = (chunk: number, content: string) => ({
      terminal_id: 'terminal-1',
      session_id: 'session-1',
      tmux_session: 'tmux-1',
      content,
      rows: [],
      chunk_index: chunk,
      active: false,
      state: 'completed',
      status: {},
      created_at: '2026-09-11T00:00:00Z',
      updated_at: '2026-09-11T00:01:00Z',
    })
    getMainTerminal
      .mockResolvedValueOnce(settled(2, ''))
      .mockResolvedValueOnce(settled(2, 'turn one'))
      // Same revision: metadata only.
      .mockResolvedValueOnce(settled(2, ''))
      // A whole turn ran and settled between polls.
      .mockResolvedValueOnce(settled(4, ''))
      .mockResolvedValueOnce(settled(5, 'turn one\nturn two'))

    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<MainAgentTerminal sessionId="session-1" />))
      await act(async () => { await vi.advanceTimersByTimeAsync(0) })
      expect(getMainTerminal).toHaveBeenCalledTimes(2)

      await act(async () => { await vi.advanceTimersByTimeAsync(3000) })
      expect(getMainTerminal).toHaveBeenCalledTimes(3)

      await act(async () => { await vi.advanceTimersByTimeAsync(3000) })
      expect(getMainTerminal).toHaveBeenCalledTimes(5)
      expect(getMainTerminal).toHaveBeenNthCalledWith(5, 'session-1', { content: 'history', lines: 1000 })
    } finally {
      await act(async () => root.unmount())
      host.remove()
      vi.useRealTimers()
    }
  })

  it('keeps the live view mounted when a turn ends but the retained pane is still live', async () => {
    const base = {
      terminal_id: 'terminal-1', session_id: 'session-1', tmux_session: 'tmux-1', content: '', rows: [], chunk_index: 1,
      state: 'running', status: {}, created_at: '2026-09-29T00:00:00Z', updated_at: '2026-09-29T00:00:00Z',
    }
    getMainTerminal.mockResolvedValue({ ...base, active: false, process_state: 'live' })
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<MainAgentTerminal sessionId="session-1" />))
      await act(async () => Promise.resolve())
      expect(host.querySelector('[data-testid="live-terminal"]')).not.toBeNull()
      expect(host.querySelector('[data-testid="static-terminal"]')).toBeNull()
      // Only the settled history of a pane that is gone may use the static view.
      expect(getMainTerminal).not.toHaveBeenCalledWith('session-1', expect.objectContaining({ content: 'history' }))
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  // One bad poll must never take down a working terminal: a slow or failed
  // request (the server is busy while a message is sent) used to replace it
  // with an error, and one 404 with "not started"; the next poll then brought
  // it back, which looked like a one-second crash.
  describe('with a terminal already showing', () => {
    const live = {
      terminal_id: 'terminal-1', session_id: 'session-1', tmux_session: 'tmux-1', content: '', rows: [], chunk_index: 1,
      active: true, state: 'running', process_state: 'live', status: {}, created_at: '2026-09-29T00:00:00Z', updated_at: '2026-09-29T00:00:00Z',
    }
    async function mount() {
      vi.useFakeTimers()
      const host = document.createElement('div')
      document.body.append(host)
      const root = createRoot(host)
      await act(async () => root.render(<MainAgentTerminal sessionId="session-1" />))
      await act(async () => { await vi.advanceTimersByTimeAsync(0) })
      expect(host.querySelector('[data-testid="live-terminal"]')).not.toBeNull()
      return { host, cleanup: async () => { await act(async () => root.unmount()); host.remove(); vi.useRealTimers() } }
    }
    const poll = () => act(async () => { await vi.advanceTimersByTimeAsync(3000) })

    it('keeps it through a failed poll', async () => {
      getMainTerminal.mockResolvedValueOnce(live).mockRejectedValueOnce(new Error('timeout of 15000ms exceeded')).mockResolvedValue(live)
      const { host, cleanup } = await mount()
      try {
        await poll()
        expect(host.querySelector('[data-testid="live-terminal"]')).not.toBeNull()
        expect(host.textContent).not.toContain('timeout')
        await poll()
        expect(host.querySelector('[data-testid="live-terminal"]')).not.toBeNull()
      } finally { await cleanup() }
    })

    it('keeps it through a single 404 and drops it only when the pane stays gone', async () => {
      const gone = { response: { status: 404 } }
      getMainTerminal.mockResolvedValueOnce(live).mockRejectedValueOnce(gone).mockResolvedValueOnce(live)
        .mockRejectedValueOnce(gone).mockRejectedValueOnce(gone).mockRejectedValueOnce(gone)
      const { host, cleanup } = await mount()
      try {
        await poll() // one miss
        expect(host.querySelector('[data-testid="live-terminal"]')).not.toBeNull()
        await poll() // recovered: the miss count resets
        await poll() // miss 1 again
        await poll() // miss 2
        expect(host.querySelector('[data-testid="live-terminal"]')).not.toBeNull()
        await poll() // miss 3: gone
        expect(host.querySelector('[data-testid="main-agent-terminal-not-started"]')).not.toBeNull()
      } finally { await cleanup() }
    })

    it('still shows the error when there was never a terminal', async () => {
      vi.useFakeTimers()
      getMainTerminal.mockRejectedValue(new Error('boom'))
      const host = document.createElement('div')
      document.body.append(host)
      const root = createRoot(host)
      try {
        await act(async () => root.render(<MainAgentTerminal sessionId="session-1" />))
        await act(async () => { await vi.advanceTimersByTimeAsync(0) })
        expect(host.textContent).toContain('boom')
      } finally { await act(async () => root.unmount()); host.remove(); vi.useRealTimers() }
    })
  })
})
