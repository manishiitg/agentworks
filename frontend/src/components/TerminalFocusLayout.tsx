import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'
import { normalizeEventViewMode, useChatStore } from '../stores/useChatStore'
import { useCodeFilesPreference } from '../products/work/codeLocalFiles'
import ConfirmationDialog from './ui/ConfirmationDialog'
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
  // A Code workspace working on files on the user's own computer: most features do not work in Local mode, so focus mode
  // (no extra toolbars, just the chat and files) is offered there too, not only in the terminal view.
  const localCode = useCodeFilesPreference(sessionId || '').location === 'computer'
  const projectKey = useChatStore(state => activeTabId ? state.chatTabs[activeTabId]?.metadata?.agentProfileProjectId : undefined)
  const available = enabled && !!sessionId && (terminalSelected || localCode)
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

  // Only the workspace's own layout asks (the app-level one wraps it). Asked once per workspace in this browser, never again unless the user enters focus mode themselves.
  const askKey = `local-focus-asked:${projectKey || sessionId || ''}`
  const [asked, setAsked] = useState(false)
  useEffect(() => { setAsked(false) }, [askKey])
  const alreadyAsked = (() => { try { return localStorage.getItem(askKey) !== null } catch { return true } })()
  const answer = (enter: boolean) => {
    try { localStorage.setItem(askKey, enter ? 'entered' : 'declined') } catch { /* Asked again next time without storage. */ }
    setAsked(true)
    if (enter) toggle()
  }

  return (
    <TerminalFocusContext.Provider value={{ available, focused, toggle }}>
      <div className={className} data-terminal-focus={focused || undefined}>
        {children}
        {focused && !terminalSelected && <button type="button" onClick={toggle} aria-label="Exit focus mode"
          className="fixed right-3 top-3 z-50 rounded-md border border-border bg-background px-3 py-1.5 text-xs font-medium shadow-sm hover:bg-muted">Exit focus mode</button>}
      </div>
      <ConfirmationDialog
        isOpen={tabId !== undefined && enabled && localCode && !!sessionId && !focused && !asked && !alreadyAsked}
        onClose={() => answer(false)}
        onConfirm={() => answer(true)}
        title="Use focus mode?"
        message="This workspace works on files on your computer. In Local mode most features (integrations, skills, schedules, dashboards) are not available, so focus mode hides the extra toolbars and keeps just the chat and your files. Leave it any time with Exit focus mode, top right."
        confirmText="Enter focus mode"
        cancelText="Not now"
        type="info"
        ignoreWorkspaceAutoCollapse
      />
    </TerminalFocusContext.Provider>
  )
}
