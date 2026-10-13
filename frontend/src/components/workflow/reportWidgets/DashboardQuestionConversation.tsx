import { lazy, Suspense, useEffect, useRef, useState, type ComponentProps } from 'react'
import { MessageCircle } from 'lucide-react'
import api from '../../../services/api'
import { useAuthStore } from '../../../stores/useAuthStore'
import { openProjectAskConversation } from '../../../utils/projectAskConversation'
import { sendWorkspacePaneMessageToChat } from '../../../utils/workspacePaneChat'
import { ReportQuestionFrame } from './ReportQuestionFrame'
import { reportContentQuestion, type ReportContentContext } from './reportContentQuestion'

const ChatArea = lazy(() => import('../../ChatArea'))
export type DashboardQuestionCapability = { can_view: boolean; can_ask: boolean; mode?: string; reason?: string }
type Props = ComponentProps<typeof ReportQuestionFrame> & {
  resourceId: string; revision: string; projectId: string; workspacePath: string
  capability: DashboardQuestionCapability; product: string
}

export function DashboardQuestionConversation({ resourceId, revision, projectId, workspacePath, capability, product, ...frame }: Props) {
  const viewer = useAuthStore(state => state.user?.id)
  const [bound, setBound] = useState<{ tabId: string; viewer: string; resourceId: string } | null>(null)
  const [open, setOpen] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const generation = useRef(0)
  const panel = useRef<HTMLDivElement>(null)
  const accepted = useRef(new Map<string, string>())
  const opening = useRef<Promise<string> | null>(null)
  // Workflow readers and Relays need shared Ask parity and published-result
  // checks before this entry point can expose their conversation here.
  const canAsk = Boolean(viewer && capability.can_ask && (product === 'crew' || (product === 'workflow' || product === 'relay') && capability.mode === 'owner'))
  useEffect(() => {
    generation.current++
    setBound(null); setOpen(false); setError(null)
    accepted.current.clear(); opening.current = null
    return () => { generation.current++ }
  }, [viewer, resourceId])

  const openAsk = async () => {
    if (!canAsk) throw new Error('Ask is unavailable for this dashboard.')
    if (opening.current) return opening.current
    const token = generation.current
    const request = (async () => {
      const { data } = await api.get<{ project_id: string; product: string; capability: DashboardQuestionCapability }>(`/api/dashboards/${encodeURIComponent(resourceId)}`)
      if (generation.current !== token) throw new Error('The dashboard account changed.')
      if (!data.capability.can_ask || data.project_id !== projectId || data.product !== product) throw new Error('Ask is no longer available for this dashboard.')
      const id = await openProjectAskConversation({ product, projectId, workspacePath })
      if (generation.current !== token) throw new Error('The dashboard account changed.')
      setBound({ tabId: id, viewer: viewer!, resourceId }); setOpen(true); setError(null)
      return id
    })().catch(reason => {
      if (generation.current === token) { setBound(null); setError(reason instanceof Error ? reason.message : 'Could not open project Ask.') }
      throw reason
    }).finally(() => { if (generation.current === token) opening.current = null })
    opening.current = request
    return request
  }
  useEffect(() => { if (open) panel.current?.scrollIntoView?.({ block: 'nearest', behavior: 'smooth' }) }, [open])

  const submitQuestion = async (question: string, context: ReportContentContext, submissionId: string) => {
    const message = reportContentQuestion(question, frame.title, frame.documentPath, workspacePath, context, { resourceId, revision })
    const previous = accepted.current.get(submissionId)
    if (previous) {
      if (previous !== message) throw new Error('This question was already submitted with different context.')
      return
    }
    const token = generation.current
    const id = await openAsk()
    if (generation.current !== token) throw new Error('The dashboard account changed.')
    await sendWorkspacePaneMessageToChat({ tabId: id, message, notificationId: submissionId })
    if (generation.current === token) accepted.current.set(submissionId, message)
  }
  return <>
    <ReportQuestionFrame {...frame} allowQuestions={canAsk} submitQuestion={submitQuestion} publication={{ resourceId, revision }} />
    <div ref={panel} className="m-3 rounded-lg border border-border bg-background p-3">
      <button type="button" disabled={!canAsk} onClick={() => void openAsk().catch(() => {})} className="flex items-center gap-2 text-sm font-medium disabled:opacity-50">
        <MessageCircle className="h-4 w-4" /> Open project Ask
      </button>
      {!canAsk && <p className="mt-2 text-xs text-muted-foreground">{capability.reason === 'ask_disabled' ? 'This Crew has questions turned off.' : 'Ask is unavailable for this dashboard.'}</p>}
      {error && <p role="alert" className="mt-2 text-sm text-destructive">{error}</p>}
      {open && canAsk && bound && bound.viewer === viewer && bound.resourceId === resourceId && <section aria-label="Project Ask conversation" className="mt-3">
        <p className="mb-2 text-xs text-muted-foreground">Your existing project conversation. Questions use your account and the project’s normal Ask permissions.</p>
        <div className="h-[min(65vh,40rem)] min-h-80"><Suspense fallback={<p>Opening project Ask…</p>}><ChatArea tabId={bound.tabId} compact hideHeader inputVariant="product" previousChatsWorkspacePath={workspacePath} composerPlaceholder="Ask a follow-up…" /></Suspense></div>
      </section>}
    </div>
  </>
}
