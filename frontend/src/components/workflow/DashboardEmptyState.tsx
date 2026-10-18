import { useRef, useState, type ReactNode } from 'react'
import { Loader2, Plus } from 'lucide-react'
import { DashboardStarterPreview } from './DashboardStarterPreview'
import { openWorkspacePaneChatWithDraft } from '../../utils/workspacePaneChat'
import { useChatStore } from '../../stores/useChatStore'

export function DashboardEmptyState({ description = 'Live data, forms, agent actions and visual stories. Describe what your team needs in chat.', action }: { description?: string; action?: ReactNode }) {
  return <div className="mx-auto w-full max-w-lg px-6 py-12 text-center">
    <DashboardStarterPreview />
    <h2 className="text-xl font-semibold tracking-tight">Build your own interactive workspace</h2>
    <p className="mt-3 text-sm leading-6 text-muted-foreground">{description}</p>
    {action && <div className="mt-6 flex justify-center">{action}</div>}
  </div>
}

type DashboardChatTarget = { workspacePath: string } | { tabId: string } | { profileId: string; conversationKey: string }

/** The parent admits authoring; this control only prepares a user-reviewed draft. */
export function DashboardCreateDraftButton({ target }: { target: DashboardChatTarget }) {
  const pending = useRef(false)
  const [busy, setBusy] = useState(false)
  const createDraft = async () => {
    if (pending.current) return
    pending.current = true
    setBusy(true)
    try {
      await openWorkspacePaneChatWithDraft({ ...target, message: 'Help me create a dashboard for this project. Ask me what to include before building it.' })
    } catch (error) {
      useChatStore.getState().addToast(error instanceof Error ? error.message : 'Could not open project chat.', 'error')
    } finally {
      pending.current = false
      setBusy(false)
    }
  }
  return <button type="button" disabled={busy} onClick={() => { void createDraft() }} className="inline-flex items-center gap-2 rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary/90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:opacity-50">
    {busy ? <Loader2 aria-hidden="true" className="h-4 w-4 animate-spin" /> : <Plus aria-hidden="true" className="h-4 w-4" />}Create dashboard
  </button>
}
