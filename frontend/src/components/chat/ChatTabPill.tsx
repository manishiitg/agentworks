import React, { useCallback, useEffect, useRef, useState } from 'react'
import { ChevronDown, Loader2, MessageSquare, Pencil, PinOff, X } from 'lucide-react'
import { useProductNavigationToolbarStore } from '../../stores/useProductNavigationToolbarStore'
import { readableGeneratedChatTitle } from '../../utils/generatedChatTitle'
import { foregroundTurnState } from '../../utils/foregroundTurnActivity'
import { chatRuntimeActivity } from '../../utils/chatRuntimeActivity'
import { useChatStore, type ChatTab } from '../../stores/useChatStore'
import { runtimeNeedsUserInput } from '../../utils/runtimeActivity'
import type { ProductSurface } from '../../products/productSurfaceConfig'
import { ProductSurfaceIcon } from '../ProductSurfaceIcon'
import { PulseIcon, isPulseConversationSession } from '../workflow/pulseIcon'
import ConfirmationDialog from '../ui/ConfirmationDialog'

export interface ChatTabPillProps {
  tab: Pick<ChatTab, 'tabId' | 'name' | 'isStreaming' | 'hasRunningBgAgents' | 'hasUnreadCompletion' | 'metadata'> & { sessionId?: string | null }
  isActive: boolean
  canClose: boolean
  isBlank: boolean
  displayName?: string
  titleOverride?: string
  /** Optional server-derived aggregate for work not already represented by an open chat tab. */
  workspaceActivity?: 'running' | 'waiting' | null
  productSurface?: ProductSurface
  readOnly?: boolean
  onTabClick: (tabId: string) => void
  onCloseTab: (tabId: string) => void
  /** False for a run/inspection tab whose close only hides a view. */
  closeStopsSession?: boolean
  onRename?: (name: string) => Promise<boolean | void>
  onMakeInteractive?: (tabId: string) => void
  /** Takes the tab off the strip into the Agents menu (it stays open). */
  onUnpin?: () => void
}

const ALLOW_MAKE_SCHEDULE_INTERACTIVE = false
// The state reads at a glance on a tab you are not looking at (owner 2026-10-06):
// a spinner while working, amber when it waits for you, green when it finished
// since you last looked, grey when idle.
type TabStatus = 'waiting' | 'busy' | 'completed' | 'ready'
const TAB_STATUS_DOT: Record<TabStatus, { cls: string; label: string }> = {
  waiting: { cls: 'bg-[hsl(var(--warning))] animate-pulse', label: 'Needs your input' },
  busy: { cls: 'bg-[hsl(var(--info))]', label: 'Working' },
  completed: { cls: 'bg-[hsl(var(--success))]', label: 'Completed — open to mark as seen' },
  ready: { cls: 'bg-muted-foreground/60', label: 'Ready' },
}

/** The shared Chat tab pill used by workflows, Work, and Vault. */
export const ChatTabPill = React.memo<ChatTabPillProps>(({
  tab, isActive, canClose, isBlank, displayName: displayNameOverride, titleOverride, productSurface, workspaceActivity,
  onTabClick, onCloseTab, onRename, onMakeInteractive, onUnpin, readOnly = false, closeStopsSession = false,
}) => {
  const rawName = displayNameOverride ?? tab.name
  const displayName = readableGeneratedChatTitle(rawName, tab.metadata?.isScheduledRun) ||
    (tab.metadata?.isScheduledRun && /^(?:Manual )?Pulse$/i.test(rawName) ? rawName === 'Manual Pulse' ? 'Manual review' : 'Automatic review' : rawName)
  const rawTooltip = titleOverride || tab.name || displayName
  const tooltip = readableGeneratedChatTitle(rawTooltip, tab.metadata?.isScheduledRun) || rawTooltip
  const workspacePickable = !!productSurface && ['agentworks', 'relays', 'work', 'code'].includes(productSurface)
  const workspaceTrigger = useRef<HTMLButtonElement | null>(null)
  const pickerOpen = useProductNavigationToolbarStore(state => state.pickerOpen)
  const registerWorkspaceTrigger = useCallback((node: HTMLButtonElement | null) => {
    const previous = workspaceTrigger.current
    workspaceTrigger.current = node
    if (node || useProductNavigationToolbarStore.getState().workspaceTrigger === previous)
      useProductNavigationToolbarStore.setState({ workspaceTrigger: node })
  }, [])
  // The same signal as the activity monitor and the chat's own footer.
  const needsInput = useChatStore(state => {
    if (!tab.sessionId) return false
    return runtimeNeedsUserInput(state.activeSessionsCache.find(session => session.session_id === tab.sessionId))
  })
  // The same busy signal as the chat header: a stored completion ends the spinner at once.
  const running = useChatStore(state => {
    const stored = state.chatTabs[tab.tabId]
    if (!stored) return !!(tab.isStreaming || tab.hasRunningBgAgents)
    const foreground = foregroundTurnState(stored.sessionId ? state.tabEvents[stored.sessionId] : undefined)
    const session = stored.sessionId ? state.activeSessionsCache.find(item => item.session_id === stored.sessionId) : undefined
    return chatRuntimeActivity({
      isStreaming: stored.isStreaming,
      hasRunningBgAgents: stored.hasRunningBgAgents,
      isCompleted: stored.isCompleted,
      foregroundTurnCompleted: foreground === 'completed',
      foregroundTurnStarted: foreground === 'running',
    }, session).state === 'running'
  })
  const activityNeedsInput = workspaceActivity === undefined ? needsInput : needsInput || workspaceActivity === 'waiting'
  const activityRunning = workspaceActivity === undefined ? running : workspaceActivity === 'running'
  const rawStatus: TabStatus = activityNeedsInput
    ? 'waiting'
    : activityRunning
      ? 'busy'
      : tab.hasUnreadCompletion && !isActive ? 'completed' : 'ready'
  const [status, setStatus] = useState(rawStatus)
  const [isRenaming, setIsRenaming] = useState(false)
  const [renameDraft, setRenameDraft] = useState(tab.name)
  const [isSavingName, setIsSavingName] = useState(false)
  const [closingSessionId, setClosingSessionId] = useState<string | null>(null)
  const renameInputRef = useRef<HTMLInputElement>(null)
  useEffect(() => {
    if (rawStatus === status) return
    if (rawStatus === 'busy') {
      setStatus('busy')
      return
    }
    if (status === 'busy') {
      const timer = setTimeout(() => setStatus(rawStatus), 1200)
      return () => clearTimeout(timer)
    }
    setStatus(rawStatus)
  }, [rawStatus, status])
  const dot = TAB_STATUS_DOT[status]
  const previousStatus = useRef(status)
  const [announcement, setAnnouncement] = useState('')
  useEffect(() => {
    if (previousStatus.current === status) return
    previousStatus.current = status
    setAnnouncement(`${displayName}: ${TAB_STATUS_DOT[status].label}`)
  }, [status, displayName])
  const isBusy = running || status === 'busy'
  useEffect(() => {
    if (!isRenaming) setRenameDraft(tab.name)
  }, [isRenaming, tab.name])
  useEffect(() => {
    if (isRenaming) renameInputRef.current?.select()
  }, [isRenaming])

  const saveName = async () => {
    const nextName = renameDraft.replace(/\s+/g, ' ').trim()
    if (!nextName || !onRename || isSavingName) return
    setIsSavingName(true)
    try {
      const saved = await onRename(nextName)
      if (saved !== false) setIsRenaming(false)
    } finally {
      setIsSavingName(false)
    }
  }
  const activate = () => {
    if (workspacePickable && isActive) window.dispatchEvent(new Event('open-workspace-picker'))
    else onTabClick(tab.tabId)
  }

  return (<>
    <div
      onClick={activate}
      onKeyDown={event => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); activate() } }}
      role="button"
      tabIndex={0}
      data-tab-active={isActive ? 'true' : undefined}
      className={`group flex min-w-0 cursor-pointer items-center gap-1.5 px-2 font-medium outline-none transition-colors ${productSurface ? 'rounded-lg py-1.5 text-sm' : 'rounded-t-md py-1 text-xs'} ${
        productSurface
          ? isActive ? 'bg-muted text-foreground shadow-[inset_0_-2px_0_hsl(var(--primary))]' : 'text-muted-foreground hover:bg-muted hover:text-foreground'
          // The open tab: filled, bold and underlined in the accent colour, so it stands out in a long tab bar.
          : isActive
          ? 'bg-muted font-semibold text-foreground shadow-[inset_0_-2px_0_hsl(var(--primary))]'
          : 'text-gray-600 hover:bg-gray-100 hover:text-gray-900 dark:text-gray-400 dark:hover:bg-gray-700 dark:hover:text-gray-100'
      }`}
    >
      {productSurface && <ProductSurfaceIcon surface={productSurface} className="h-4 w-4" />}
      <span className="sr-only" role="status" aria-live="polite" aria-atomic="true">{announcement}</span>
      {isPulseConversationSession(tab.sessionId) && <PulseIcon className="h-3.5 w-3.5 shrink-0 text-primary" aria-label="Pulse" role="img" />}
      <span data-chat-status-slot className="grid h-3 w-3 shrink-0 place-items-center">
        {!isBlank && (!productSurface || status !== 'ready') && (status === 'busy'
          ? <Loader2 className="h-3 w-3 animate-spin text-[hsl(var(--info))]" aria-label={dot.label} role="img"><title>{dot.label}</title></Loader2>
          : <span className={`${status === 'ready' ? 'h-1.5 w-1.5' : 'h-2 w-2'} rounded-full ${dot.cls}`} title={dot.label} aria-label={dot.label} role="img" />)}
      </span>
      {isRenaming ? (
        <input
          ref={renameInputRef}
          value={renameDraft}
          maxLength={120}
          disabled={isSavingName}
          onClick={event => event.stopPropagation()}
          onChange={event => setRenameDraft(event.target.value)}
          onBlur={() => { void saveName() }}
          onKeyDown={event => {
            event.stopPropagation()
            if (event.key === 'Enter') { event.preventDefault(); void saveName() }
            if (event.key === 'Escape') { event.preventDefault(); setRenameDraft(tab.name); setIsRenaming(false) }
          }}
          aria-label="Chat name"
          className="h-5 w-36 rounded border border-primary/50 bg-background px-1.5 text-xs text-foreground outline-none ring-1 ring-primary/20"
        />
      ) : (
        <span
          className={`min-w-0 truncate whitespace-nowrap ${productSurface ? 'max-w-[18rem] font-semibold' : 'max-w-[14rem]'}`}
          title={tooltip}
        >
          {displayName}
        </span>
      )}
      {productSurface && <>
        {workspacePickable && <button ref={registerWorkspaceTrigger} type="button" data-workspace-setup-trigger aria-label="Switch workspace" aria-haspopup="menu" aria-expanded={pickerOpen}
          title="Switch workspace or product"
          onClick={event => { event.stopPropagation(); window.dispatchEvent(new Event('open-workspace-picker')) }}
          onKeyDown={event => { if (event.key === 'Enter' || event.key === ' ') event.stopPropagation() }}
          className="grid h-5 w-5 shrink-0 place-items-center rounded text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
          <ChevronDown aria-hidden="true" className="h-3 w-3" />
        </button>}
      </>}
      {onRename && !isBlank && (
        <button
          type="button"
          onClick={(event) => { event.stopPropagation(); setRenameDraft(tab.name); setIsRenaming(true) }}
          className={`${isRenaming ? 'hidden' : 'flex'} ml-0.5 h-4 w-4 shrink-0 items-center justify-center rounded text-gray-400 opacity-0 transition-colors hover:bg-gray-200 hover:text-gray-700 group-hover:opacity-100 focus:opacity-100 dark:hover:bg-gray-700 dark:hover:text-gray-200`}
          aria-label={`Rename ${displayName}`}
          title="Rename chat"
        >
          <Pencil className="h-2.5 w-2.5" />
        </button>
      )}
      {onUnpin && !isBlank && !isRenaming && (
        <button
          type="button"
          onClick={(event) => { event.stopPropagation(); onUnpin() }}
          className="ml-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded text-gray-400 opacity-0 transition-colors hover:bg-gray-200 hover:text-gray-700 group-hover:opacity-100 focus:opacity-100 dark:hover:bg-gray-700 dark:hover:text-gray-200"
          aria-label={`Unpin ${displayName}`}
          title="Unpin: move to the Agents menu"
        >
          <PinOff className="h-2.5 w-2.5" />
        </button>
      )}
      {ALLOW_MAKE_SCHEDULE_INTERACTIVE && onMakeInteractive && tab.metadata?.isViewOnly && (tab.metadata?.isScheduledRun || tab.metadata?.isBotRun) && !readOnly && (
        <button type="button" onClick={(event) => { event.stopPropagation(); onMakeInteractive(tab.tabId) }} className="ml-0.5 rounded p-0.5 text-blue-600 opacity-80 hover:bg-blue-100 hover:opacity-100 dark:text-blue-300 dark:hover:bg-blue-900/40" title="Interact in Automation Builder" aria-label="Interact in Automation Builder">
          <MessageSquare className="h-3 w-3" />
        </button>
      )}
      {canClose && (
        <button
          type="button"
          disabled={isBusy}
          onClick={(event) => {
            event.stopPropagation()
            if (isBusy) return
            if (closeStopsSession && tab.sessionId) setClosingSessionId(tab.sessionId)
            else onCloseTab(tab.tabId)
          }}
          className={`ml-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded text-gray-400 opacity-0 transition-colors focus:opacity-100 ${isBusy ? 'cursor-not-allowed group-hover:opacity-40' : 'group-hover:opacity-100 hover:bg-gray-200 hover:text-gray-700 dark:hover:bg-gray-700 dark:hover:text-gray-200'}`}
          aria-label={isBusy ? `${displayName} is still running — stop it before closing` : `Close ${displayName}`}
          title={isBusy ? 'Still running — stop the run before closing' : 'Close tab'}
        >
          <X className="h-3 w-3" />
        </button>
      )}
    </div>
    <ConfirmationDialog isOpen={closingSessionId !== null} onClose={() => setClosingSessionId(null)}
      onConfirm={() => {
        setClosingSessionId(null)
        if (tab.sessionId === closingSessionId) onCloseTab(tab.tabId)
      }}
      title={`Close ${displayName}?`}
      message="Closing this chat stops its background runs. Your saved conversation remains available in history."
      confirmText="Close chat" type="warning" />
  </>)
})

ChatTabPill.displayName = 'ChatTabPill'
