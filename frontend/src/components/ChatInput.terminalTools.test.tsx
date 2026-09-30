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
import { useChatStore, type ChatTab } from '../stores/useChatStore'
import { useLLMStore } from '../stores/useLLMStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useModeStore } from '../stores/useModeStore'
import { agentApi } from '../services/api'
import { setUserCommands } from '../commands/registry'
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
  beforeEach(async () => {
    useModeStore.setState({ selectedModeCategory: 'workflow' })
    useLLMStore.setState({ providerManifestLoaded: true, llmConfigLocked: false })
    useChatStore.setState({ activeTabId: 'A', chatTabs: { A: terminalTab('A'), B: terminalTab('B') }, activeSessionsCache: [], tabEvents: {} })
    useWorkspaceStore.setState({ activeFolder: 'Workflow/project' })
    vi.spyOn(useWorkspaceStore.getState(), 'fetchFiles').mockResolvedValue(undefined)
    vi.spyOn(agentApi, 'listTerminals').mockResolvedValue({ terminals: [] } as never)
    vi.spyOn(agentApi, 'uploadPlannerFile').mockResolvedValue({ data: { file_path: 'Workflow/project/example.txt' } } as never)
    client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    host = document.createElement('div'); document.body.append(host); root = createRoot(host)
    await act(async () => root.render(<QueryClientProvider client={client}><ChatInput onSubmit={send} onStopStreaming={vi.fn()} /></QueryClientProvider>))
  })
  afterEach(async () => {
    await act(async () => root?.unmount())
    client?.clear(); host?.remove(); setUserCommands([]); vi.restoreAllMocks(); send.mockClear()
  })
  const toolbar = () => host.querySelector('[data-testid="native-terminal-toolbar"]')!
  const button = (label: string) => toolbar().querySelector<HTMLButtonElement>(`[aria-label="${label}"]`)!
  const textarea = () => host.querySelector<HTMLTextAreaElement>('[data-testid="chat-input-textarea"]')!
  const composer = () => document.getElementById(button('Show message composer')?.getAttribute('aria-controls') ?? button('Hide message composer').getAttribute('aria-controls')!)!

  it('opens the existing command picker without changing the draft or leaving tmux', async () => {
    await act(async () => setUserCommands([{ command: 'terminal-review', description: 'Review in this session', icon: 'terminal', source: 'user', modes: ['workflow'], execute: ctx => { ctx.onSubmit('review via app command') } }]))
    expect(composer().hidden).toBe(true)
    await act(async () => button('Browse commands').click())
    expect(composer().hidden).toBe(false)
    expect(textarea().value).toBe('review this file')
    expect(document.activeElement).toBe(textarea())
    const command = [...document.querySelectorAll<HTMLElement>('[role="option"]')].find(node => node.textContent?.includes('/terminal-review'))!
    expect(command).toBeDefined()
    await act(async () => command.click())
    expect(send).toHaveBeenCalledWith('review via app command', expect.objectContaining({ sourceTabId: 'A' }))
    expect(useChatStore.getState().getTab('A')?.viewMode).toBe('terminal')
  })

  it('uploads through the scoped shared file input, keeps the draft, and sends in terminal mode', async () => {
    const input = host.querySelector<HTMLInputElement>('input[type="file"]')!
    const click = vi.spyOn(input, 'click')
    await act(async () => button('Attach files').click())
    expect(click).toHaveBeenCalledOnce()
    expect(composer().hidden).toBe(false)
    const file = new File(['example'], 'example.txt', { type: 'text/plain' })
    Object.defineProperty(input, 'files', { configurable: true, value: [file] })
    await act(async () => input.dispatchEvent(new Event('change', { bubbles: true })))
    expect(agentApi.uploadPlannerFile).toHaveBeenCalledWith(file, 'Workflow/project', expect.any(String))
    expect(useChatStore.getState().getTabConfig('A')?.fileContext).toEqual([{ name: 'example.txt', path: 'Workflow/project/example.txt', type: 'file' }])
    expect(useChatStore.getState().getTabConfig('B')?.fileContext).toEqual([])
    expect(textarea().value).toBe('review this file')
    expect(send).not.toHaveBeenCalled()
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Send to terminal"]')!.click())
    expect(send).toHaveBeenCalledWith('review this file', expect.objectContaining({ sourceTabId: 'A' }))
    expect(useChatStore.getState().getTab('A')?.viewMode).toBe('terminal')
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

  it('retains rejected submissions and attachments, and returns focus to the originating terminal on collapse', async () => {
    const focus = vi.fn()
    window.addEventListener(MAIN_TERMINAL_FOCUS_EVENT, focus)
    try {
      const file = { name: 'example.txt', path: 'Workflow/project/example.txt', type: 'file' as const }
      await act(async () => useChatStore.getState().setTabConfig('A', { fileContext: [file] }))
      await act(async () => button('Show message composer').click())
      send.mockResolvedValueOnce(false)
      await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Send to terminal"]')!.click())
      expect(textarea().value).toBe('review this file')
      expect(useChatStore.getState().getTabConfig('A')?.fileContext).toEqual([file])
      await act(async () => button('Hide message composer').click())
      expect(composer().hidden).toBe(true)
      expect(focus).toHaveBeenCalledOnce()
      expect((focus.mock.calls[0][0] as CustomEvent).detail).toEqual({ sessionId: 'A-session' })
      expect(useChatStore.getState().getTab('A')?.viewMode).toBe('terminal')
    } finally { window.removeEventListener(MAIN_TERMINAL_FOCUS_EVENT, focus) }
  })
})
