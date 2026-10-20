import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'
import { normalizeEventViewMode, useChatStore } from '../stores/useChatStore'
import { requestMainTerminalFocus } from '../utils/mainTerminalFocus'

const TerminalFocusContext = createContext({ available: false, focused: false, toggle: () => {} })

export const useTerminalFocusMode = () => useContext(TerminalFocusContext)

/** Keeps both workspace panes mounted while hiding their surrounding chrome. */
export function TerminalFocusLayout({ tabId, enabled = true, className, children }: {
  tabId?: string | null
  enabled?: boolean
  className: string
  children: ReactNode
}) {
  const activeTabId = useChatStore(state => tabId === undefined ? state.activeTabId : tabId)
  const sessionId = useChatStore(state => activeTabId ? state.chatTabs[activeTabId]?.sessionId : null)
  const terminalSelected = useChatStore(state => activeTabId
    ? normalizeEventViewMode(state.chatTabs[activeTabId]?.viewMode) === 'terminal'
    : false)
  const available = enabled && !!sessionId && terminalSelected
  const [owner, setOwner] = useState<{ tabId: string; sessionId: string } | null>(null)
  const focused = available && owner?.tabId === activeTabId && owner?.sessionId === sessionId

  // Focus is temporary and belongs to this visible conversation, never to
  // another project, restored session, or a page outside the workspace.
  useEffect(() => { setOwner(null) }, [activeTabId, sessionId, available])

  const toggle = () => {
    if (!available || !activeTabId || !sessionId) return
    setOwner(focused ? null : { tabId: activeTabId, sessionId })
    requestMainTerminalFocus(sessionId)
  }

  return (
    <TerminalFocusContext.Provider value={{ available, focused, toggle }}>
      <div className={className} data-terminal-focus={focused || undefined}>
        {children}
        {focused && !terminalSelected && <button type="button" onClick={toggle} aria-label="Exit focus mode"
          className="fixed right-3 top-3 z-50 rounded-md border border-border bg-background px-3 py-1.5 text-xs font-medium shadow-sm hover:bg-muted">Exit focus mode</button>}
      </div>
    </TerminalFocusContext.Provider>
  )
}
