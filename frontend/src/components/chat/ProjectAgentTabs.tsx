import { createPortal } from 'react-dom'
import { useContext, useEffect, useLayoutEffect, useRef, useState, type DragEvent as ReactDragEvent, type ReactNode } from 'react'
import { ArrowDown, ArrowUp, Check, ChevronDown, GripVertical, Loader2, Pencil, Pin, PinOff, Plus, X } from 'lucide-react'
import type { AgentProfileSideChat } from '../../services/api-types'
import { useChatStore, type ChatTab } from '../../stores/useChatStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import type { ProductSurface } from '../../products/productSurfaceConfig'
import { workChatTabShortcut, WORK_SIDE_CHAT_LIMIT } from '../../products/work/workTabs'
import { workSideChatDisplayName } from '../../products/work/workProjectPresentation'
import { markFeatureUsed } from '../../utils/featureUsage'
import { recordTabReorderTelemetry } from '../../utils/chatDeliveryTelemetry'
import { AgentWorksChatTabItem } from './AgentWorksChatTabItem'
import { compactOwnerChatName, ownerShortName } from './chatTabName'
import { ProjectWorkspaceTitle } from './ProjectWorkspaceTitle'
import { MobileWorkspaceNavigationContext } from '../workspace/MobileWorkspaceNavigation'
import { useChatRuntimeActivity } from '../../hooks/useChatRuntimeActivity'
import { runtimeTurnFailedUnseen } from '../../utils/runtimeActivity'
import { useMobileViewport } from '../../hooks/useMobileViewport'

type AgentMenuState = 'waiting' | 'running' | 'failed' | 'done' | null
const AGENT_STATE_WORD = { waiting: 'Needs you', running: 'Working', failed: 'Stopped with an error', done: 'Finished' } as const

function useAgentMenuState(tabId: string, workspaceActivity?: 'running' | 'waiting' | null): AgentMenuState {
  const activity = useChatRuntimeActivity(tabId)
  const failed = useChatStore(state => {
    const tab = state.chatTabs[tabId]
    return !!tab?.sessionId && state.activeTabId !== tabId && runtimeTurnFailedUnseen(state.activeSessionsCache.find(session => session.session_id === tab.sessionId), tab.lastAccessedAt)
  })
  const done = useChatStore(state => state.activeTabId !== tabId && !!state.chatTabs[tabId]?.hasUnreadCompletion)
  if (activity.state === 'waiting' || workspaceActivity === 'waiting') return 'waiting'
  if (activity.state === 'running' || workspaceActivity === 'running') return 'running'
  return failed ? 'failed' : done ? 'done' : null
}

function AgentMenuActivity({ tabId, workspaceActivity }: { tabId: string; workspaceActivity?: 'running' | 'waiting' | null }) {
  const state = useAgentMenuState(tabId, workspaceActivity)
  if (state === 'waiting') return <span role="img" aria-label="Needs you" title="Needs you" className="h-2 w-2 shrink-0 rounded-full bg-[hsl(var(--warning))] motion-safe:animate-pulse" />
  if (state === 'running') return <Loader2 role="img" aria-label="Working" className="h-3 w-3 shrink-0 animate-spin text-[hsl(var(--info))]" />
  if (state === 'failed') return <span role="img" aria-label="Failed" title="Failed" className="h-2 w-2 shrink-0 rounded-full bg-destructive" />
  return null
}

function AgentMenuStateWord({ tabId, workspaceActivity }: { tabId: string; workspaceActivity?: 'running' | 'waiting' | null }) {
  const state = useAgentMenuState(tabId, workspaceActivity)
  return state ? <span className="ml-auto shrink-0 pl-2 text-[10px] font-normal text-muted-foreground">{AGENT_STATE_WORD[state]}</span> : null
}

/** One tab-management view; each product supplies its existing server operations. */
export function ProjectAgentTabs({ tabs, listed, canonicalTabId, projectName, allowSideChats, opening, surface,
  onOrder, onPin, onRename, onOpen, onCloseTab, onSelectTab, projectActivity, renderAfterTab, isTabActive, onMakeInteractive,
}: {
  tabs: ChatTab[]; listed: AgentProfileSideChat[]; canonicalTabId: string; projectName: string
  allowSideChats: boolean; opening: boolean; surface: ProductSurface
  onOrder: (keys: string[]) => Promise<void>; onPin: (key: string, pinned: boolean) => Promise<void>
  onRename: (tab: ChatTab, name: string) => Promise<boolean | void>; onOpen: (name: string) => Promise<boolean | void>
  onCloseTab: (tabId: string) => void; onSelectTab: (tabId: string) => void
  projectActivity?: 'running' | 'waiting' | null
  renderAfterTab?: (tab: ChatTab) => ReactNode; isTabActive?: (tab: ChatTab) => boolean; onMakeInteractive?: (tabId: string) => void
}) {
  const activeTabId = useChatStore(state => state.activeTabId)
  const canonicalSessionId = useChatStore(state => state.chatTabs[canonicalTabId]?.sessionId)
  const [newTabName, setNewTabName] = useState<string | null>(null)
  const listedFor = (tab: ChatTab) => listed.find(chat => tab.metadata?.agentProfileConversationKey === chat.conversation_key || tab.sessionId === chat.session_id)
  const ownedListed = listed.filter(chat => chat.can_write !== false)
  const sideChats = tabs.filter(tab => tab.tabId !== canonicalTabId && !tab.metadata?.isViewOnly)
  const phone = useMobileViewport()
  const phoneNavigation = useContext(MobileWorkspaceNavigationContext)
  const selectTab = (tabId: string) => { onSelectTab(tabId); phoneNavigation?.onChange('chat') }
  const closeProjectTab = onCloseTab
  const renameSideChat = onRename
  const submitNewAgent = async (requested: string) => {
    const name = requested.trim()
    if (!name || opening) return
    try { if (await onOpen(name) !== false) { setNewTabName(null); phoneNavigation?.onChange('chat') } }
    catch (cause) { useChatStore.getState().addToast(cause instanceof Error ? cause.message : 'Could not open a new agent.', 'error') }
  }
  const isAgentTab = (tab: ChatTab) => !tab.metadata?.isViewOnly && !tab.metadata?.isScheduledRun && !tab.metadata?.isBotRun
  const agentCount = tabs.filter(isAgentTab).length
  const sideChatKey = (tab: ChatTab) => listedFor(tab)?.conversation_key
  const sideChatName = (tab: ChatTab) => workSideChatDisplayName(listedFor(tab)?.name || tab.name)
  const canControl = (tab: ChatTab) => tab.metadata?.agentProfileTabCanWrite !== false
  // Another person's tab names that person, so two owners' main chats never look alike.
  const tabLabel = (tab: ChatTab) => {
    const owner = tab.metadata?.agentProfileTabOwnerName
    const name = compactOwnerChatName(tab.tabId === canonicalTabId ? 'Main' : sideChatName(tab), owner)
    return owner && !canControl(tab) ? `${name} · ${ownerShortName(owner)}` : name
  }
  const sideChatPinned = (tab: ChatTab) => listedFor(tab)?.pinned !== false
  // The strip shows the main chat and the tabs the person pinned, as many as they pin (owner, 2026-10-20); the rest are
  // in the Agents menu. The tab the person is in is always on the strip (in the last place when it is not pinned), so it
  // can be seen and dragged.
  const onStrip = (tab: ChatTab) => tab.tabId === canonicalTabId || !listedFor(tab) || sideChatPinned(tab)
  const activeTab = tabs.find(tab => tab.tabId === activeTabId)
  const visibleTabs = activeTab && !onStrip(activeTab) ? [...tabs.filter(onStrip), activeTab] : tabs.filter(onStrip)
  const [moreOpen, setMoreOpen] = useState(false)
  const [reordering, setReordering] = useState(false)
  const canReorderInMenu = tabs.length > 5
  useEffect(() => { if (!moreOpen || !canReorderInMenu) setReordering(false) }, [moreOpen, canReorderInMenu])
  const [renaming, setRenaming] = useState<string | null>(null)
  const [renameDraft, setRenameDraft] = useState('')
  const moreRef = useRef<HTMLDivElement>(null)
  const menuRef = useRef<HTMLDivElement>(null)
  const [menuPosition, setMenuPosition] = useState({ top: 0, left: 0, maxHeight: 400 })
  useLayoutEffect(() => {
    if (!moreOpen) return
    const position = () => {
      const rect = moreRef.current?.getBoundingClientRect()
      if (!rect) return
      const viewport = window.visualViewport
      const visibleTop = viewport?.offsetTop ?? 0
      const bottom = visibleTop + (viewport?.height ?? window.innerHeight)
      const top = Math.max(visibleTop + 8, Math.min(rect.bottom + 4, bottom - 120))
      setMenuPosition({ top, left: Math.max(8, Math.min(rect.right - 320, window.innerWidth - Math.min(320, window.innerWidth - 16) - 8)), maxHeight: Math.min(416, Math.max(0, bottom - top - 8)) })
    }
    position()
    menuRef.current?.querySelector<HTMLButtonElement>('button')?.focus()
    window.addEventListener('resize', position)
    window.addEventListener('scroll', position, true)
    window.visualViewport?.addEventListener('resize', position)
    window.visualViewport?.addEventListener('scroll', position)
    return () => {
      window.removeEventListener('resize', position); window.removeEventListener('scroll', position, true)
      window.visualViewport?.removeEventListener('resize', position); window.visualViewport?.removeEventListener('scroll', position)
    }
  }, [moreOpen])
  useEffect(() => {
    if (!moreOpen) return
    const close = (event: MouseEvent) => { if (!moreRef.current?.contains(event.target as Node) && !menuRef.current?.contains(event.target as Node)) setMoreOpen(false) }
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') { setMoreOpen(false); moreRef.current?.querySelector<HTMLButtonElement>('button')?.focus() } }
    document.addEventListener('mousedown', close)
    document.addEventListener('keydown', escape)
    return () => { document.removeEventListener('mousedown', close); document.removeEventListener('keydown', escape) }
  }, [moreOpen])
  // Dragging a side tab onto another saves the new order on the server; the strip then follows the server's order.
  const [draggingKey, setDraggingKey] = useState<string | null>(null)
  const [dropKey, setDropKey] = useState<string | null>(null)
  const drag = useRef<{ key: string; tabId: string; attemptId: string; dropped: boolean } | null>(null)
  const dragMime = 'application/x-agentworks-chat-key'
  const startDrag = (event: ReactDragEvent<HTMLDivElement>, key: string, tabId: string) => {
    if ((event.target as Element).closest('button,input')) { event.preventDefault(); return }
    // A payload is required by native browser drag/drop. A ref keeps source
    // identity stable while React renders the selected tab or drop indicator.
    event.dataTransfer.setData(dragMime, key)
    event.dataTransfer.setData('text/plain', key)
    event.dataTransfer.effectAllowed = 'move'
    drag.current = { key, tabId, attemptId: crypto.randomUUID(), dropped: false }
    setDraggingKey(key)
    if (canonicalSessionId) recordTabReorderTelemetry(canonicalSessionId, drag.current.attemptId, 'start', tabId)
  }
  const endDrag = () => {
    const attempt = drag.current
    if (attempt && !attempt.dropped && canonicalSessionId) recordTabReorderTelemetry(canonicalSessionId, attempt.attemptId, 'cancel', attempt.tabId)
    drag.current = null; setDraggingKey(null); setDropKey(null)
  }
  const overTab = (event: ReactDragEvent<HTMLDivElement>, key: string | undefined) => {
    if (!drag.current || !key) return
    event.preventDefault(); event.dataTransfer.dropEffect = 'move'; setDropKey(key)
  }
  const dropTab = (event: ReactDragEvent<HTMLDivElement>, key: string | undefined) => {
    const attempt = drag.current
    if (!attempt || !key) return
    event.preventDefault(); event.stopPropagation()
    attempt.dropped = true
    void moveSideChat(attempt.key, key, attempt)
    setDropKey(null)
  }
  const moveSideChat = async (fromKey: string, toKey: string, attempt: { key: string; tabId: string; attemptId: string; dropped: boolean } = { key: fromKey, tabId: sideChats.find(tab => sideChatKey(tab) === fromKey)?.tabId || '', attemptId: crypto.randomUUID(), dropped: true }) => {
    const keys = ownedListed.map(chat => chat.conversation_key)
    const from = keys.indexOf(fromKey)
    const to = keys.indexOf(toKey)
    const targetId = sideChats.find(tab => sideChatKey(tab) === toKey)?.tabId
    const trace = (status: 'drop' | 'no_change' | 'saved' | 'failed') => { if (canonicalSessionId) recordTabReorderTelemetry(canonicalSessionId, attempt.attemptId, status, attempt.tabId, targetId) }
    trace('drop')
    if (from < 0 || to < 0 || from === to) { trace('no_change'); return }
    keys.splice(to, 0, ...keys.splice(from, 1))
    try {
      await onOrder(keys)
      trace('saved')
    } catch (cause) {
      trace('failed')
      useChatStore.getState().addToast(cause instanceof Error ? cause.message : 'Could not save the tab order.', 'error')
    }
  }
  const pinSideChat = async (key: string, pinned: boolean) => {
    try {
      await onPin(key, pinned)
    } catch (cause) {
      useChatStore.getState().addToast(cause instanceof Error ? cause.message : 'Could not pin this tab.', 'error')
    }
  }
  // Cmd/Ctrl+arrows cycle visible chats outside editors; number keys pick a chat.
  // Alt+Shift+T / Option+Shift+T opens one
  // (PLAT-571), only while this product is on screen and never for a key
  // the terminal already took (it marks its own Alt keys handled).
  const tabsRef = useRef(tabs)
  tabsRef.current = tabs
  const visibleTabsRef = useRef(visibleTabs)
  visibleTabsRef.current = visibleTabs
  useEffect(() => {
    if (!allowSideChats) return
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || useProductSurfaceStore.getState().productSurface !== surface) return
      if (event.target instanceof Element && event.target.closest('.xterm')) return
      const action = workChatTabShortcut(event)
      if (!action) return
      if (action.kind === 'adjacent') {
        // Keep macOS line navigation, word navigation and selection in editors.
        if (event.target instanceof Element && event.target.closest('input,textarea,select,[contenteditable]:not([contenteditable="false"]),[role="textbox"],.monaco-editor,.cm-editor')) return
        const shown = visibleTabsRef.current
        const active = shown.findIndex(tab => isTabActive ? isTabActive(tab) : tab.tabId === useChatStore.getState().activeTabId)
        if (shown.length < 2 || active < 0) return
        event.preventDefault()
        markFeatureUsed('chat-tab-keys')
        onSelectTab(shown[(active + action.direction + shown.length) % shown.length].tabId)
        return
      }
      event.preventDefault()
      markFeatureUsed(action.kind === 'new' ? 'chat-tab-new' : 'chat-tab-keys')
      if (action.kind === 'new') {
        setNewTabName(current => current ?? '')
        return
      }
      const target = tabsRef.current[action.index]
      if (target) onSelectTab(target.tabId)
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [allowSideChats, surface, onSelectTab, isTabActive])
  // With many tabs the strip scrolls sideways (tabs keep their width, "+" stays in view) and the
  // active tab is brought into view when it changes.
  const stripRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const active = stripRef.current?.querySelector('[data-tab-active="true"]')
    if (active && typeof (active as HTMLElement).scrollIntoView === 'function') (active as HTMLElement).scrollIntoView({ inline: 'nearest', block: 'nearest' })
  }, [activeTabId, tabs.length])
  const newTabForm = allowSideChats && newTabName !== null && (
    <form
      className="absolute inset-x-3 top-full z-40 flex items-center rounded-lg border border-border bg-background p-2 shadow-lg md:static md:shrink-0 md:border-0 md:bg-transparent md:p-0 md:shadow-none"
      onSubmit={event => { event.preventDefault(); void submitNewAgent(newTabName) }}
    >
      <input
        autoFocus
        value={newTabName}
        onChange={event => setNewTabName(event.target.value)}
        onKeyDown={event => { if (event.key === 'Escape') setNewTabName(null) }}
        onBlur={() => { if (!opening && !newTabName.trim()) setNewTabName(null) }}
        maxLength={120}
        disabled={opening}
        placeholder="Name this agent, e.g. research"
        aria-label="Agent name"
        className="h-11 w-full md:h-7 md:w-48 rounded-md border border-input bg-background px-2 text-xs focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        data-testid="work-new-chat-tab-name"
      />
    </form>
  )

  return (
    <div className="flex min-w-0 flex-1 items-center gap-1">
      {!phone && <ProjectWorkspaceTitle name={projectName} surface={surface} />}
      <div ref={stripRef} className="hidden min-w-[84px] items-center gap-1 overflow-x-auto md:flex [scrollbar-width:none] [&::-webkit-scrollbar]:hidden [&>*]:shrink-0" data-testid="work-chat-tab-strip" aria-label="Chat tabs">
      {visibleTabs.map(tab => {
        const key = tab.tabId === canonicalTabId || !canControl(tab) ? undefined : sideChatKey(tab)
        const targetKey = key || (tab.tabId === canonicalTabId ? listed?.[0]?.conversation_key : undefined)
        return <div
          key={tab.tabId}
          data-chat-tab-id={tab.tabId}
          draggable={!!key}
          onDragStart={key ? event => startDrag(event, key, tab.tabId) : undefined}
          onDragOver={event => overTab(event, targetKey)}
          onDrop={event => dropTab(event, targetKey)}
          onDragEnd={endDrag}
          className={`flex items-center rounded border-l-2 ${dropKey === targetKey && draggingKey !== key ? 'border-primary bg-primary/10' : 'border-transparent'} ${draggingKey === key ? 'opacity-50' : ''}`}
          title={key ? 'Drag to reorder this agent' : undefined}
        ><AgentWorksChatTabItem
        tab={tab}
        isActive={isTabActive ? isTabActive(tab) : tab.tabId === activeTabId}
        canClose={false}
        isBlank={false}
        displayName={tabLabel(tab)}
        workspaceActivity={tab.tabId === canonicalTabId ? projectActivity : undefined}
        titleOverride={tab.tabId === canonicalTabId ? projectName : key ? sideChatName(tab) : undefined}
        onTabClick={selectTab}
        onCloseTab={closeProjectTab}
        closeStopsSession={!!key}
        onMakeInteractive={onMakeInteractive}
      />{renderAfterTab?.(tab)}</div>
      })}
      </div>
      {(tabs.length > 1 || phone) && (
        <div ref={moreRef} className="relative shrink-0 max-md:min-w-0 max-md:flex-1">
          <button
            type="button"
            onClick={() => setMoreOpen(open => !open)}
            aria-expanded={moreOpen}
            className="inline-flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring max-md:h-10 max-md:w-full max-md:justify-center max-md:gap-2 max-md:px-2 max-md:text-sm max-md:font-semibold max-md:text-foreground"
            title="Switch agent"
            aria-label={`Agents: switch, reorder or close (${agentCount})`}
            aria-haspopup="dialog"
            data-testid="work-more-tabs"
          >
            {phone && activeTab ? <>
              <AgentMenuActivity tabId={activeTab.tabId} workspaceActivity={activeTab.tabId === canonicalTabId ? projectActivity : undefined} />
              <span className="truncate">{tabLabel(activeTab)}</span>
              <ChevronDown className={`h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform ${moreOpen ? 'rotate-180' : ''}`} aria-hidden="true" />
            </> : <ChevronDown className="h-3.5 w-3.5" aria-hidden="true" />}
          </button>
          {moreOpen && createPortal(
            <div ref={menuRef} style={menuPosition} className="fixed z-[100] flex w-80 max-w-[calc(100vw-1rem)] flex-col rounded-xl border border-border bg-popover p-1.5 shadow-lg" role="dialog" aria-label="Agents">
              <div className="flex shrink-0 items-center justify-between gap-3 border-b border-border px-2 pb-2 pt-1">
                <h2 className="text-xs font-semibold">Agents & activity</h2>
                {canReorderInMenu && <button type="button" aria-pressed={reordering} onClick={() => setReordering(value => !value)} className="rounded px-2 py-1 text-xs text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">{reordering ? 'Done' : 'Reorder'}</button>}
              </div>
              {reordering && <p className="shrink-0 px-2 py-2 text-[11px] text-muted-foreground">Drag agents or use the arrows to change their order.</p>}
              <div className="workspace-menu-scroll min-h-0 overflow-y-auto overscroll-contain pt-1">
              {tabs.map(tab => {
                const key = tab.tabId === canonicalTabId || !canControl(tab) ? undefined : sideChatKey(tab)
                // Anyone may pin another person's tab on their own strip; renaming and ordering stay with its owner.
                const pinKey = tab.tabId === canonicalTabId ? undefined : sideChatKey(tab)
                const label = tabLabel(tab)
                const position = ownedListed.findIndex(chat => chat.conversation_key === key)
                return <div
                  key={tab.tabId}
                  data-chat-list-id={tab.tabId}
                  draggable={reordering && !!key}
                  onDragStart={reordering && key ? event => startDrag(event, key, tab.tabId) : undefined}
                  onDragOver={event => overTab(event, key || listed?.[0]?.conversation_key)}
                  onDrop={event => dropTab(event, key || listed?.[0]?.conversation_key)}
                  onDragEnd={endDrag}
                  className={`group flex min-h-10 items-center gap-1 rounded-lg px-1 ${tab.tabId === activeTabId ? 'bg-muted/70' : 'hover:bg-muted/50'} ${dropKey === key && draggingKey !== key ? 'ring-1 ring-inset ring-primary' : ''}`}
                >
                  {reordering && key ? <GripVertical aria-hidden="true" className="h-3.5 w-3.5 shrink-0 cursor-grab text-muted-foreground" /> : <span className="flex w-4 shrink-0 justify-center">{tab.tabId === activeTabId && <Check aria-hidden="true" className="h-3.5 w-3.5 text-primary" />}</span>}
                  {renaming === tab.tabId ? <input
                    autoFocus
                    aria-label={`New name for ${label}`}
                    value={renameDraft}
                    onChange={event => setRenameDraft(event.target.value)}
                    onKeyDown={event => {
                      if (event.key === 'Escape') { event.stopPropagation(); setRenaming(null) }
                      if (event.key === 'Enter') { void renameSideChat(tab, renameDraft).finally(() => setRenaming(null)) }
                    }}
                    onBlur={() => setRenaming(null)}
                    className="min-w-0 flex-1 rounded border border-border bg-background px-2 py-1 text-xs"
                  /> : <button
                    type="button"
                    onClick={() => { setMoreOpen(false); selectTab(tab.tabId) }}
                    title={[label, tab.metadata?.agentProfileTabOwnerName].filter(Boolean).join(' · ')}
                    className={`min-w-0 flex-1 rounded py-2 text-left text-xs focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${tab.tabId === activeTabId ? 'font-medium text-foreground' : 'text-foreground/85'}`}
                  >
                    <span className="flex min-w-0 items-center gap-2"><AgentMenuActivity tabId={tab.tabId} workspaceActivity={tab.tabId === canonicalTabId ? projectActivity : undefined} /><span className="truncate">{label}</span>{phone && <AgentMenuStateWord tabId={tab.tabId} workspaceActivity={tab.tabId === canonicalTabId ? projectActivity : undefined} />}</span>
                    {!isAgentTab(tab) && <span className="block text-[10px] font-normal text-muted-foreground">Activity</span>}
                  </button>}
                  {reordering && key ? <div className="flex shrink-0 items-center">
                    <button type="button" aria-label={`Move ${label} earlier`} disabled={position <= 0} onClick={() => { const target = ownedListed[position - 1]; if (target) void moveSideChat(key, target.conversation_key) }} className="inline-flex h-7 w-7 items-center justify-center rounded text-muted-foreground hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-30"><ArrowUp className="h-3.5 w-3.5" /></button>
                    <button type="button" aria-label={`Move ${label} later`} disabled={position < 0 || position === ownedListed.length - 1} onClick={() => { const target = ownedListed[position + 1]; if (target) void moveSideChat(key, target.conversation_key) }} className="inline-flex h-7 w-7 items-center justify-center rounded text-muted-foreground hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-30"><ArrowDown className="h-3.5 w-3.5" /></button>
                  </div> : <>
                  {key && renaming !== tab.tabId && <button type="button" aria-label={`Rename ${label}`} title="Rename" className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring" onClick={() => { setRenameDraft(sideChatName(tab)); setRenaming(tab.tabId) }}><Pencil className="h-3.5 w-3.5" /></button>}
                  {pinKey && <button type="button" aria-pressed={sideChatPinned(tab)} aria-label={sideChatPinned(tab) ? `Unpin ${label}` : `Pin ${label} to the tab bar`} title={sideChatPinned(tab) ? 'Pinned to the tab bar. Click to unpin' : 'Pin to the tab bar'} className={`inline-flex h-7 w-7 shrink-0 items-center justify-center rounded hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring ${sideChatPinned(tab) ? 'text-foreground' : 'text-muted-foreground'}`} onClick={() => { void pinSideChat(pinKey, !sideChatPinned(tab)) }}>{sideChatPinned(tab) ? <PinOff className="h-3.5 w-3.5" /> : <Pin className="h-3.5 w-3.5" />}</button>}
                  {tab.tabId !== canonicalTabId && canControl(tab) && (
                    <button
                      type="button"
                      onClick={() => { setMoreOpen(false); closeProjectTab(tab.tabId) }}
                      className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
                      aria-label={`Close ${label}`}
                      title={isAgentTab(tab) ? "Close agent" : "Close activity view"}
                    >
                      <X className="h-3 w-3" />
                    </button>
                  )}
                  </>}
                </div>
              })}
              </div>
              {phone && allowSideChats && <button type="button" disabled={opening || ownedListed.length >= WORK_SIDE_CHAT_LIMIT} onClick={() => { setMoreOpen(false); setNewTabName('') }}
                className="mt-1 flex min-h-11 shrink-0 items-center gap-2 border-t border-border px-2 text-sm text-primary disabled:opacity-40"><Plus className="h-4 w-4" aria-hidden="true" />New tab</button>}
            </div>, document.body
          )}
        </div>
      )}

      {newTabForm}
      {allowSideChats && newTabName === null && (
        <button
          type="button"
          onClick={() => setNewTabName('')}
          disabled={opening || ownedListed.length >= WORK_SIDE_CHAT_LIMIT}
          className={`max-md:hidden ml-0.5 inline-flex h-7 shrink-0 items-center justify-center gap-1 rounded-md text-xs text-primary transition-colors hover:bg-primary/10 hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-40 ${sideChats.length === 0 ? 'px-2' : 'w-7'}`}
          aria-label="New agent"
          title={ownedListed.length >= WORK_SIDE_CHAT_LIMIT
            ? `At most ${WORK_SIDE_CHAT_LIMIT + 1} agents per project`
            : 'New agent (⌥⇧T on Mac, Alt+Shift+T on Windows): another agent in this project. The main agent keeps Slack, WhatsApp, MCP and schedules'}
          data-testid="work-new-chat-tab"
        >
          {opening ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Plus className="h-3.5 w-3.5" />}
          {/* Labelled until the first side chat exists, then a browser-style "+" next to the tabs. */}
          {sideChats.length === 0 && <span className="hidden sm:inline">New agent</span>}
        </button>
      )}
    </div>
  )
}
