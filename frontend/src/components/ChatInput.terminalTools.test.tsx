// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.hoisted(() => {
  const data = new Map<string, string>()
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: {
    getItem: (key: string) => data.get(key) ?? null,
    setItem: (key: string, value: string) => { data.set(key, value) },
    removeItem: (key: string) => { data.delete(key) },
    clear: () => data.clear(),
  } })
})

// Keep the real shared composer, command picker, registry, and stores. Only
// unrelated configuration panels and network calls are replaced.
vi.mock('./providers/CodingProvidersPanel', () => ({ default: () => null }))
vi.mock('../commands/user-commands', () => ({ loadAndRegisterUserCommands: vi.fn().mockResolvedValue(undefined) }))

import { ChatInput } from './ChatInput'
import { TerminalFocusLayout } from './TerminalFocusLayout'
import { useChatStore, type ChatTab } from '../stores/useChatStore'
import { useLLMStore } from '../stores/useLLMStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import { useModeStore } from '../stores/useModeStore'
import { agentApi } from '../services/api'
import { setProductCommands, setUserCommands } from '../commands/registry'
import { MAIN_TERMINAL_FOCUS_EVENT } from '../utils/mainTerminalFocus'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

function terminalTab(id: string): ChatTab {
  return {
    tabId: id, name: id, sessionId: `${id}-session`, viewMode: 'terminal',
    isStreaming: false, isCompleted: false, hasRunningBgAgents: false,
    isSyntheticTurn: false, canSteer: false, hideToolCalls: false,
    createdAt: 0, lastViewedEventCount: 0, lastViewedEventCounts: { micro: 0 },
    metadata: { mode: 'workflow', phaseId: 'workflow-builder', workshopMode: 'workshop' },
    config: { inputText: 'review this file', useCodeExecutionMode: false,
      selectedServers: [], selectedSkills: [], selectedSecrets: [],
      llmConfig: { provider: 'claude-code', model_id: 'claude-code' },
      fileContext: [], workflowContext: [], queuedMessages: [], browserMode: 'none' },
  }
}

describe('terminal toolbar shared tools', () => {
  let host: HTMLDivElement
  let root: Root
  let client: QueryClient
  const send = vi.fn(async () => true)
  const renderComposer = (tabId?: string, enabled = true) => root.render(<QueryClientProvider client={client}>
    <TerminalFocusLayout tabId={tabId} enabled={enabled} className="h-screen">
      <ChatInput tabId={tabId} onSubmit={send} onStopStreaming={vi.fn()} />
    </TerminalFocusLayout>
  </QueryClientProvider>)
  beforeEach(async () => {
    useProductSurfaceStore.setState({ productSurface: 'agentworks' })
    useModeStore.setState({ selectedModeCategory: 'workflow' })
    useLLMStore.setState({ providerManifestLoaded: true, llmConfigLocked: false })
    useChatStore.setState({ activeTabId: 'A', chatTabs: { A: terminalTab('A'), B: terminalTab('B') }, activeSessionsCache: [], tabEvents: {} })
    useWorkspaceStore.setState({ activeFolder: 'Workflow/project' })
    vi.spyOn(useWorkspaceStore.getState(), 'fetchFiles').mockResolvedValue(undefined)
    vi.spyOn(agentApi, 'listTerminals').mockResolvedValue({ terminals: [] } as never)
    vi.spyOn(agentApi, 'uploadPlannerFile').mockResolvedValue({ data: { file_path: 'Workflow/project/example.txt', absolute_path: '/workspace/Workflow/project/example.txt' } } as never)
    vi.spyOn(agentApi, 'getMainTerminal').mockImplementation(async sessionId => ({ terminal_id: `${sessionId}:main`, session_id: sessionId, tmux_session: `${sessionId}-tmux`, process_state: 'live', active: false } as never))
    vi.spyOn(agentApi, 'sendTerminalInput').mockResolvedValue(undefined)
    client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    host = document.createElement('div'); document.body.append(host); root = createRoot(host)
    await act(async () => renderComposer())
  })
  afterEach(async () => {
    await act(async () => root?.unmount())
    useProductSurfaceStore.setState({ productSurface: 'agentworks' }); client?.clear(); host?.remove(); setUserCommands([]); setProductCommands([]); vi.restoreAllMocks(); send.mockClear()
  })
  const toolbar = () => host.querySelector('[data-testid="native-terminal-toolbar"]')!
  const button = (label: string) => toolbar().querySelector<HTMLButtonElement>(`[aria-label="${label}"]`)!
  const textarea = () => host.querySelector<HTMLTextAreaElement>('[data-testid="chat-input-textarea"]')!
  const composer = () => host.querySelector<HTMLDivElement>('[data-testid="message-composer"]')!
  const attach = async () => {
    await act(async () => button('Attach files').click())
    const input = host.querySelector<HTMLInputElement>('input[type="file"]')!
    Object.defineProperty(input, 'files', { configurable: true, value: [new File(['example'], 'example.txt', { type: 'text/plain' })] })
    await act(async () => input.dispatchEvent(new Event('change', { bubbles: true })))
  }


  it('uses the Relay catalog for both command picker and typed commands, excluding Pulse', async () => {
    await act(async () => {
      useProductSurfaceStore.setState({ productSurface: 'relays' })
      setProductCommands([{ command: 'publish', description: 'Publish Relay API version', modes: ['workflow'], requiredWorkshopMode: 'workshop', icon: null, source: 'product', execute: ctx => ctx.onSubmit('Publish Relay version') }])
      renderComposer()
    })
    await act(async () => button('Browse commands').click())
    const options = [...document.querySelectorAll<HTMLElement>('[role="option"]')]
    expect(options.map(node => node.textContent)).toEqual([expect.stringContaining('/publish')])
    expect(document.body.textContent).not.toContain('/pulse')
    await act(async () => options[0].click())
    expect(send).toHaveBeenCalledWith('Publish Relay version', expect.objectContaining({ sourceTabId: 'A' }))
    send.mockClear()
    await act(async () => useChatStore.getState().setTabViewMode('A', 'formatted'))
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(textarea(), '/pulse')
      textarea().dispatchEvent(new Event('input', { bubbles: true }))
    })
    // Pulse may be sent as ordinary user text, but cannot execute its API action.
    expect(document.body.textContent).not.toContain('Run one complete Pulse')
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(textarea(), '/publish')
      textarea().dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => textarea().dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })))
    expect(send).toHaveBeenCalledWith('Publish Relay version', expect.objectContaining({ sourceTabId: 'A' }))
  })

  it('executes from a standalone picker without opening or consuming the chat draft', async () => {
    await act(async () => setUserCommands([{ command: 'terminal-review', description: 'Review in this session', icon: 'terminal', source: 'user', modes: ['workflow'], execute: ctx => { expect(ctx.beforeSlash).toBe(''); ctx.onSubmit('review via app command') } }]))
    expect(composer().hidden).toBe(true)
    await act(async () => button('Browse commands').click())
    expect(composer().hidden).toBe(true)
    expect(textarea().value).toBe('review this file')
    expect(document.activeElement).toBe(host.querySelector('[aria-label="Search commands"]'))
    const command = [...document.querySelectorAll<HTMLElement>('[role="option"]')].find(node => node.textContent?.includes('/terminal-review'))!
    expect(command).toBeDefined()
    await act(async () => command.click())
    expect(send).toHaveBeenCalledWith('review via app command', expect.objectContaining({ sourceTabId: 'A' }))
    expect(useChatStore.getState().getTab('A')?.viewMode).toBe('terminal')
    expect(composer().hidden).toBe(true)
    expect(textarea().value).toBe('review this file')
    expect(useChatStore.getState().getTabConfig('A')?.inputText).toBe('review this file')
    expect(document.querySelector('[role="listbox"]')).toBeNull()
  })

  const clickWithMouse = (target: HTMLElement) => {
    target.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
    target.focus()
    target.dispatchEvent(new MouseEvent('mouseup', { bubbles: true }))
    target.click()
  }

  it.each(['button', 'Escape'])('dismisses via %s and returns focus to the originating terminal', async action => {
    const focus = vi.fn()
    window.addEventListener(MAIN_TERMINAL_FOCUS_EVENT, focus)
    try {
      await act(async () => clickWithMouse(button('Browse commands')))
      expect(button('Browse commands').getAttribute('aria-expanded')).toBe('true')
      expect(composer().hidden).toBe(true)
      await act(async () => {
        if (action === 'button') clickWithMouse(button('Browse commands'))
        else document.activeElement!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }))
      })
      expect(document.querySelector('[role="listbox"]')).toBeNull()
      expect(button('Browse commands').getAttribute('aria-expanded')).toBe('false')
      expect(textarea().value).toBe('review this file')
      expect(send).not.toHaveBeenCalled()
      expect(focus).toHaveBeenCalledOnce()
      expect((focus.mock.calls[0][0] as CustomEvent).detail).toEqual({ sessionId: 'A-session' })
    } finally { window.removeEventListener(MAIN_TERMINAL_FOCUS_EVENT, focus) }
  })

  it('searches and navigates commands with native arrow keys and Enter', async () => {
    await act(async () => setUserCommands(['one', 'two'].map(name => ({ command: `terminal-${name}`, description: name,
      icon: 'terminal', source: 'user' as const, modes: ['workflow' as const], execute: ctx => ctx.onSubmit(name) }))))
    await act(async () => button('Browse commands').click())
    const search = host.querySelector<HTMLInputElement>('[aria-label="Search commands"]')!
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
      setter.call(search, 'terminal-')
      search.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(host.querySelectorAll('[role="option"]')).toHaveLength(2)
    for (const key of ['ArrowDown', 'ArrowUp', 'ArrowDown', 'Enter']) {
      await act(async () => search.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true })))
    }
    expect(send).toHaveBeenCalledWith('two', expect.objectContaining({ sourceTabId: 'A' }))
    expect(textarea().value).toBe('review this file')
    expect(composer().hidden).toBe(true)
  })

  it('keeps the hidden draft when command validation rejects execution', async () => {
    const execute = vi.fn()
    await act(async () => setUserCommands([{ command: 'terminal-blocked', description: 'Blocked', icon: 'terminal',
      source: 'user', modes: ['workflow'], validate: () => 'Missing configuration', execute }]))
    await act(async () => button('Browse commands').click())
    const command = [...host.querySelectorAll<HTMLElement>('[role="option"]')].find(node => node.textContent?.includes('/terminal-blocked'))!
    await act(async () => command.click())
    expect(execute).not.toHaveBeenCalled()
    expect(send).not.toHaveBeenCalled()
    expect(textarea().value).toBe('review this file')
    expect(composer().hidden).toBe(true)
    expect(host.querySelector('[role="listbox"]')).toBeNull()
  })

  it('dismisses on outside focus without stealing focus or capturing the next input’s keys', async () => {
    await act(async () => button('Browse commands').click())
    const outside = document.createElement('input')
    document.body.append(outside)
    const focus = vi.fn()
    window.addEventListener(MAIN_TERMINAL_FOCUS_EVENT, focus)
    try {
      await act(async () => outside.focus())
      expect(document.activeElement).toBe(outside)
      expect(host.querySelector('[role="listbox"]')).toBeNull()
      const key = new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true, cancelable: true })
      outside.dispatchEvent(key)
      expect(key.defaultPrevented).toBe(false)
      expect(focus).not.toHaveBeenCalled()
      expect(send).not.toHaveBeenCalled()
    } finally { outside.remove(); window.removeEventListener(MAIN_TERMINAL_FOCUS_EVENT, focus) }
  })

  it('opens a command options dialog without the composer or unrelated chat draft', async () => {
    await act(async () => setProductCommands([{ command: 'run-technical-review', description: 'Review', icon: 'terminal',
      source: 'product', modes: ['workflow'], execute: ctx => ctx.onSubmit('focused review') }]))
    await act(async () => button('Browse commands').click())
    const command = [...host.querySelectorAll<HTMLElement>('[role="option"]')].find(node => node.textContent?.includes('/run-technical-review'))!
    await act(async () => command.click())
    expect(composer().hidden).toBe(true)
    expect(document.querySelector('[role="dialog"]')).not.toBeNull()
    expect(document.querySelector<HTMLTextAreaElement>('[role="dialog"] textarea')!.value).toBe('')
    await act(async () => document.querySelector<HTMLFormElement>('[role="dialog"]')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
    expect(send).toHaveBeenCalledWith('focused review', expect.objectContaining({ sourceTabId: 'A' }))
    expect(textarea().value).toBe('review this file')
    expect(composer().hidden).toBe(true)
    expect(document.querySelector('[role="dialog"]')).toBeNull()
  })

  it('uploads and pastes references into tmux without a composer, submission, or consuming the saved draft', async () => {
    const focus = vi.fn()
    window.addEventListener(MAIN_TERMINAL_FOCUS_EVENT, focus)
    try {
      const input = host.querySelector<HTMLInputElement>('input[type="file"]')!
      const click = vi.spyOn(input, 'click')
      expect(button('Show message composer')).toBeNull()
      expect(button('Hide message composer')).toBeNull()
      await attach()
      expect(click).toHaveBeenCalledOnce()
      expect(composer().hidden).toBe(true)
      expect(agentApi.uploadPlannerFile).toHaveBeenCalledWith(expect.any(File), 'Workflow/project', expect.any(String))
      expect(agentApi.getMainTerminal).toHaveBeenCalledWith('A-session', { content: 'none' })
      expect(agentApi.sendTerminalInput).toHaveBeenCalledWith('A-session:main', ' @/workspace/Workflow/project/example.txt ', false)
      expect(useChatStore.getState().getTabConfig('A')?.fileContext).toEqual([])
      expect(useChatStore.getState().getTabConfig('B')?.fileContext).toEqual([])
      expect(textarea().value).toBe('review this file')
      expect(send).not.toHaveBeenCalled()
      expect((focus.mock.calls[0][0] as CustomEvent).detail).toEqual({ sessionId: 'A-session' })
      await act(async () => button('Return to chat').click())
      expect(composer().hidden).toBe(false)
      expect(textarea().value).toBe('review this file')
    } finally { window.removeEventListener(MAIN_TERMINAL_FOCUS_EVENT, focus) }
  })

  it('preserves successful uploads as chat attachments when tmux paste fails', async () => {
    vi.mocked(agentApi.sendTerminalInput).mockRejectedValueOnce(new Error('Disconnected'))
    await attach()
    expect(composer().hidden).toBe(true)
    expect(useChatStore.getState().getTabConfig('A')?.fileContext).toEqual([{ name: 'example.txt', path: 'Workflow/project/example.txt', type: 'file' }])
    expect(textarea().value).toBe('review this file')
    expect(send).not.toHaveBeenCalled()
    await act(async () => button('Return to chat').click())
    expect(composer().hidden).toBe(false)
  })

  it.each(['no live pane', 'missing absolute path'])('retains attachments instead of pasting when there is %s', async reason => {
    if (reason === 'no live pane') vi.mocked(agentApi.getMainTerminal).mockResolvedValueOnce({ terminal_id: 'A-session:main', active: false, process_state: 'closed' } as never)
    else vi.mocked(agentApi.uploadPlannerFile).mockResolvedValueOnce({ data: { filepath: 'Workflow/project/example.txt' } } as never)
    await attach()
    expect(agentApi.sendTerminalInput).not.toHaveBeenCalled()
    expect(useChatStore.getState().getTabConfig('A')?.fileContext).toHaveLength(1)
    expect(textarea().value).toBe('review this file')
    expect(composer().hidden).toBe(true)
  })

  it('rechecks session ownership after the terminal lookup completes', async () => {
    vi.mocked(agentApi.getMainTerminal).mockImplementationOnce(async () => {
      const state = useChatStore.getState()
      useChatStore.setState({ chatTabs: { ...state.chatTabs, A: { ...state.chatTabs.A, sessionId: 'replacement' } } })
      return { terminal_id: 'A-session:main', tmux_session: 'old-tmux', active: true } as never
    })
    await attach()
    expect(agentApi.sendTerminalInput).not.toHaveBeenCalled()
    expect(useChatStore.getState().getTabConfig('A')?.fileContext).toEqual([])
    expect(textarea().value).toBe('review this file')
  })

  it('uploads in chat mode without pasting into tmux', async () => {
    await act(async () => button('Return to chat').click())
    const input = host.querySelector<HTMLInputElement>('input[type="file"]')!
    Object.defineProperty(input, 'files', { configurable: true, value: [new File(['example'], 'example.txt')] })
    await act(async () => input.dispatchEvent(new Event('change', { bubbles: true })))
    expect(agentApi.sendTerminalInput).not.toHaveBeenCalled()
    expect(useChatStore.getState().getTabConfig('A')?.fileContext).toHaveLength(1)
    expect(textarea().value).toBe('review this file')
    expect(composer().hidden).toBe(false)
  })

  it.each(['session', 'view'])('does not paste into a changed %s after upload started', async change => {
    vi.mocked(agentApi.uploadPlannerFile).mockImplementationOnce(async () => {
      const state = useChatStore.getState()
      if (change === 'session') useChatStore.setState({ chatTabs: { ...state.chatTabs, A: { ...state.chatTabs.A, sessionId: 'replacement' } } })
      else state.setTabViewMode('A', 'formatted')
      return { data: { filepath: 'Workflow/project/example.txt', absolute_path: '/workspace/Workflow/project/example.txt' } } as never
    })
    await attach()
    expect(agentApi.sendTerminalInput).not.toHaveBeenCalled()
    expect(textarea().value).toBe('review this file')
    expect(useChatStore.getState().getTabConfig('A')?.fileContext).toHaveLength(change === 'view' ? 1 : 0)
  })

  it('quotes paths with spaces and uses the scoped terminal when another tab is globally active', async () => {
    await act(async () => renderComposer('B'))
    vi.mocked(agentApi.uploadPlannerFile).mockResolvedValueOnce({ data: { filepath: 'Workflow/project/my file.txt', absolute_path: '/workspace/Workflow/project/my file.txt' } } as never)
    await attach()
    expect(agentApi.sendTerminalInput).toHaveBeenCalledWith('B-session:main', ' @"/workspace/Workflow/project/my file.txt" ', false)
    expect(useChatStore.getState().activeTabId).toBe('A')
    expect(send).not.toHaveBeenCalled()
  })

  it('closes an open command picker when changing tabs and keeps the next terminal composer collapsed', async () => {
    await act(async () => button('Browse commands').click())
    expect(textarea().getAttribute('aria-expanded')).toBe('true')
    await act(async () => useChatStore.setState({ activeTabId: 'B' }))
    expect(composer().hidden).toBe(true)
    expect(textarea().getAttribute('aria-expanded')).toBe('false')
    expect(document.querySelector('[role="listbox"]')).toBeNull()
    expect(send).not.toHaveBeenCalled()
  })

  it('toggles focus without remounting the composer, and restores normal layout on return to chat', async () => {
    const original = textarea()
    await act(async () => button('Enter focus mode').click())
    expect(host.querySelector('[data-terminal-focus="true"]')).not.toBeNull()
    expect(button('Exit focus mode').getAttribute('aria-pressed')).toBe('true')
    expect(textarea()).toBe(original)
    expect(textarea().value).toBe('review this file')
    expect(send).not.toHaveBeenCalled()
    await act(async () => button('Exit focus mode').click())
    expect(host.querySelector('[data-terminal-focus="true"]')).toBeNull()
    await act(async () => button('Enter focus mode').click())
    await act(async () => button('Return to chat').click())
    expect(host.querySelector('[data-terminal-focus="true"]')).toBeNull()
    expect(textarea()).toBe(original)
    expect(textarea().value).toBe('review this file')
    await act(async () => useChatStore.getState().setTabViewMode('A', 'terminal'))
    expect(button('Enter focus mode').getAttribute('aria-pressed')).toBe('false')
  })

  it.each(['tab', 'session', 'close'])('does not carry focus mode to a changed %s', async change => {
    await act(async () => button('Enter focus mode').click())
    await act(async () => {
      const state = useChatStore.getState()
      if (change === 'tab') useChatStore.setState({ activeTabId: 'B' })
      if (change === 'session') useChatStore.setState({ chatTabs: { ...state.chatTabs, A: { ...state.chatTabs.A, sessionId: 'replacement' } } })
      if (change === 'close') useChatStore.setState({ chatTabs: { B: state.chatTabs.B }, activeTabId: null })
    })
    expect(host.querySelector('[data-terminal-focus="true"]')).toBeNull()
    expect(send).not.toHaveBeenCalled()
  })

  it('focuses the scoped project conversation even when the globally active tab differs', async () => {
    await act(async () => renderComposer('B'))
    await act(async () => button('Enter focus mode').click())
    expect(host.querySelector('[data-terminal-focus="true"]')).not.toBeNull()
    expect(useChatStore.getState().activeTabId).toBe('A')
    await act(async () => useChatStore.getState().setTabViewMode('A', 'formatted'))
    expect(host.querySelector('[data-terminal-focus="true"]')).not.toBeNull()
    await act(async () => useChatStore.getState().setTabViewMode('B', 'formatted'))
    expect(host.querySelector('[data-terminal-focus="true"]')).toBeNull()
  })

  it('restores navigation on global pages and does not resume focus when returning', async () => {
    await act(async () => button('Enter focus mode').click())
    await act(async () => renderComposer(undefined, false))
    expect(host.querySelector('[data-terminal-focus="true"]')).toBeNull()
    expect(button('Exit focus mode')).toBeNull()
    await act(async () => renderComposer())
    expect(button('Enter focus mode').getAttribute('aria-pressed')).toBe('false')
  })
})
