import { createContext, useContext, useEffect, useRef, type ReactNode } from 'react'
import { ArrowLeft } from 'lucide-react'
import { useWorkspacePanelNavigationStore, useWorkspacePanelScope, type WorkspacePanelChatTarget } from '../../stores/useWorkspacePanelNavigationStore'
import { WorkspaceViewIconButton } from '../workflow/WorkspaceViewIconButton'
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
