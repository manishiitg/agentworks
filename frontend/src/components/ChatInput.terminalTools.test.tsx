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
const contextPreference = vi.hoisted(() => ({ show: false }))
vi.mock('../hooks/useChatPreferences', () => ({ useChatPreferences: () => ({ showContextIndicator: contextPreference.show }) }))

import { ChatInput } from './ChatInput'
import { TerminalFocusLayout } from './TerminalFocusLayout'
import { useChatStore, type ChatTab } from '../stores/useChatStore'
import { useLLMStore } from '../stores/useLLMStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import { useModeStore } from '../stores/useModeStore'
import { useAuthStore } from '../stores/useAuthStore'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { useWorkflowManifestStore } from '../stores/useWorkflowManifestStore'
import { useWorkflowStore } from '../stores/useWorkflowStore'
import api, { agentApi, workflowManifestApi } from '../services/api'
import * as productProjects from '../platform/chat/productProjects'
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
  const stopTurn = vi.fn(() => useChatStore.getState().setTabStreaming('A', false))
  const newChat = vi.fn()
  const renderComposer = (tabId?: string, enabled = true) => root.render(<QueryClientProvider client={client}>
    <TerminalFocusLayout tabId={tabId} enabled={enabled} className="h-screen">
      <ChatInput tabId={tabId} onSubmit={send} onStopStreaming={stopTurn} onNewChat={newChat} showNewChatAction />
    </TerminalFocusLayout>
  </QueryClientProvider>)
  beforeEach(async () => {
    contextPreference.show = false
    useProductSurfaceStore.setState({ productSurface: 'agentworks' })
    useModeStore.setState({ selectedModeCategory: 'workflow' })
    // Terminal UI is now offered by the server's provider capability, not
    // inferred from a CLI provider name. These cases exercise that UI.
    useLLMStore.setState({ providerManifestLoaded: true, llmConfigLocked: false,
      providerManifest: [{ id: 'claude-code', coding_agent: { supports_terminal_stream: true } }] as never,
    })
    useChatStore.setState({ activeTabId: 'A', chatTabs: { A: terminalTab('A'), B: terminalTab('B') }, activeSessionsCache: [], tabEvents: {} })
    useWorkspaceStore.setState({ activeFolder: 'Workflow/project' })
    vi.spyOn(useWorkspaceStore.getState(), 'fetchFiles').mockResolvedValue(undefined)
    vi.spyOn(agentApi, 'getQueuedMessages').mockResolvedValue([])
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
    useProductSurfaceStore.setState({ productSurface: 'agentworks' }); client?.clear(); host?.remove(); setUserCommands([]); setProductCommands([]); vi.restoreAllMocks(); send.mockClear(); stopTurn.mockClear(); newChat.mockClear()
  })
  it('removes # from the draft with Backspace in an empty reference search and returns the caret', async () => {
    vi.spyOn(workflowManifestApi, 'listWorkflowManifests').mockResolvedValue({ success: true, total: 1,
      workflows: [{ workspace_path: 'Workflow/project', manifest: { id: 'project', label: 'Project' } }],
    } as Awaited<ReturnType<typeof workflowManifestApi.listWorkflowManifests>>)
    vi.spyOn(productProjects, 'loadProductProjects').mockResolvedValue([])
    await act(async () => {
      useChatStore.getState().setTabViewMode('A', 'formatted')
      renderComposer('A', false)
    })
    const input = host.querySelector('textarea')!
    await act(async () => {
      input.focus()
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(input, 'Keep # suffix')
      input.setSelectionRange(6, 6)
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    const search = host.querySelector<HTMLInputElement>('[aria-label="Search references"]')!
    expect(document.activeElement).toBe(search)
    // Deleting search text is ordinary editing; only an already empty
    // search should hand Backspace back to the composer's trigger.
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(search, 'project')
      search.dispatchEvent(new Event('input', { bubbles: true }))
      search.dispatchEvent(new KeyboardEvent('keydown', { key: 'Backspace', bubbles: true, cancelable: true }))
    })
    expect(input.value).toBe('Keep # suffix')
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(search, '')
      search.dispatchEvent(new Event('input', { bubbles: true }))
      search.dispatchEvent(new KeyboardEvent('keydown', { key: 'Backspace', bubbles: true, cancelable: true }))
      await new Promise(resolve => setTimeout(resolve, 0))
    })
    expect(host.querySelector('[aria-label="Search references"]')).toBeNull()
    expect(input.value).toBe('Keep  suffix')
    expect(useChatStore.getState().getTabConfig('A')?.inputText).toBe('Keep  suffix')
    expect(document.activeElement).toBe(input)
    expect(input.selectionStart).toBe(5)
    expect(input.selectionEnd).toBe(5)
    expect(send).not.toHaveBeenCalled()
  })
  it('shows the Goal Builder model under the server lock and the existing on-open agents menu', async () => {
    const llms = useLLMStore.getState(), presets = useGlobalPresetStore.getState()
    const workflows = useWorkflowManifestStore.getState(), auth = useAuthStore.getState()
    const get = vi.spyOn(api, 'get').mockResolvedValue({ data: { agents: [] } })
    const openModels = vi.spyOn(useWorkflowStore.getState(), 'openWorkspaceView').mockImplementation(() => {})
    try {
      await act(async () => {
        useAuthStore.setState({ user: { id: 'person-one', username: 'builder' } })
        useGlobalPresetStore.setState({ activePresetIds: { ...presets.activePresetIds, workflow: 'goal-one' } })
        useWorkflowManifestStore.setState({ workflows: [{ workspace_path: 'Workflow/project', manifest: {
          schema_version: 1, id: 'goal-one', label: 'Goal', ownership: { employee_id: null }, schedules: [],
          capabilities: { selected_servers: [], selected_tools: [], selected_skills: [], selected_secrets: [], selected_global_secret_names: [], browser_mode: 'none', use_code_execution_mode: false,
            llm_config: { schema_version: 2, mode: 'explicit', builder_llm: { provider: 'codex-cli', model_id: 'builder-model' } } },
        } }] })
        useLLMStore.setState({ savedLLMs: [], availableLLMs: [{ provider: 'codex-cli', model: 'builder-model', label: 'Codex · Builder model', description: '' }] })
        useChatStore.setState({ activeSessionsCache: [{ session_id: 'A-session', runtime: { provider: 'claude-code', model_id: 'old-model' } }] as never })
        useChatStore.getState().setTabViewMode('A', 'formatted')
        renderComposer('A', false)
      })
      const modelButton = host.querySelector<HTMLButtonElement>('button[aria-label="Model: Builder model"]')!
      expect(modelButton).not.toBeNull()
      expect(modelButton.textContent).toBe('Builder model')
      await act(async () => modelButton.click())
      expect(openModels).toHaveBeenCalledExactlyOnceWith('identity', 'llm')
      await act(async () => useWorkflowManifestStore.setState(state => ({ workflows: state.workflows.map(workflow => ({ ...workflow, my_access: 'read' as const })) })))
      expect(host.querySelector('button[aria-label="Model: Builder model"]')).toBeNull()
      expect(host.querySelector('[aria-label="Model: Builder model"]')).not.toBeNull()
      const directoryCalls = () => get.mock.calls.filter(([url]) => url === '/api/agent-directory')
      expect(directoryCalls()).toHaveLength(0)
      expect(host.querySelector('[aria-label="Agents you can reach"]')).toBeNull()
      const add = host.querySelector<HTMLButtonElement>('[aria-label="Add files and actions"]')!
      expect(add).not.toBeNull()
      await act(async () => { add.focus(); add.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true })) })
      expect(document.querySelector('[data-testid="chat-upload-button"]')?.textContent).toContain('Attach files')
      expect(directoryCalls()).toHaveLength(0)
      const agents = document.querySelector<HTMLElement>('[aria-label="Agents you can reach"]')!
      expect(agents).not.toBeNull()
      await act(async () => { agents.focus(); agents.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true })) })
      expect(directoryCalls()).toHaveLength(1)
      expect(document.querySelector('[aria-label="Accessible agents"]')).not.toBeNull()
      await act(async () => useLLMStore.setState({ llmConfigLocked: true, savedLLMs: [{ id: 'company-model', provider: 'cursor-cli', model_id: 'auto', name: 'Company model' }] }))
      expect(host.querySelector('[aria-label="Model: Company model"]')).not.toBeNull()
      expect(host.querySelector('[aria-label="Model: Builder model"]')).toBeNull()
      expect(directoryCalls()).toHaveLength(1)
    } finally {
      await act(async () => {
        useAuthStore.setState(auth); useLLMStore.setState(llms)
        useGlobalPresetStore.setState(presets); useWorkflowManifestStore.setState(workflows)
      })
    }
  })
  it('stops metadata intervals with context hidden and resumes them in the terminal view', async () => {
    vi.useFakeTimers()
    vi.mocked(agentApi.listTerminals).mockResolvedValue({ terminals: [{ terminal_id: 'A-main', active: true }] } as never)
    await act(async () => {
      useChatStore.getState().setTabStreaming('A', true)
      useChatStore.getState().setTabViewMode('A', 'formatted')
      renderComposer('A', false)
    })
    const before = vi.mocked(agentApi.listTerminals).mock.calls.length
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
    expect(agentApi.listTerminals).toHaveBeenCalledTimes(before)
    await act(async () => {
      useChatStore.getState().setTabViewMode('A', 'terminal')
      renderComposer('A')
    })
    await act(async () => { await vi.advanceTimersByTimeAsync(3_100) })
    expect(vi.mocked(agentApi.listTerminals).mock.calls.length).toBeGreaterThan(before)
    vi.useRealTimers()
  })
  it('sends the selected Queue next intent by click and Enter while a turn is running', async () => {
    useChatStore.getState().setTabStreaming('A', true)
    await act(async () => renderComposer(undefined, false))
    expect(host.querySelector('[aria-label^="Send options:"]')).toBeNull()
    await act(async () => {
      const add = host.querySelector<HTMLElement>('[aria-label="Add files and actions"]')!
      add.focus(); add.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
    })
    const queue = [...document.querySelectorAll<HTMLElement>('[role="menuitemradio"]')].find(row => row.textContent?.includes('Queue next'))!
    await act(async () => { queue.focus(); queue.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })) })
    const submit = host.querySelector<HTMLButtonElement>('[data-testid="chat-submit-button"]')!
    expect(submit.getAttribute('aria-label')).toBe('Queue next')
    expect(submit.querySelector('.lucide-clock')).toBeNull()
    expect(submit.querySelector('.lucide-arrow-up')).not.toBeNull()
    await act(async () => submit.click())
    expect(send).toHaveBeenLastCalledWith('review this file', expect.objectContaining({ sourceTabId: 'A', deliveryMode: 'queue' }))
    const input = host.querySelector<HTMLTextAreaElement>('[data-testid="chat-input-textarea"]')!
    await act(async () => useChatStore.getState().setTabConfig('A', { inputText: '' }))
    // Empty: the arrow is hidden but keeps its place, so typing does not shift the row.
    expect(host.querySelector('[data-testid="chat-submit-button"]')?.closest('[data-send-slot]')?.className).toBe('invisible')
    expect(host.querySelector('[aria-label="Add files and actions"]')).not.toBeNull()
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(input, 'second message')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })))
    expect(send).toHaveBeenLastCalledWith('second message', expect.objectContaining({ sourceTabId: 'A', deliveryMode: 'queue' }))
  })
  it('recalls a waiting message with Up only after server removal and sends the edit through Queue next', async () => {
    let queued = [{ message_id: 'queued-1', client_message_id: 'client-1', content: 'Original queued instruction.', queue_position: 1, queue_reason: 'running' as const, can_steer: true }]
    vi.mocked(agentApi.getQueuedMessages).mockImplementation(async () => queued)
    const remove = vi.spyOn(agentApi, 'removeQueuedMessage').mockRejectedValueOnce(new Error('offline'))
    remove.mockImplementation(async () => { queued = [] })
    await act(async () => {
      useChatStore.getState().setTabConfig('A', { inputText: '' })
      useChatStore.getState().setTabStreaming('A', true)
      renderComposer(undefined, false)
      await client.invalidateQueries({ queryKey: ['chat-queued-messages'] })
    })
    const flush = async () => act(async () => { await new Promise(resolve => setTimeout(resolve, 40)) })
    await flush()
    const input = host.querySelector<HTMLTextAreaElement>('[data-testid="chat-input-textarea"]')!
    const edit = host.querySelector<HTMLButtonElement>('[aria-label="Edit queued message"]')!
    expect(edit).toBeTruthy()
    await act(async () => edit.click())
    await flush()
    expect(input.value).toBe('')
    expect(host.textContent).toContain('Original queued instruction.')
    await act(async () => input.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true })))
    await flush()
    expect(remove).toHaveBeenLastCalledWith('A-session', 'queued-1')
    expect(input.value).toBe('Original queued instruction.')
    expect(host.querySelector('[aria-label="Queued messages"]')).toBeNull()
    expect(useChatStore.getState().getTabConfig('A')?.sendDeliveryMode).toBe('queue')
    expect(agentApi.sendTerminalInput).not.toHaveBeenCalled()
    expect(send).not.toHaveBeenCalled()
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(input, 'Updated queued instruction.')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })))
    expect(send).toHaveBeenLastCalledWith('Updated queued instruction.', expect.objectContaining({ deliveryMode: 'queue' }))
  })
  it('opens slash commands from the add menu without changing the draft or delivery mode', async () => {
    await act(async () => {
      setUserCommands([{ command: 'menu-review', description: 'Review via menu', icon: 'terminal', source: 'user', modes: ['workflow'], execute: ctx => ctx.onSubmit('review via menu') }])
      useChatStore.getState().setTabConfig('A', { sendDeliveryMode: 'queue' })
      renderComposer(undefined, false)
    })
    expect(host.querySelector('[data-testid="chat-command-menu-button"]')).toBeNull()
    const trigger = host.querySelector<HTMLButtonElement>('[aria-label="Add files and actions"]')!
    await act(async () => {
      trigger.focus()
      trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
    })
    const commands = document.querySelector<HTMLElement>('[data-testid="chat-command-menu-button"]')!
    expect(commands.textContent).toContain('Slash commands')
    await act(async () => {
      commands.focus()
      commands.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
    })
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 20)) })
    const input = host.querySelector<HTMLTextAreaElement>('[data-testid="chat-input-textarea"]')!
    expect(input.value).toBe('review this file')
    expect(document.activeElement).toBe(input)
    expect(document.querySelector('[role="menu"]')).toBeNull()
    expect(useChatStore.getState().getTabConfig('A')?.sendDeliveryMode).toBe('queue')
    const command = [...document.querySelectorAll<HTMLElement>('[role="option"]')].find(node => node.textContent?.includes('/menu-review'))!
    expect(command).toBeTruthy()
    await act(async () => command.click())
    expect(send).toHaveBeenCalledWith('review via menu', expect.objectContaining({ sourceTabId: 'A' }))
  })
  it('keeps chat and background actions inside the add menu and stops only the selected work', async () => {
    let items = [
      { id: 'task-1', kind: 'sub_agent' as const, label: 'code review', started_at: new Date().toISOString(), can_stop: true, status: 'Reading files' },
      { id: 'task-2', kind: 'step' as const, label: 'refresh', started_at: new Date().toISOString(), can_stop: true },
    ]
    vi.spyOn(agentApi, 'getSessionBackgroundWork').mockImplementation(async () => ({ session_id: 'A-session', items }))
    const stopItem = vi.spyOn(agentApi, 'stopSessionBackgroundWork').mockImplementation(async (_sessionId, itemId) => { items = items.filter(item => item.id !== itemId) })
    const scroll = vi.fn()
    const card = document.createElement('div')
    card.dataset.backgroundAgentId = 'task-1'
    card.scrollIntoView = scroll
    document.body.append(card)
    try {
      await act(async () => {
        useChatStore.getState().setTabViewMode('A', 'formatted')
        useChatStore.getState().setTabStreaming('A', true)
        useChatStore.getState().setTabHasRunningBgAgents('A', true)
        renderComposer(undefined, false)
      })
      expect(host.querySelector('[data-testid="chat-background-activity"]')).toBeTruthy()
      expect(host.querySelector('[data-testid="background-work-pill"]')).toBeNull()
      expect(document.querySelector('[data-testid="chat-new-chat"]')).toBeNull()
      // Stop sits in plain sight while the turn runs (owner pressed Escape looking for it inside the menu).
      expect(host.querySelector('[data-testid="chat-stop-button"]')).toBeTruthy()
      const openMenu = async () => act(async () => {
        const trigger = host.querySelector<HTMLElement>('[aria-label="Add files and actions"]')!
        trigger.focus(); trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
      })
      const openTasks = async (count: number) => act(async () => {
        const tasks = [...document.querySelectorAll<HTMLElement>('[role="menuitem"]')].find(item => item.textContent === `${count} running`)!
        expect(tasks).toBeTruthy()
        tasks.focus(); tasks.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true }))
      })
      const select = async (element: HTMLElement) => act(async () => {
        element.focus(); element.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
      })
      await openMenu()
      await openTasks(2)
      expect(document.querySelector('[data-testid="background-work-status"]')?.textContent).toBe('Reading files')
      await select(document.querySelector<HTMLElement>('[aria-label="Open Sub-agent: code review"]')!)
      expect(scroll).toHaveBeenCalled()
      await openMenu()
      await openTasks(2)
      await select(document.querySelector<HTMLElement>('[aria-label="Stop Sub-agent: code review"]')!)
      expect(stopItem).toHaveBeenCalledExactlyOnceWith('A-session', 'task-1')
      expect(stopTurn).not.toHaveBeenCalled()
      expect(document.querySelectorAll('[data-testid="background-work-item"]')).toHaveLength(1)
      await act(async () => {
        const remaining = document.querySelector<HTMLElement>('[aria-label="Stop Step: refresh"]')!
        remaining.focus(); remaining.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowLeft', bubbles: true }))
      })
      expect(document.querySelector('[role="menu"] [data-testid="chat-stop-button"]')).toBeNull()
      await act(async () => {
        const item = document.querySelector<HTMLElement>('[role="menu"] [role="menuitem"]')!
        item.focus(); item.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
      })
      expect(document.querySelector('[role="menu"]')).toBeNull()
      await act(async () => host.querySelector<HTMLElement>('[data-testid="chat-stop-button"]')!.click())
      expect(stopTurn).toHaveBeenCalledOnce()
      expect(stopItem).toHaveBeenCalledTimes(1)
      expect(host.querySelector('[data-testid="chat-stop-button"]')).toBeNull()
      await openMenu()
      await openTasks(1)
      expect(document.querySelector('[data-testid="background-work-item"]')?.textContent).toContain('Step: refresh')
      await act(async () => {
        const remaining = document.querySelector<HTMLElement>('[aria-label="Stop Step: refresh"]')!
        remaining.focus(); remaining.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowLeft', bubbles: true }))
      })
      await select(document.querySelector<HTMLElement>('[data-testid="chat-new-chat"]')!)
      await act(async () => { await new Promise(resolve => setTimeout(resolve, 20)) })
      expect(newChat).toHaveBeenCalledOnce()
      expect(document.querySelector('[role="menu"]')).toBeNull()
      expect(send).not.toHaveBeenCalled()
      expect(useChatStore.getState().getTabConfig('A')?.inputText).toBe('review this file')
    } finally { card.remove() }
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


  it('uses the Relay catalog for both command picker and typed commands, excluding Autopilot', async () => {
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
    // Autopilot may be sent as ordinary user text, but cannot execute its API action.
    expect(document.body.textContent).not.toContain('Run one complete Autopilot')
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

  it('runs an active product command without opening the composer or consuming its saved draft', async () => {
    await act(async () => setProductCommands([{ command: 'design-plan', description: 'Review', icon: 'terminal',
      source: 'product', modes: ['workflow'], execute: ctx => ctx.onSubmit('focused review') }]))
    await act(async () => button('Browse commands').click())
    const command = [...host.querySelectorAll<HTMLElement>('[role="option"]')].find(node => node.textContent?.includes('/design-plan'))!
    await act(async () => command.click())
    expect(composer().hidden).toBe(true)
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
