import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from 'react'
import { ArrowLeft, MessageCircle } from 'lucide-react'
import { panelBackAction, useWorkspacePanelNavigationStore, useWorkspacePanelScope, type WorkspacePanelChatTarget } from '../../stores/useWorkspacePanelNavigationStore'
import { WorkspaceViewIconButton } from '../workflow/WorkspaceViewIconButton'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { WorkspacePanelHeaderSlotsProvider } from './WorkspacePanelHeaderSlots'

const WorkspacePanelNavigationContext = createContext<{ scope: string; chatTarget?: WorkspacePanelChatTarget; onOpenChat?: () => void }>({ scope: '' })

/** Every right-panel item gets a scoped navigation slot in its outer header. */
export function WorkspacePanelNavigationProvider({ workspacePath, view, children, chatTarget, onOpenChat }: {
  workspacePath: string | null | undefined
  view: string
  children: ReactNode
  chatTarget?: WorkspacePanelChatTarget
  onOpenChat?: () => void
}) {
  const scope = useWorkspacePanelScope(workspacePath, view)
  return <WorkspacePanelNavigationContext.Provider value={{ scope, chatTarget, onOpenChat }}><WorkspacePanelHeaderSlotsProvider>{children}</WorkspacePanelHeaderSlotsProvider></WorkspacePanelNavigationContext.Provider>
}

export function useWorkspacePanelHosted() {
  return !!useContext(WorkspacePanelNavigationContext).scope
}

/** Panel actions prepare a draft in the displayed chat, without sending it. */
export function useWorkspacePanelChatDraft(tabId?: string) {
  const { chatTarget, onOpenChat } = useContext(WorkspacePanelNavigationContext)
  return async (message: string) => {
    const target = tabId ? { tabId } : chatTarget
    if (!target) throw new Error('Open your own project chat to make this change.')
    const { openWorkspacePaneChatWithDraft } = await import('../../utils/workspacePaneChat')
    await openWorkspacePaneChatWithDraft({ ...target, message })
    onOpenChat?.()
  }
}

/** Nested details reuse the header arrow. Standalone panels get the same compact
 * control locally, so the reader remains usable without a workspace shell. */
export function WorkspacePanelBack({ label, onBack }: { label: string; onBack: () => void }) {
  const { scope } = useContext(WorkspacePanelNavigationContext)
  const owner = useRef(Symbol('panel-back'))
  const callback = useRef(onBack)
  callback.current = onBack
  useEffect(() => {
    if (!scope) return
    return useWorkspacePanelNavigationStore.getState().register({ owner: owner.current, scope, label, onBack: () => callback.current() })
  }, [scope, label])
  if (scope) return null
  return <WorkspaceViewIconButton label={label} icon={ArrowLeft} onClick={onBack} />
}

/** Header and detail views publish one contextual question to the panel header. */
export function useWorkspacePanelAssistant(ask?: { workspacePath: string | null; message: string }) {
  const { scope, chatTarget, onOpenChat } = useContext(WorkspacePanelNavigationContext)
  const owner = useRef(Symbol('panel-assistant'))
  const latest = useRef({ chatTarget, onOpenChat })
  latest.current = { chatTarget, onOpenChat }
  const message = ask?.message
  const workspacePath = ask?.workspacePath
  useEffect(() => {
    if (!scope || !message) return
    return useWorkspacePanelNavigationStore.getState().registerAssistant({ owner: owner.current, scope, workspacePath: workspacePath ?? null, message, getChatTarget: () => latest.current.chatTarget, onOpenChat: () => latest.current.onOpenChat?.() })
  }, [scope, message, workspacePath])
  return !!scope && !!message
}

/** Publish a question for views whose content has no shared page header. */
export function WorkspacePanelAssistant({ workspacePath, message }: { workspacePath: string; message: string }) {
  useWorkspacePanelAssistant({ workspacePath, message })
  return null
}

export function useQuestionInPanelHeader() {
  const { scope } = useContext(WorkspacePanelNavigationContext)
  return useWorkspacePanelNavigationStore(state => !!scope && !!panelBackAction(state.assistants, scope))
}

export function WorkspacePanelAssistantButton({ workspacePath, view, panelScope, onDraftOpened, inMenu = false }: { workspacePath?: string | null; view?: string; panelScope?: string; onDraftOpened?: () => void; inMenu?: boolean }) {
  const registeredScope = useWorkspacePanelScope(workspacePath, view)
  const scope = panelScope ?? registeredScope
  const ask = useWorkspacePanelNavigationStore(state => panelBackAction(state.assistants, scope))
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const currentScope = useRef(scope)
  currentScope.current = scope
  const operation = useRef(0)
  useEffect(() => {
    operation.current++
    setError(null); setBusy(false)
    return () => { operation.current++ }
  }, [scope, ask?.message])
  if (!ask) return null
  const openDraft = async () => {
    const version = ++operation.current
    const identity = captureChatIdentity()
    const current = () => operation.current === version && currentScope.current === scope && isChatIdentityCurrent(identity)
    setBusy(true); setError(null)
    try {
      const target = ask.getChatTarget?.() ?? (ask.workspacePath ? { workspacePath: ask.workspacePath } : undefined)
      if (!target) throw new Error('Open a project chat to ask about this page.')
      const { openWorkspacePaneChatWithDraft } = await import('../../utils/workspacePaneChat')
      if (!current()) return
      await openWorkspacePaneChatWithDraft({ ...target, message: ask.message })
      if (current()) { ask.onOpenChat?.(); onDraftOpened?.() }
    } catch (cause) {
      if (current()) setError(cause instanceof Error ? cause.message : 'Could not open chat.')
    } finally { if (current()) setBusy(false) }
  }
  return <div className="relative shrink-0 max-md:order-3">
    <button type="button" aria-label="Ask AI about this page" title="Ask about this page · Opens a draft in chat" disabled={busy} onClick={() => { void openDraft() }} className={`${inMenu ? 'flex min-h-10 w-full gap-2.5 px-3 text-left text-sm text-foreground' : 'inline-flex h-8 gap-1.5 px-2 text-xs text-muted-foreground max-md:w-8 max-md:justify-center max-md:px-0'} items-center rounded-md font-medium hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50`}><MessageCircle className="h-3.5 w-3.5 shrink-0" aria-hidden="true" /><span className={inMenu ? undefined : 'max-md:sr-only'}>Ask AI</span></button>
    {error && <p role="alert" className={inMenu ? 'px-3 pb-2 text-xs text-destructive' : 'absolute right-0 top-full z-50 mt-2 w-64 rounded-md border border-border bg-popover p-3 text-xs text-destructive shadow-lg'}>{error}</p>}
  </div>
}
