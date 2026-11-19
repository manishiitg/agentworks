// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { ChatInput } from './ChatInput'
import { useChatStore, type ChatTab } from '../stores/useChatStore'
import { useLLMStore } from '../stores/useLLMStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useModeStore } from '../stores/useModeStore'
import { agentApi, authApi, workflowManifestApi } from '../services/api'
import * as productProjects from '../platform/chat/productProjects'
import { configureChatDeliveryTelemetryTransport } from '../utils/chatDeliveryTelemetry'

vi.mock('../commands/user-commands', () => ({ loadAndRegisterUserCommands: vi.fn().mockResolvedValue(undefined) }))

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
configureChatDeliveryTelemetryTransport(async () => {})

const tab: ChatTab = {
  tabId: 'chat', name: 'Chat', sessionId: 'chat-session', viewMode: 'formatted',
  isStreaming: false, isCompleted: false, hasRunningBgAgents: false, isSyntheticTurn: false,
  canSteer: false, hideToolCalls: false, createdAt: 0, lastViewedEventCount: 0, lastViewedEventCounts: { micro: 0 },
  metadata: { mode: 'workflow', phaseId: 'workflow-builder', workshopMode: 'workshop' },
  config: {
    inputText: '', useCodeExecutionMode: false, selectedServers: [], selectedSkills: [],
    fileContext: [], workflowContext: [], queuedMessages: [], browserMode: 'none',
    llmConfig: { provider: 'claude-code', model_id: 'claude-code' },
  },
}
let host: HTMLDivElement
let root: Root
let client: QueryClient
let input: HTMLTextAreaElement
const send = vi.fn(async () => true)
const stop = vi.fn()
const list = () => host.querySelector('[role="listbox"][aria-label="References"]')
const workflow = { workspace_path: 'Workflow/launch', manifest: { id: 'launch', label: 'Launch plan' } }
const workflowList = { success: true, total: 1, workflows: [workflow] } as Awaited<ReturnType<typeof workflowManifestApi.listWorkflowManifests>>

beforeEach(async () => {
  useModeStore.setState({ selectedModeCategory: 'workflow' })
  useLLMStore.setState({ providerManifestLoaded: true, llmConfigLocked: false })
  useChatStore.setState({ activeTabId: tab.tabId, chatTabs: { [tab.tabId]: tab }, activeSessionsCache: [], tabEvents: {} })
  useWorkspaceStore.setState({ activeFolder: 'Workflow/project' })
  vi.spyOn(useWorkspaceStore.getState(), 'fetchFiles').mockResolvedValue(undefined)
  vi.spyOn(agentApi, 'getChatQueueState').mockResolvedValue({ messages: [] })
  vi.spyOn(agentApi, 'listTerminals').mockResolvedValue({ terminals: [] } as never)
  vi.spyOn(authApi, 'getMyTokenUsage').mockRejectedValue(new Error('No shared account in this chat'))
  vi.spyOn(productProjects, 'loadProductProjects').mockResolvedValue([])
  vi.spyOn(workflowManifestApi, 'listWorkflowManifests').mockResolvedValue(workflowList)
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  await act(async () => root.render(<QueryClientProvider client={client}>
    <ChatInput tabId={tab.tabId} onSubmit={send} onStopStreaming={stop} hideRuntimeStatus />
  </QueryClientProvider>))
  input = host.querySelector<HTMLTextAreaElement>('[data-chat-composer]')!
})

afterEach(async () => {
  await act(async () => root.unmount())
  client.clear()
  host.remove()
  vi.restoreAllMocks()
  send.mockClear()
  stop.mockClear()
})

function edit(text: string, caret = text.length) {
  return act(async () => {
    input.focus()
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(input, text)
    input.setSelectionRange(caret, caret)
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
}

function press(key: string, options: KeyboardEventInit = {}) {
  return act(async () => input.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...options })))
}

it('keeps the draft focused and sends literal # text even when a reference matches or has not loaded', async () => {
  vi.mocked(workflowManifestApi.listWorkflowManifests).mockReturnValueOnce(new Promise(() => {}))
  await edit('Check #3304')
  expect(list()).not.toBeNull()
  expect(document.activeElement).toBe(input)
  await press('Enter')
  expect(send).toHaveBeenLastCalledWith('Check #3304', expect.any(Object))
  expect(list()).toBeNull()

  await edit('#')
  expect(list()).not.toBeNull()
  await edit('# ')
  expect(list()).toBeNull()
  await edit('# Heading')
  expect(list()).toBeNull()
  await press('Enter')
  expect(send).toHaveBeenLastCalledWith('# Heading', expect.any(Object))

  await edit('Check #launch')
  expect(list()?.querySelector('[role="option"]')).not.toBeNull()
  expect(input.hasAttribute('aria-activedescendant')).toBe(false)
  await press('Enter')
  expect(send).toHaveBeenLastCalledWith('Check #launch', expect.any(Object))
  expect(useChatStore.getState().getTabConfig(tab.tabId)?.workflowContext).toEqual([])

  await edit('Check #nothing-matches')
  expect(list()).toBeNull()
  expect(input.value).toBe('Check #nothing-matches')
})

it('dismisses without editing and only adds a reference after arrow selection or a click', async () => {
  await edit('See #lau')
  await press('Escape')
  expect(list()).toBeNull()
  expect(input.value).toBe('See #lau')
  expect(stop).not.toHaveBeenCalled()
  await edit('See #launch')
  expect(list()).toBeNull()

  await edit('See #launch please')
  await edit('See #launcher please', 'See #lau'.length)
  const option = list()?.querySelector<HTMLElement>('[role="option"]')
  expect(option).toBeDefined()
  await press('ArrowDown')
  expect(input.getAttribute('aria-activedescendant')).toBe(option?.id)
  await press('Enter')
  expect(input.value).toBe('See #workflow:Launch plan please')
  expect(document.activeElement).toBe(input)
  expect(send).not.toHaveBeenCalled()
  expect(useChatStore.getState().getTabConfig(tab.tabId)?.workflowContext).toEqual([
    { presetId: 'workflow:launch', label: 'Launch plan', workspacePath: 'Workflow/launch' },
  ])

  await edit('And #lau')
  const clickable = list()?.querySelector<HTMLElement>('[role="option"]')
  expect(clickable).toBeDefined()
  await act(async () => {
    clickable!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true }))
    clickable!.click()
  })
  expect(input.value).toBe('And #workflow:Launch plan ')
  expect(send).not.toHaveBeenCalled()
  await edit('And #')
  await edit('And ') // Backspace in the textarea removes # normally.
  expect(list()).toBeNull()
  expect(useChatStore.getState().getTabConfig(tab.tabId)?.inputText).toBe('And ')
})
