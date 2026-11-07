import React, { useCallback, useEffect, useRef, useState } from 'react'
import { ChevronDown, Loader2, MessageSquare, MessageSquareText, Pencil, PinOff, X } from 'lucide-react'
import { useProductNavigationToolbarStore } from '../../stores/useProductNavigationToolbarStore'
import { readableGeneratedChatTitle } from '../../utils/generatedChatTitle'
import { useChatStore, type ChatTab } from '../../stores/useChatStore'
import { useAgentMenuState } from './agentMenuState'
import type { ProductSurface } from '../../products/productSurfaceConfig'
import { ProductSurfaceIcon } from '../ProductSurfaceIcon'
import ConfirmationDialog from '../ui/ConfirmationDialog'
import { compactOwnerChatName } from './chatTabName'
import { AgentPurposeButton } from './AgentPurposeButton'
import { AgentHoverCard, type AgentCardInfo } from './AgentCard'
import { AgentMark, AgentSetupMark } from './AgentIcon'

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
  /** The agent's one-line purpose, shown on its purpose icon (tab owner only). */
  purpose?: string
  /** Puts the purpose request in this tab's chat box; the icon sits where the status dot is while the tab is idle. */
  onSetPurpose?: () => void
  /** A Crew agent's tab (owner, 4 Nov, tab option B): its name only, a tiny mark before it only when needed, and its
   * details in a card on hover. */
  agent?: { asking?: boolean; info?: AgentCardInfo }
}

const ALLOW_MAKE_SCHEDULE_INTERACTIVE = false
// The state reads at a glance on a tab you are not looking at (owner 2026-10-06):
// a spinner while working, amber when it waits for you, red when the server says
// the last turn failed, green when it finished since you last looked, grey when idle.
// A selected ready tab uses a red dot; activity indicators keep their state colors.
// Needs you / Working / Failed use the same words as the agent menu and Home (PLAT-746-158).
type TabStatus = 'waiting' | 'busy' | 'failed' | 'completed' | 'ready'
const TAB_STATUS_DOT: Record<TabStatus, { cls: string; label: string }> = {
  waiting: { cls: 'bg-[hsl(var(--warning))] animate-pulse', label: 'Needs you' },
  busy: { cls: 'bg-[hsl(var(--info))]', label: 'Working' },
  failed: { cls: 'bg-[hsl(var(--destructive))]', label: 'Failed' },
  completed: { cls: 'bg-[hsl(var(--success))]', label: 'Completed — open to mark as seen' },
  ready: { cls: 'bg-muted-foreground/60', label: 'Ready' },
}

/** The shared Chat tab pill used by workflows, Work, and Vault. */
export const ChatTabPill = React.memo<ChatTabPillProps>(({
  tab, isActive, canClose, isBlank, displayName: displayNameOverride, titleOverride, productSurface, workspaceActivity,
  onTabClick, onCloseTab, onRename, onMakeInteractive, onUnpin, purpose, onSetPurpose, agent, readOnly = false, closeStopsSession = false,
}) => {
  const ownerName = tab.metadata?.agentProfileTabOwnerName
  const yourTab = !!ownerName && tab.metadata?.agentProfileTabCanWrite === true
  const rawName = compactOwnerChatName(displayNameOverride ?? tab.name, ownerName)
  const displayName = readableGeneratedChatTitle(rawName, tab.metadata?.isScheduledRun) ||
    rawName
  const rawTooltip = titleOverride || tab.name || displayName
  const nameTooltip = readableGeneratedChatTitle(rawTooltip, tab.metadata?.isScheduledRun) || rawTooltip
  const tooltip = ownerName ? `${nameTooltip} · ${yourTab ? 'Your tab' : 'Owned by'} · ${ownerName}` : nameTooltip
  const workspacePickable = !!productSurface && ['agentworks', 'relays', 'work', 'code'].includes(productSurface)
  const workspaceTrigger = useRef<HTMLButtonElement | null>(null)
  const pickerOpen = useProductNavigationToolbarStore(state => state.pickerOpen)
  const registerWorkspaceTrigger = useCallback((node: HTMLButtonElement | null) => {
    const previous = workspaceTrigger.current
    workspaceTrigger.current = node
    if (node || useProductNavigationToolbarStore.getState().workspaceTrigger === previous)
      useProductNavigationToolbarStore.setState({ workspaceTrigger: node })
  }, [])
  // One status source for the pill, the left menu, and the rail (agentMenuState.ts).
  const menuState = useAgentMenuState(tab.tabId, workspaceActivity, tab)
  const status: TabStatus = menuState === 'waiting' ? 'waiting' : menuState === 'running' ? 'busy' : menuState === 'failed' ? 'failed' : menuState === 'done' ? 'completed' : 'ready'
  const [isRenaming, setIsRenaming] = useState(false)
  const [renameDraft, setRenameDraft] = useState(tab.name)
  const [isSavingName, setIsSavingName] = useState(false)
  const [closingSessionId, setClosingSessionId] = useState<string | null>(null)
  const renameInputRef = useRef<HTMLInputElement>(null)
  const dot = TAB_STATUS_DOT[status]
  const previousStatus = useRef(status)
  const [announcement, setAnnouncement] = useState('')
  useEffect(() => {
    if (previousStatus.current === status) return
    previousStatus.current = status
    setAnnouncement(`${displayName}: ${TAB_STATUS_DOT[status].label}`)
  }, [status, displayName])
  const isBusy = status === 'busy'
  // Closing a temporary run view only hides it, even while its execution continues.
  const closeBlocked = isBusy && tab.metadata?.isViewOnly !== true
  useEffect(() => {
    if (!isRenaming) setRenameDraft(tab.name)
  }, [isRenaming, tab.name])
  useEffect(() => {
    if (isRenaming) { renameInputRef.current?.focus(); renameInputRef.current?.select() }
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
      aria-label={tooltip}
      tabIndex={0}
      data-tab-active={isActive ? 'true' : undefined}
      className={`group relative flex min-w-0 cursor-pointer items-center font-medium outline-none transition-colors ${agent ? 'gap-1.5 rounded-lg px-2.5 py-1 text-[13px]' : `gap-1.5 border-b-2 border-transparent px-2 ${productSurface ? 'rounded-lg py-1.5 text-sm' : 'rounded-md py-1 text-xs'}`} ${
        // A soft background identifies the selected tab without changing its height.
        isActive
          ? 'bg-muted font-semibold text-foreground'
          : productSurface
          ? 'text-muted-foreground hover:bg-muted hover:text-foreground'
          : 'text-gray-600 hover:bg-gray-100 hover:text-gray-900 dark:text-gray-400 dark:hover:bg-gray-700 dark:hover:text-gray-100'
      }`}
    >
      {!ownerName && productSurface && <ProductSurfaceIcon surface={productSurface} className="h-4 w-4" />}
      <span className="sr-only" role="status" aria-live="polite" aria-atomic="true">{announcement}</span>
      {/* An agent is its name only (owner, 5 Nov: no icons): its mark (working, needs you, failed, new reply) sits before it. */}
      {agent ? <AgentMark state={isBlank ? null : menuState} asking={agent.asking} /> :
      <span data-chat-status-slot className="grid h-3 w-3 shrink-0 place-items-center">
        {/* An idle tab shows its owner a quiet purpose icon in this spot instead (owner, 3 Nov). */}
        {!isBlank && status === 'ready' && onSetPurpose && <AgentPurposeButton name={displayName} purpose={purpose} onSetPurpose={onSetPurpose} />}
        {/* No dot for an idle chat (owner, 2 Nov): only Working, Needs you, Completed and Failed show; red means Failed only. */}
        {!isBlank && status !== 'ready' && (status === 'busy'
          ? <Loader2 className="h-3 w-3 animate-spin text-[hsl(var(--info))]" aria-label={dot.label} role="img"><title>{dot.label}</title></Loader2>
          : status === 'waiting'
            // Waiting for the person (a question or an approval): a ring keeps pulsing so a stuck chat stands out.
            ? <span className="relative grid h-3 w-3 place-items-center" title={dot.label} aria-label={dot.label} role="img">
                <span className="absolute inline-flex h-3 w-3 animate-ping rounded-full bg-[hsl(var(--warning))] opacity-60" />
                <span className="relative h-2.5 w-2.5 rounded-full bg-[hsl(var(--warning))]" />
              </span>
            : <span className={`h-2 w-2 rounded-full ${dot.cls}`} title={dot.label} aria-label={dot.label} role="img" />)}
      </span>}
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
        <>
          <AgentHoverCard info={agent?.info} onRename={onRename && !isBlank ? () => { setRenameDraft(tab.name); setIsRenaming(true) } : undefined}><span
            className={`block min-w-0 truncate whitespace-nowrap ${productSurface ? 'max-w-[18rem] font-semibold' : 'max-w-[14rem]'}`}
            title={agent?.info ? undefined : tooltip}
          >
            {displayName}
          </span></AgentHoverCard>
          <AgentSetupMark setup={agent?.info?.setup} />
          {/* Another person's agent shows no mark (owner, 6 Nov): their name is the strip's group label. */}
          {ownerName && !yourTab && <span className="sr-only">Only {ownerName} types here</span>}
        </>
      )}
      <ShowChatButton tabId={tab.tabId} />
      {productSurface && <>
        {workspacePickable && <button ref={registerWorkspaceTrigger} type="button" data-workspace-setup-trigger aria-label="Switch workspace" aria-haspopup="menu" aria-expanded={pickerOpen}
          title="Switch workspace or product"
          onClick={event => { event.stopPropagation(); window.dispatchEvent(new Event('open-workspace-picker')) }}
          onKeyDown={event => { if (event.key === 'Enter' || event.key === ' ') event.stopPropagation() }}
          className="grid h-5 w-5 shrink-0 place-items-center rounded text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
          <ChevronDown aria-hidden="true" className="h-3 w-3" />
        </button>}
      </>}
      {onRename && !isBlank && !agent?.info && (
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
          disabled={closeBlocked}
          onClick={(event) => {
            event.stopPropagation()
            if (closeBlocked) return
            if (closeStopsSession && tab.sessionId) setClosingSessionId(tab.sessionId)
            else onCloseTab(tab.tabId)
          }}
          onKeyDown={event => { if (event.key === 'Enter' || event.key === ' ') event.stopPropagation() }}
          className={`ml-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded text-gray-400 opacity-0 transition-colors focus:opacity-100 ${closeBlocked ? 'cursor-not-allowed group-hover:opacity-40' : 'group-hover:opacity-100 hover:bg-gray-200 hover:text-gray-700 dark:hover:bg-gray-700 dark:hover:text-gray-200'}`}
          aria-label={closeBlocked ? `${displayName} is still running — stop it before closing` : `Close ${displayName}`}
          title={closeBlocked ? 'Still running — stop the run before closing' : 'Close tab'}
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


// While a tab shows its terminal, one click shows the chat again (the terminal keeps running).
function ShowChatButton({ tabId }: { tabId: string }) {
  const terminalView = useChatStore(state => state.chatTabs[tabId]?.viewMode === 'terminal')
  if (!terminalView) return null
  return (
    <button type="button" aria-label="Show chat" title="Show chat"
      onClick={event => { event.stopPropagation(); useChatStore.getState().setTabViewMode(tabId, 'formatted') }}
      onKeyDown={event => { if (event.key === 'Enter' || event.key === ' ') event.stopPropagation() }}
      className="grid h-5 w-5 shrink-0 place-items-center rounded text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
      <MessageSquareText aria-hidden="true" className="h-3 w-3" />
    </button>
  )
}
