// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({ chat: {} as Record<string, any>, activate: vi.fn() }))
vi.mock('../stores/useChatStore', () => ({ useChatStore: { getState: () => mocks.chat } }))
vi.mock('../stores/useGlobalPresetStore', () => ({ useGlobalPresetStore: { getState: () => ({ workflowPresets: [{ id: 'one', selectedFolder: { filepath: 'Workflow/one' } }], refreshPresets: vi.fn() }) } }))
vi.mock('../stores/useWorkflowStore', () => ({ useWorkflowStore: { getState: () => ({ setShowChatArea: vi.fn(), setShowWorkspacePane: vi.fn(), setFocusedPane: vi.fn() }) } }))
vi.mock('./workflowNavigation', () => ({ selectWorkflowPreset: () => true }))
vi.mock('./activateTab', () => ({ activateTab: mocks.activate }))
import { CHAT_SCROLL_TO_BOTTOM_EVENT, SettledScroll, requestChatScrollToBottom } from './chatScrollRequest'
import { sendWorkspacePaneMessageToChat } from './workspacePaneChat'
import { transcriptReadingState } from '../components/useTranscriptScroll'

let events = 0
const count = () => { events++ }
beforeEach(() => {
  vi.useFakeTimers()
  events = 0
  window.addEventListener(CHAT_SCROLL_TO_BOTTOM_EVENT, count)
  mocks.chat = {
    chatTabs: { here: { tabId: 'here', isStreaming: false, metadata: { mode: 'workflow', presetQueryId: 'one' } } },
    activeTabId: 'here', autoScroll: true,
    getTab: (id: string) => mocks.chat.chatTabs[id],
    getActiveSessions: vi.fn(async () => []),
    getTabConfig: vi.fn(() => ({ queuedMessages: [] })),
    setTabConfig: vi.fn(), setTabViewMode: vi.fn(), setAutoScroll: vi.fn(),
  }
})
afterEach(() => { window.removeEventListener(CHAT_SCROLL_TO_BOTTOM_EVENT, count); vi.useRealTimers() })

describe('SettledScroll', () => {
  it('scrolls once after the list goes quiet, not at each change', () => {
    const perform = vi.fn()
    const settled = new SettledScroll(perform, 100, 800)
    settled.request()
    for (let i = 0; i < 20; i++) { vi.advanceTimersByTime(15); settled.touch() }
    expect(perform).not.toHaveBeenCalled()
    vi.advanceTimersByTime(100)
    expect(perform).toHaveBeenCalledTimes(1)
    vi.advanceTimersByTime(2000)
    expect(perform).toHaveBeenCalledTimes(1)
  })

  it('coalesces many requests inside the window into one scroll', () => {
    const perform = vi.fn()
    const settled = new SettledScroll(perform, 100, 800)
    for (let i = 0; i < 6; i++) { settled.request(); vi.advanceTimersByTime(30) }
    vi.advanceTimersByTime(1000)
    expect(perform).toHaveBeenCalledTimes(1)
  })

  it('never waits longer than the cap on a list that keeps changing', () => {
    const perform = vi.fn()
    const settled = new SettledScroll(perform, 100, 800)
    settled.request()
    for (let i = 0; i < 100; i++) { vi.advanceTimersByTime(50); settled.touch() }
    expect(perform).toHaveBeenCalledTimes(1)
  })

  it('drops the scroll when the user has scrolled since the request', () => {
    const perform = vi.fn()
    const settled = new SettledScroll(perform, 100, 800)
    let manual = 0
    const version = manual
    settled.request(() => manual === version)
    manual++
    vi.advanceTimersByTime(1000)
    expect(perform).not.toHaveBeenCalled()
    settled.request()
    settled.cancel()
    vi.advanceTimersByTime(1000)
    expect(perform).not.toHaveBeenCalled()
    expect(settled.pending).toBe(false)
  })
})

describe('requestChatScrollToBottom', () => {
  it('dispatches one event and no timed repeats', () => {
    requestChatScrollToBottom()
    vi.advanceTimersByTime(2000)
    expect(events).toBe(1)
    expect(mocks.chat.setAutoScroll).toHaveBeenCalledWith(true)
  })
})

describe('pane message scroll intent', () => {
  it('keeps the reading position in the chat already on screen when scrolled up', async () => {
    transcriptReadingState('here').following = false
    await sendWorkspacePaneMessageToChat({ workspacePath: 'Workflow/one', message: 'Apply it' })
    vi.advanceTimersByTime(2000)
    expect(events).toBe(0)
    expect(mocks.chat.setAutoScroll).not.toHaveBeenCalled()
    transcriptReadingState('here').following = true
  })

  it('lands at the bottom once when the chat on screen is at the bottom', async () => {
    await sendWorkspacePaneMessageToChat({ workspacePath: 'Workflow/one', message: 'Apply it' })
    vi.advanceTimersByTime(2000)
    expect(events).toBe(1)
  })

  it('lands at the bottom when the message goes to another chat', async () => {
    mocks.chat.activeTabId = 'elsewhere'
    transcriptReadingState('here').following = false
    await sendWorkspacePaneMessageToChat({ workspacePath: 'Workflow/one', message: 'Apply it' })
    vi.advanceTimersByTime(2000)
    expect(events).toBe(1)
    expect(mocks.chat.setAutoScroll).toHaveBeenCalledWith(true)
    transcriptReadingState('here').following = true
  })
})
