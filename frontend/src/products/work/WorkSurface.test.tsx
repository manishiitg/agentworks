// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { normalizeWorkWorkspaceView, WorkChatTabs } from './WorkSurface'
import { useChatStore, type ChatTab } from '../../stores/useChatStore'
import { agentApi } from '../../services/api'
import { isWorkWorkspaceViewEnabled } from './workViewGating'
import { useProductNavigationToolbarStore } from '../../stores/useProductNavigationToolbarStore'
import { routeView } from '../../utils/productRoutes'

vi.mock('../../utils/chatDeliveryTelemetry', async original => ({ ...await original<typeof import('../../utils/chatDeliveryTelemetry')>(), recordTabReorderTelemetry: vi.fn() }))

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => useProductNavigationToolbarStore.setState({ sidebarWidth: 0 }))
// RunsOnPicker loads the provider manifest in an effect. This runner has no working
// localStorage, so the real store's late write became an unhandled rejection after the
// test ended. A store with the manifest already loaded starts no request at all.
const llmState = vi.hoisted(() => ({ providerManifest: [], providerManifestLoaded: true, loadProviderManifest: vi.fn(), setShowLLMModal: vi.fn() }))
vi.mock('../../stores/useLLMStore', () => ({
  useLLMStore: Object.assign((selector: (state: typeof llmState) => unknown) => selector(llmState), { getState: () => llmState }),
}))

describe('fresh deep links keep their view', () => {
  it('does not resolve a phantom Secrets view to project identity', () => {
    expect(normalizeWorkWorkspaceView('secrets')).toBeNull()
  })
  afterEach(() => { window.history.pushState(null, '', '/') })

  it('resolves a Crew schedules link instead of the identity default', () => {
    window.history.pushState(null, '', '/crew/projects/example?view=schedules')
    expect(routeView('work', 'example', normalizeWorkWorkspaceView, 'identity')).toBe('schedules')
  })

  it('keeps a route view only while its panel is enabled', () => {
    expect(isWorkWorkspaceViewEnabled('schedules', new Set(['schedules']))).toBe(true)
    expect(isWorkWorkspaceViewEnabled('schedules', new Set(['files']))).toBe(false)
    expect(isWorkWorkspaceViewEnabled('costs', new Set(['files']))).toBe(false)
  })

  it('resolves Crew shell and costs links instead of Files', () => {
    window.history.pushState(null, '', '/crew/projects/example?view=shell')
    expect(routeView('work', 'example', normalizeWorkWorkspaceView, 'files')).toBe('shell')
    window.history.pushState(null, '', '/crew/projects/example?view=costs')
    expect(routeView('work', 'example', normalizeWorkWorkspaceView, 'files')).toBe('costs')
  })
})

describe('Crew native tab dragging', () => {
  it('names another owner tab and leaves only pinning to the caller', async () => {
    const owner = { agentProfileTabOwnerID: 'other', agentProfileTabOwnerName: 'alex.taylor@example.com', agentProfileTabCanWrite: false }
    const make = (id: string, metadata: Record<string, unknown>) => ({ tabId: id, sessionId: 'session-' + id, name: id, createdAt: 1, config: {}, metadata: { agentProfileId: 'work', agentProfileProjectId: 'crew', agentProfileConversationKey: 'crew:chat:' + id, ...metadata } } as ChatTab)
    useChatStore.setState({ activeTabId: 'main', chatTabs: {
      main: make('main', { agentProfileIsMainChat: true, agentProfileTabOwnerID: 'me', agentProfileTabOwnerName: 'me@example.com', agentProfileTabCanWrite: true }),
      research: make('research', owner), review: make('review', owner),
    } })
    const list = vi.spyOn(agentApi, 'listAgentProfileSideChats').mockResolvedValue([
      { conversation_key: 'crew:chat:research', session_id: 'session-research', name: 'Research', tab_owner_id: 'other', tab_owner_name: 'alex.taylor@example.com', can_write: false },
      { conversation_key: 'crew:chat:review', session_id: 'session-review', name: 'Review', tab_owner_id: 'other', tab_owner_name: 'alex.taylor@example.com', can_write: false },
    ])
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<WorkChatTabs projectId="crew" projectName="Crew" canonicalTabId="main" profileId="work" allowSideChats />))
      expect(host.querySelectorAll('[data-chat-owner-label]')).toHaveLength(0)
      expect(host.querySelector('[data-project-title]')?.getAttribute('title')).toBe('Crew')
      expect(host.querySelector('[data-chat-tab-id="research"] [role="button"]')?.getAttribute('aria-label')).toContain('alex.taylor@example.com')
      expect(host.textContent).not.toContain('me@example.com')
      await act(async () => host.querySelector<HTMLButtonElement>('[data-testid="work-more-tabs"]')!.click())
      const menu = document.querySelector('[role="dialog"][aria-label="Agents"]')!
      expect(menu.querySelectorAll('[data-chat-owner-label]')).toHaveLength(0)
      expect(menu.querySelector('button[title="Research · alex.taylor · alex.taylor@example.com"]')).not.toBeNull()
      // Another owner's tab can be pinned on the caller's own strip.
      expect(menu.querySelector('[aria-label="Unpin Research · alex.taylor"]')).not.toBeNull()
      expect(menu.querySelector('[aria-label="Rename Research"]')).toBeNull()
      expect(menu.querySelector('[aria-label="Close Review"]')).toBeNull()
    } finally { await act(async () => root.unmount()); host.remove(); list.mockRestore() }
  })

  it('shows exactly the server list in its order, whatever this browser remembered about a tab', async () => {
    // PLAT-746-230: opening the sidebar must not hide the project's header tabs.
    useProductNavigationToolbarStore.setState({ sidebarWidth: 260 })
    const make = (id: string, extra: Record<string, unknown> = {}) => ({ tabId: id, sessionId: `session-${id}`, name: id, createdAt: 900, config: {}, metadata: { agentProfileId: 'work', agentProfileProjectId: 'crew', agentProfileIsMainChat: id === 'main', agentProfileConversationKey: id === 'main' ? 'crew:chat:root-main' : `crew:chat:${id}`, ...extra } } as ChatTab)
    useChatStore.setState({ chatTabs: {
      main: make('main'),
      // Remembered from before the project moved from Code to Crew: still listed by the server, so it shows.
      a: make('a', { agentProfileId: 'code', agentProfileConversationKey: undefined }),
      // PLAT-746-224: a persisted activity copy must neither show nor steal the server chat binding.
      duplicate: { ...make('duplicate', { isViewOnly: true, agentProfileProjectId: undefined }), sessionId: 'session-b' },
      b: make('b'), z: make('z'),
      // A tab the server no longer lists is not shown.
      gone: make('gone'),
      bot: make('bot', { isViewOnly: true, isBotRun: true }), schedule: make('schedule', { isViewOnly: true, isScheduledRun: true }),
    }, activeTabId: 'duplicate' })
    const list = vi.spyOn(agentApi, 'listAgentProfileSideChats').mockResolvedValue([
      { conversation_key: 'crew:chat:b', session_id: 'session-b', name: 'b', position: 1, pinned: true },
      { conversation_key: 'crew:chat:a', session_id: 'session-a', name: 'a', pinned: true },
      { conversation_key: 'crew:chat:z', session_id: 'session-z', name: 'z', pinned: false },
    ])
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<WorkChatTabs projectId="crew" projectName="Crew" canonicalTabId="main" profileId="work" allowSideChats />))
      const ids = (selector: string, attribute: string, scope: ParentNode = host) => [...scope.querySelectorAll(selector)].map(node => node.getAttribute(attribute))
      expect(ids('[data-chat-tab-id]', 'data-chat-tab-id')).toEqual(['main', 'b', 'a', 'bot', 'schedule'])
      expect(useChatStore.getState().activeTabId).toBe('b')
      expect(useChatStore.getState().chatTabs.duplicate).toBeUndefined()
      expect(Object.values(useChatStore.getState().chatTabs).find(tab => tab.sessionId === 'session-b')?.tabId).toBe('b')
      const menu = host.querySelector<HTMLButtonElement>('[data-testid="work-more-tabs"]')!
      expect(menu.textContent).toBe('')
      expect(menu.getAttribute('aria-label')).toBe('Agents: switch, reorder or close (4)')
      await act(async () => menu.click())
      expect(ids('[data-chat-list-id]', 'data-chat-list-id', document)).toEqual(['main', 'b', 'a', 'z', 'bot', 'schedule'])
      expect(document.querySelector('[aria-label="Agents"] [title="Close activity view"]')).not.toBeNull()
      expect(document.querySelector('[aria-label="Agents"] [aria-label="Close Crew"]')).toBeNull()
    } finally { await act(async () => root.unmount()); host.remove(); list.mockRestore() }
  })

  it.each(['a', 'b'])('keeps a drag source and drop target working while %s is selected', async active => {
    const make = (id: string, position: number) => ({ tabId: id, sessionId: `session-${id}`, name: id, createdAt: position, config: {}, metadata: { agentProfileId: 'work', agentProfileProjectId: 'crew', agentProfileIsMainChat: id === 'main', agentProfileConversationKey: id === 'main' ? 'crew' : `crew:chat:${id}`, agentProfileTabPosition: position } } as ChatTab)
    useChatStore.setState({ chatTabs: { main: make('main', 0), a: make('a', 1), b: make('b', 2) }, activeTabId: active })
    const list = vi.spyOn(agentApi, 'listAgentProfileSideChats').mockResolvedValue([{ conversation_key: 'crew:chat:a', session_id: 'session-a', name: 'a', position: 1 }, { conversation_key: 'crew:chat:b', session_id: 'session-b', name: 'b', position: 2 }])
    const order = vi.spyOn(agentApi, 'orderAgentProfileSideChats').mockResolvedValue([{ conversation_key: 'crew:chat:b', session_id: 'session-b', name: 'b', position: 1 }, { conversation_key: 'crew:chat:a', session_id: 'session-a', name: 'a', position: 2 }])
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<WorkChatTabs projectId="crew" projectName="Crew" canonicalTabId="main" profileId="work" allowSideChats />))
      const strip = host.querySelector('[data-testid="work-chat-tab-strip"]')!
      expect(strip.querySelector('.lucide-grip-vertical')).toBeNull()
      expect(strip.querySelector('[aria-label="Close a"]')).toBeNull()
      expect(strip.querySelector('[aria-label="Unpin a"]')).toBeNull()
      expect(strip.querySelector('[title="Make this the main chat"]')).toBeNull()
      const dataTransfer = { setData: vi.fn(), effectAllowed: '', dropEffect: '' }
      const gesture = (kind: string, node: Element) => { const event = new Event(kind, { bubbles: true, cancelable: true }); Object.defineProperty(event, 'dataTransfer', { value: dataTransfer }); node.dispatchEvent(event) }
      await act(async () => gesture('dragstart', host.querySelector('[data-chat-tab-id="a"]')!))
      expect(dataTransfer.setData).toHaveBeenCalledWith('application/x-agentworks-chat-key', 'crew:chat:a')
      await act(async () => gesture('dragover', host.querySelector('[data-chat-tab-id="b"]')!))
      await act(async () => gesture('drop', host.querySelector('[data-chat-tab-id="b"]')!))
      expect(order).toHaveBeenCalledWith('work', 'crew', ['crew:chat:b', 'crew:chat:a'])
      const menu = host.querySelector<HTMLButtonElement>('[data-testid="work-more-tabs"]')!
      expect(menu.textContent).toBe('')
      expect(menu.getAttribute('aria-label')).toBe('Agents: switch, reorder or close (3)')
      await act(async () => menu.click())
      const popover = document.querySelector('[role="dialog"][aria-label="Agents"]')!
      expect(popover.textContent).toContain('a')
      expect(popover.querySelector('[aria-label="Unpin a"]')).not.toBeNull()
      expect(popover.querySelector('[aria-label="Move a earlier"]')).toBeNull()
      expect([...popover.querySelectorAll('button')].some(button => button.textContent === 'Reorder')).toBe(false)
      expect(popover.querySelector('[aria-label="Close a"]')).not.toBeNull()
    } finally { await act(async () => root.unmount()); host.remove(); list.mockRestore(); order.mockRestore() }
  })

  it.each([5, 6])('offers menu reordering only above five tabs (%s tabs)', async count => {
    const make = (id: string) => ({ tabId: id, sessionId: `session-${id}`, name: id, createdAt: 1, config: {}, metadata: { agentProfileId: 'work', agentProfileProjectId: 'crew', agentProfileIsMainChat: id === 'main', agentProfileConversationKey: id === 'main' ? 'crew' : `crew:chat:${id}` } } as ChatTab)
    const ids = ['main', ...Array.from({ length: count - 1 }, (_, index) => `agent-${index}`)]
    const listed = ids.slice(1).map((id, index) => ({ conversation_key: `crew:chat:${id}`, session_id: `session-${id}`, name: id, position: index + 1 }))
    useChatStore.setState({ chatTabs: Object.fromEntries(ids.map(id => [id, make(id)])), activeTabId: 'main' })
    const list = vi.spyOn(agentApi, 'listAgentProfileSideChats').mockResolvedValue(listed)
    const order = vi.spyOn(agentApi, 'orderAgentProfileSideChats').mockImplementation(async (_profile, _project, keys) => keys.map(key => listed.find(chat => chat.conversation_key === key)!))
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<WorkChatTabs projectId="crew" projectName="Crew" canonicalTabId="main" profileId="work" allowSideChats />))
      await act(async () => host.querySelector<HTMLButtonElement>('[data-testid="work-more-tabs"]')!.click())
      const menu = document.querySelector('[role="dialog"][aria-label="Agents"]')!
      const reorder = [...menu.querySelectorAll('button')].find(button => button.textContent === 'Reorder')
      expect(Boolean(reorder)).toBe(count > 5)
      expect(menu.querySelector('.lucide-grip-vertical')).toBeNull()
      if (reorder) {
        await act(async () => reorder.click())
        const earlier = menu.querySelector<HTMLButtonElement>('[aria-label="Move agent-1 earlier"]')!
        expect(earlier).not.toBeNull()
        expect(menu.querySelector<HTMLButtonElement>('[aria-label="Move agent-0 earlier"]')!.disabled).toBe(true)
        await act(async () => earlier.click())
        expect(order).toHaveBeenCalledWith('work', 'crew', ['crew:chat:agent-1', 'crew:chat:agent-0', ...listed.slice(2).map(chat => chat.conversation_key)])
        await act(async () => [...menu.querySelectorAll('button')].find(button => button.textContent === 'Done')!.click())
        expect(menu.querySelector('[aria-label="Rename agent-0"]')).not.toBeNull()
        expect(menu.querySelector('[aria-label="Move agent-0 earlier"]')).toBeNull()
      }
    } finally { await act(async () => root.unmount()); host.remove(); list.mockRestore(); order.mockRestore() }
  })
})
