import React, { useEffect, useRef, useState } from 'react'
import { MessageSquare, Pencil, X } from 'lucide-react'
import type { ChatTab } from '../../stores/useChatStore'

export interface ChatTabPillProps {
  tab: Pick<ChatTab, 'tabId' | 'name' | 'isStreaming' | 'hasRunningBgAgents' | 'hasUnreadCompletion' | 'metadata'>
  isActive: boolean
  canClose: boolean
  isBlank: boolean
  displayName?: string
  titleOverride?: string
  readOnly?: boolean
  onTabClick: (tabId: string) => void
  onCloseTab: (tabId: string) => void
  onRename?: (name: string) => Promise<boolean | void>
  onMakeInteractive?: (tabId: string) => void
}

const ALLOW_MAKE_SCHEDULE_INTERACTIVE = false
const TAB_STATUS_DOT: Record<'busy' | 'completed' | 'ready', { cls: string; label: string }> = {
  busy: { cls: 'bg-[hsl(var(--info))] animate-pulse', label: 'Busy' },
  completed: { cls: 'bg-[hsl(var(--success))]', label: 'Completed — open to mark as seen' },
  ready: { cls: 'bg-muted-foreground/60', label: 'Ready' },
}

/** The shared Chat tab pill used by workflows, Work, and CapLayer. */
export const ChatTabPill = React.memo<ChatTabPillProps>(({
  tab, isActive, canClose, isBlank, displayName: displayNameOverride, titleOverride,
  onTabClick, onCloseTab, onRename, onMakeInteractive, readOnly = false,
}) => {
  const displayName = displayNameOverride ?? tab.name
  const rawStatus: 'busy' | 'completed' | 'ready' = tab.isStreaming || tab.hasRunningBgAgents
    ? 'busy'
    : tab.hasUnreadCompletion && !isActive ? 'completed' : 'ready'
  const [status, setStatus] = useState(rawStatus)
  const [isRenaming, setIsRenaming] = useState(false)
  const [renameDraft, setRenameDraft] = useState(tab.name)
  const [isSavingName, setIsSavingName] = useState(false)
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
  const isBusy = status === 'busy'
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

  return (
    <div
      onClick={() => onTabClick(tab.tabId)}
      onKeyDown={(event) => event.key === 'Enter' && onTabClick(tab.tabId)}
      role="button"
      tabIndex={0}
      className={`group flex min-w-0 cursor-pointer items-center gap-1.5 rounded-t-md px-2 py-1 text-xs font-medium outline-none transition-colors ${
        isActive
          ? 'bg-gray-100 text-gray-900 dark:bg-gray-800 dark:text-gray-100'
          : 'text-gray-600 hover:bg-gray-100 hover:text-gray-900 dark:text-gray-400 dark:hover:bg-gray-700 dark:hover:text-gray-100'
      }`}
    >
      {!isBlank && <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${dot.cls}`} title={dot.label} aria-label={dot.label} />}
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
          className="min-w-0 max-w-[14rem] truncate whitespace-nowrap"
          title={titleOverride || tab.name || displayName}
        >
          {displayName}
        </span>
      )}
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
      {ALLOW_MAKE_SCHEDULE_INTERACTIVE && onMakeInteractive && tab.metadata?.isViewOnly && (tab.metadata?.isScheduledRun || tab.metadata?.isBotRun) && !readOnly && (
        <button type="button" onClick={(event) => { event.stopPropagation(); onMakeInteractive(tab.tabId) }} className="ml-0.5 rounded p-0.5 text-blue-600 opacity-80 hover:bg-blue-100 hover:opacity-100 dark:text-blue-300 dark:hover:bg-blue-900/40" title="Interact in Automation Builder" aria-label="Interact in Automation Builder">
          <MessageSquare className="h-3 w-3" />
        </button>
      )}
      {canClose && (
        <button
          type="button"
          disabled={isBusy}
          onClick={(event) => { event.stopPropagation(); if (!isBusy) onCloseTab(tab.tabId) }}
          className={`ml-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded text-gray-400 transition-colors ${isBusy ? 'cursor-not-allowed opacity-40' : 'hover:bg-gray-200 hover:text-gray-700 dark:hover:bg-gray-700 dark:hover:text-gray-200'}`}
          aria-label={isBusy ? `${displayName} is still running — stop it before closing` : `Close ${displayName}`}
          title={isBusy ? 'Still running — stop the run before closing' : 'Close tab'}
        >
          <X className="h-3 w-3" />
        </button>
      )}
    </div>
  )
})

ChatTabPill.displayName = 'ChatTabPill'
