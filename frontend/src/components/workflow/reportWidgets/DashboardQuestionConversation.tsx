import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState, type ComponentProps } from 'react'
import { MessageCircle, X } from 'lucide-react'
import api from '../../../services/api'
import { useAuthStore } from '../../../stores/useAuthStore'
import { openProjectAskConversation } from '../../../utils/projectAskConversation'
import { sendWorkspacePaneMessageToChat } from '../../../utils/workspacePaneChat'
import ModalPortal from '../../ui/ModalPortal'
import { ReportEmbedProvider, useReportDataApi } from './reportEmbedContext'
import { installDashboardChatWidgets, type ChatWidgetSlot } from './dashboardChatWidgets'
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
  const [popup, setPopup] = useState(false)
  const [slots, setSlots] = useState<ChatWidgetSlot[]>([])
  const [authored, setAuthored] = useState(false)
  const data = useReportDataApi()
  const boundRef = useRef(bound)
  boundRef.current = bound
  const [error, setError] = useState<string | null>(null)
  const generation = useRef(0)
  const container = useRef<HTMLDivElement>(null)
  const dialog = useRef<HTMLDialogElement>(null)
  const widgetApi = useRef<ReturnType<typeof installDashboardChatWidgets>["api"] | null>(null)
  const revealWidgets = useRef<(() => void) | null>(null)
  const accepted = useRef(new Map<string, string>())
  const opening = useRef<Promise<string> | null>(null)
  // Native project chat enforces Builder/Run permissions on every request.
  // Code has no project Ask adapter yet.
  const canAsk = Boolean(viewer && capability.can_ask && ['crew', 'workflow', 'relay'].includes(product))
  useEffect(() => {
    generation.current++
    setBound(null); setPopup(false); setSlots([]); setAuthored(false); setError(null)
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
      const previous = boundRef.current
      const id = previous && previous.viewer === viewer && previous.resourceId === resourceId ? previous.tabId : await openProjectAskConversation({ product, projectId, workspacePath })
      if (generation.current !== token) throw new Error('The dashboard account changed.')
      setBound({ tabId: id, viewer: viewer!, resourceId }); setError(null)
      return id
    })().catch(reason => {
      if (generation.current === token) { setBound(null); setError(reason instanceof Error ? reason.message : 'Could not open project Ask.') }
      throw reason
    }).finally(() => { if (generation.current === token) opening.current = null })
    opening.current = request
    return request
  }
  const opener = useRef(openAsk)
  opener.current = openAsk
  const chatWidgets = useCallback((frame: HTMLIFrameElement) => {
    const binding = installDashboardChatWidgets(frame, {
      available: canAsk, container: container.current!, openConversation: () => opener.current(),
      changed: (next, hasWidgets) => { setSlots(next); setAuthored(hasWidgets) }, popup: setPopup, error: setError,
    })
    widgetApi.current = binding.api
    revealWidgets.current = binding.reveal
    return { api: binding.api, dispose: () => { binding.dispose(); if (widgetApi.current === binding.api) { widgetApi.current = null; revealWidgets.current = null } } }
  }, [canAsk, viewer, resourceId])
  const runtime = useMemo(() => data ? { data, chatWidgets } : null, [data, chatWidgets])
  useEffect(() => { if (popup) dialog.current?.showModal() }, [popup])

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
    if (generation.current === token) {
      accepted.current.set(submissionId, message)
      if (revealWidgets.current) revealWidgets.current()
      else setPopup(true)
    }
  }
  const conversation = canAsk && bound && bound.viewer === viewer && bound.resourceId === resourceId
    ? <div className="flex h-full min-h-0 flex-col"><p className="shrink-0 px-2 py-1 text-xs text-muted-foreground">{capability.mode === 'owner' ? 'Your Builder conversation.' : 'Your Run conversation.'}</p><div className="min-h-0 flex-1"><Suspense fallback={<p>Opening chat…</p>}><ChatArea tabId={bound.tabId} compact hideHeader previousChatsWorkspacePath={workspacePath} composerPlaceholder="Ask a question…" /></Suspense></div></div>
    : <p role="status">{canAsk ? error || 'Opening chat…' : 'Chat is unavailable for this dashboard.'}</p>
  return <ReportEmbedProvider value={runtime}>
    <div ref={container} className="relative overflow-hidden">
      <ReportQuestionFrame key={`${viewer}:${resourceId}`} {...frame} allowQuestions={canAsk} submitQuestion={submitQuestion} publication={{ resourceId, revision }} />
      {!authored && canAsk && <button type="button" aria-label="Open dashboard chat" title="Ask" className="absolute left-3 top-3 z-30 inline-flex items-center gap-1 rounded-md border border-border bg-background/95 px-2 py-1 text-sm shadow-sm" onClick={() => void widgetApi.current?.open().catch(reason => setError(reason instanceof Error ? reason.message : 'Could not open chat.'))}>
        <MessageCircle className="h-4 w-4" /> Ask
      </button>}
      {slots.map(slot => <section key={slot.key} aria-label="Dashboard chat widget" aria-hidden={!slot.visible} className="absolute z-30 overflow-hidden bg-background text-foreground" style={{ left: slot.left, top: slot.top, width: slot.width, height: slot.height, clipPath: slot.clipPath, visibility: slot.visible ? 'visible' : 'hidden' }}>{conversation}</section>)}
      {error && <p role="alert" className="absolute bottom-2 left-2 z-40 rounded-md border border-border bg-background px-2 py-1 text-sm text-destructive">{error}</p>}
    </div>
    {popup && canAsk && <ModalPortal><dialog ref={dialog} aria-label="Dashboard chat" onCancel={() => setPopup(false)} className="m-auto h-[min(80vh,42rem)] w-[calc(100%_-_2rem)] max-w-xl overflow-hidden rounded-xl border border-border bg-background p-3 text-foreground shadow-xl backdrop:bg-black/50">
      <div className="flex h-full min-h-0 flex-col">
        <header className="mb-2 flex items-center justify-between"><span>Ask</span><button type="button" aria-label="Close chat" onClick={() => setPopup(false)}><X className="h-4 w-4" /></button></header>
        <div className="min-h-0 flex-1">{conversation}</div>
      </div>
    </dialog></ModalPortal>}
  </ReportEmbedProvider>
}
