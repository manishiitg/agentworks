import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState, type ComponentProps, type CSSProperties } from 'react'
import { MessageCircle, X, Zap } from 'lucide-react'
import api from '../../../services/api'
import { useAuthStore } from '../../../stores/useAuthStore'
import { openProjectAskConversation } from '../../../utils/projectAskConversation'
import { sendWorkspacePaneMessageToChat } from '../../../utils/workspacePaneChat'
import ModalPortal from '../../ui/ModalPortal'
import { WorkingTipsContext } from '../../chat/workingTipsContext'
import type { CommandDefinition } from '../../../commands'
import { ReportEmbedProvider, useReportDataApi } from './reportEmbedContext'
import { installDashboardChatWidgets, type ChatWidgetSlot, type ReportTheme } from './dashboardChatWidgets'
import { installDashboardActionButtons, type ActionRequest } from './reportActionButtons'
import { ReportActionDialog } from './ReportActionDialog'
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
  const [action, setAction] = useState<ActionRequest | null>(null)
  const [theme, setTheme] = useState<ReportTheme>({ vars: {} })
  const modeClass = (mode?: string) => mode === 'light' ? 'force-light' : mode === 'dark' ? 'dark' : ''
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
  // Merged Crew projects retain their work/code origin for native conversation routing.
  const canAsk = Boolean(viewer && capability.can_ask && ['crew', 'code', 'workflow', 'relay'].includes(product))
  // Function buttons run the project's published functions as this person; the
  // server resolves the project from the dashboard record, including either Crew origin.
  const canUseFunctions = Boolean(viewer && capability.can_view && ['crew', 'code', 'workflow'].includes(product))
  // "/" in this chat lists the project's published functions (not the person's own
  // commands); choosing one opens the same form as a function button.
  const [functionList, setFunctionList] = useState<{ name: string; description?: string }[]>([])
  const chatOpen = Boolean(bound)
  useEffect(() => {
    if (!canUseFunctions || !chatOpen) { setFunctionList([]); return }
    let cancelled = false
    void (async () => {
      try {
        const response = await api.get<{ functions?: { name: string; description?: string }[] }>(`/api/dashboards/${encodeURIComponent(resourceId)}/functions`)
        if (!cancelled) setFunctionList(response?.data?.functions ?? [])
      } catch {
        if (!cancelled) setFunctionList([])
      }
    })()
    return () => { cancelled = true }
  }, [canUseFunctions, chatOpen, resourceId, viewer])
  const slashCommands = useMemo<CommandDefinition[]>(() => functionList.map(fn => ({
    command: fn.name,
    description: (fn.description || 'Run this function').split('\n')[0].slice(0, 120),
    icon: <Zap className="h-3.5 w-3.5" />,
    source: 'product' as const,
    execute: () => setAction({ name: fn.name, label: fn.name.replace(/_/g, ' ') }),
  })), [functionList])
  useEffect(() => {
    generation.current++
    setBound(null); setPopup(false); setSlots([]); setAuthored(false); setError(null); setAction(null)
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
      const id = previous && previous.viewer === viewer && previous.resourceId === resourceId ? previous.tabId : await openProjectAskConversation({ product, projectId, workspacePath }, { activate: false })
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
      changed: (next, hasWidgets) => { setSlots(next); setAuthored(hasWidgets) }, popup: setPopup, error: setError, themeChanged: setTheme,
    })
    const buttons = installDashboardActionButtons(frame, { available: canUseFunctions, open: setAction })
    if (canUseFunctions) {
      void (async () => {
        try {
          const { data: listed } = await api.get<{ functions?: { name: string; can_call?: boolean }[] }>(`/api/dashboards/${encodeURIComponent(resourceId)}/functions`)
          buttons.setBlocked((listed?.functions ?? []).filter(fn => fn.can_call === false).map(fn => fn.name))
        } catch { /* the form still says so if a function is refused */ }
      })()
    }
    widgetApi.current = binding.api
    revealWidgets.current = binding.reveal
    return { api: binding.api, dispose: () => { binding.dispose(); buttons.dispose(); if (widgetApi.current === binding.api) { widgetApi.current = null; revealWidgets.current = null } } }
  }, [canAsk, canUseFunctions, viewer, resourceId])
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
    ? <div className="flex h-full min-h-0 flex-col"><div className="min-h-0 flex-1"><WorkingTipsContext.Provider value={false}><Suspense fallback={<p>Opening chat…</p>}><ChatArea tabId={bound.tabId} compact hideHeader previousChatsWorkspacePath={workspacePath} landingContent={<div />} slashCommands={slashCommands} composerPlaceholder="Ask a question…" /></Suspense></WorkingTipsContext.Provider></div></div>
    : <p role="status">{canAsk ? error || 'Opening chat…' : 'Chat is unavailable for this dashboard.'}</p>
  return <ReportEmbedProvider value={runtime}>
    <div ref={container} className="relative overflow-hidden">
      <ReportQuestionFrame key={`${viewer}:${resourceId}`} {...frame} allowQuestions={canAsk} submitQuestion={submitQuestion} publication={{ resourceId, revision }} />
      {!authored && canAsk && <button type="button" aria-label="Open dashboard chat" title="Ask" className="absolute left-3 top-3 z-30 inline-flex items-center gap-1 rounded-md border border-border bg-background/95 px-2 py-1 text-sm shadow-sm" onClick={() => void widgetApi.current?.open().catch(reason => setError(reason instanceof Error ? reason.message : 'Could not open chat.'))}>
        <MessageCircle className="h-4 w-4" /> Ask
      </button>}
      {slots.map(slot => <section key={slot.key} aria-label="Dashboard chat widget" aria-hidden={!slot.visible} data-report-theme={slot.palette} className={`absolute z-30 overflow-hidden bg-background text-foreground ${modeClass(slot.mode)}`} style={{ ...slot.vars, left: slot.left, top: slot.top, width: slot.width, height: slot.height, clipPath: slot.clipPath, visibility: slot.visible ? 'visible' : 'hidden' } as CSSProperties}>{conversation}</section>)}
      {error && <p role="alert" className="absolute bottom-2 left-2 z-40 rounded-md border border-border bg-background px-2 py-1 text-sm text-destructive">{error}</p>}
    </div>
    {popup && canAsk && <ModalPortal><dialog ref={dialog} aria-label="Dashboard chat" style={theme.vars as CSSProperties} onCancel={() => setPopup(false)} className={`${modeClass(theme.mode)} m-auto h-[min(80vh,42rem)] w-[calc(100%_-_2rem)] max-w-xl overflow-hidden rounded-xl border border-border bg-background p-3 text-foreground shadow-xl backdrop:bg-black/50`}>
      <div className="flex h-full min-h-0 flex-col">
        <header className="mb-2 flex items-center justify-between"><span>Ask</span><button type="button" aria-label="Close chat" onClick={() => setPopup(false)}><X className="h-4 w-4" /></button></header>
        <div className="min-h-0 flex-1">{conversation}</div>
      </div>
    </dialog></ModalPortal>}
    {action && canUseFunctions && <ReportActionDialog resourceId={resourceId} request={action} theme={theme} onClose={() => setAction(null)} />}
  </ReportEmbedProvider>
}
