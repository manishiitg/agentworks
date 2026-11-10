import { markFeatureUsed } from '../../utils/featureUsage'
import { workChatTabShortcut } from '../../products/work/workTabs'
import { useEffect, useRef, type ReactNode } from 'react'
import { useAppViewport } from '../../hooks/useAppViewport'
import { useNotifyFocus } from '../../hooks/useNotifyFocus'
import { AppQuickSwitcher } from './AppQuickSwitcher'
import { AppSidebar } from './AppSidebar'
import { WorkspaceAccountMenu } from './WorkspaceAccountMenu'
import { ProductNavigationContext, CompactProductNavigationContext } from './productNavigationContexts'
import ToastHost from '../ui/ToastHost'
import { TooltipProvider } from '../ui/tooltip'

/** Authenticated layout: a stable left menu beside the active page's header/content. */
export function AppShell({ children }: { children: ReactNode }) {
  const viewport = useRef<HTMLDivElement>(null)
  useAppViewport(viewport)
  useNotifyFocus()
  useEffect(() => {
    const navigate = (event: KeyboardEvent) => {
      if (event.isComposing) return
      const action = workChatTabShortcut(event)
      if (action?.kind !== 'adjacent') return
      // Back/Forward only when the person is not typing: in the chat box, text fields, editors and terminals these keys
      // keep their editing job (Cmd+Left = line start on a Mac, Ctrl+Left = previous word on Windows and in terminals).
      if (event.target instanceof Element && event.target.closest('input,textarea,select,[contenteditable]:not([contenteditable="false"]),[role="textbox"],.monaco-editor,.cm-editor,.xterm')) return
      event.preventDefault()
      event.stopImmediatePropagation()
      markFeatureUsed('chat-history-keys')
      window.history.go(action.direction)
    }
    window.addEventListener('keydown', navigate, true)
    return () => window.removeEventListener('keydown', navigate, true)
  }, [])
  return <TooltipProvider><ProductNavigationContext.Provider value={false}><CompactProductNavigationContext.Provider value={true}>
    <div ref={viewport} data-app-shell className="flex h-dvh min-h-0 overflow-hidden bg-background">
      <AppSidebar />
      <div data-app-main className="relative min-h-0 min-w-0 flex-1">{children}</div>
      <WorkspaceAccountMenu />
      <AppQuickSwitcher />
      <ToastHost />
    </div>
  </CompactProductNavigationContext.Provider></ProductNavigationContext.Provider></TooltipProvider>
}
