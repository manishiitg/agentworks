import { useRef, useState, type ReactNode } from 'react'
import { CirclePlay, FileText, LayoutDashboard, Loader2, Plus } from 'lucide-react'
import { DashboardStarterPreview } from './DashboardStarterPreview'
import { openWorkspacePaneChatWithDraft } from '../../utils/workspacePaneChat'
import { useChatStore } from '../../stores/useChatStore'

export function DashboardEmptyState({ description = 'Live data, forms, agent actions and visual stories. Describe what your team needs in chat.', action, onRefresh }: { description?: string; action?: ReactNode; onRefresh?: () => void }) {
  return <div className="mx-auto w-full max-w-lg px-6 py-12 text-center">
    <DashboardStarterPreview />
    <h2 className="text-xl font-semibold tracking-tight">Build your own interactive workspace</h2>
    <p className="mt-3 text-sm leading-6 text-muted-foreground">{description}</p>
    {action && <div className="mt-6 flex justify-center">{action}</div>}
    {onRefresh && <button type="button" onClick={onRefresh} className="mt-4 rounded px-2 py-1 text-xs text-muted-foreground underline underline-offset-4 hover:text-foreground">Check again</button>}
  </div>
}

export type DashboardChatTarget = { workspacePath: string } | { tabId: string } | { profileId: string; conversationKey: string }

const creationDrafts = {
  dashboard: { label: 'Create dashboard', icon: LayoutDashboard, message: 'Help me create a dashboard for this project. Ask me what to include before building it.' },
  motion: { label: 'Create motion dashboard', icon: CirclePlay, message: 'Help me create a motion dashboard for this project: an interactive chart or visual explanation with Play, Pause and a timeline I can scrub. Help me choose which real data or process to show. Keep the design editable, and offer MP4 export if I need a video.' },
  artifact: { label: 'Create artifact', icon: FileText, message: 'Help me create an artifact for this project. Ask me what I want to make before building it.' },
}

/** The parent admits authoring; this control only prepares a user-reviewed draft. */
export function DashboardCreateDraftButton({ target, kind = 'dashboard', compact = false }: { target: DashboardChatTarget; kind?: keyof typeof creationDrafts; compact?: boolean }) {
  const { label, icon: Icon, message } = creationDrafts[kind]
  const pending = useRef(false)
  const [busy, setBusy] = useState(false)
  const createDraft = async () => {
    if (pending.current) return
    pending.current = true
    setBusy(true)
    try {
      await openWorkspacePaneChatWithDraft({ ...target, message })
    } catch (error) {
      useChatStore.getState().addToast(error instanceof Error ? error.message : 'Could not open project chat.', 'error')
    } finally {
      pending.current = false
      setBusy(false)
    }
  }
  return <button type="button" aria-label={label} title={label} disabled={busy} onClick={() => { void createDraft() }} className={compact ? "inline-flex h-7 w-7 items-center justify-center rounded-md border border-border text-muted-foreground hover:bg-background/50 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50" : "inline-flex items-center gap-2 rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary/90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:opacity-50"}>
    {busy ? <Loader2 aria-hidden="true" className="h-4 w-4 animate-spin" /> : compact ? <Icon aria-hidden="true" className="h-3.5 w-3.5" /> : <Plus aria-hidden="true" className="h-4 w-4" />}{!compact && label}
  </button>
}
