// Child Mode on the platform backend: the same AgentWorks ChatArea the parent
// side hosts, opened on the child's own conversation for one activity. Streaming,
// tool chips, restore and scrolling are the shared code; what is the child's
// own — the celebration row, the inline scene, the kickoff after a handoff —
// is added around it here, the way the parent adds its pills.
import { useEffect, useRef, useState, type ReactNode } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { Star } from 'lucide-react'
import ChatArea, { type ChatContentRendererProps } from '../../../components/ChatArea'
import { TerminalEventTranscript } from '../../../components/TerminalEventTranscript'
import ToastHost from '../../../components/ui/ToastHost'
import { agentApi } from '../../../services/api'
import { useChatStore, waitForChatStoreHydration } from '../../../stores/useChatStore'
import { useModeStore } from '../../../stores/useModeStore'
import { useAppStore } from '../../../stores/useAppStore'
import { hydrateTabEvents, restoreSession } from '../../../utils/sessionRestore'
import { setProductCommands } from '../../../commands/registry'
import { usePresentationEvents } from '../../../platform/presentations/usePresentationEvents'
import { ProductSuggestions } from '../../../platform/chat/ProductSuggestions'
import type { ProductInteraction as TranscriptInteraction } from '../../../../shared/session/interactions'
import type { QuickCommand } from '../stores/types'
import { api } from '../api'
import { toProductCommandDefinitions } from './productCommands'
import { FAMILY_WORKSPACE, familyRuntime, type ProductPresentation } from './PlatformChat'
import { readReminderSoundPref, playReminderChime } from '../notifySound'

export const CHILD_PROFILE_ID = 'sparkquill-child'
export const CHILD_PROFILE_VERSION = 1

type ProfileDeclaration = { tools?: { presentation?: { kind?: string }; interaction?: { kind?: string; render?: string } }[] }

// What the child profile's tool bindings declared for the chat
// (product.yaml `interaction: { kind, render }`), by rendering.
type Renders = { suggestions: string | null; celebration: string | null; scene: string | null }
let renders: Renders = { suggestions: null, celebration: null, scene: null }
let sceneRenderer: ((html: string) => ReactNode) | null = null
// The active renderer's submit, so the activity page's own buttons (SQ.choose)
// and a handoff kickoff can speak into the chat.
let activeSubmit: ((text: string) => void) | null = null
// One tab per activity for this page load (keyed by the activity's slug).
const openedTabs = new Map<string, Promise<{ tabId: string; sessionId: string }>>()
// Activities whose conversation was just reset: open the key's new session
// instead of the tab that still points at the old one.
const freshSlugs = new Set<string>()

/** After "Start fresh": drop what this page knows about the activity's chat so it opens the new conversation. */
export function forgetChildChat(dir: string): void {
  const slug = activitySlug(dir)
  openedTabs.delete(slug)
  freshSlugs.add(slug)
}

/** Sends text into the child's chat as if she typed it. False when no chat is open. */
export function submitToChildChat(text: string): boolean {
  if (!activeSubmit) return false
  activeSubmit(text)
  return true
}

export function activitySlug(dir: string): string {
  return String(dir ?? '').replace(/\/+$/, '').split('/').pop() ?? ''
}

function readRenders(profile: ProfileDeclaration | null): Renders {
  const byRender = (render: string) => (profile?.tools ?? []).find((t) => t.interaction?.render === render)?.interaction?.kind ?? null
  return { suggestions: byRender('chat.suggestions'), celebration: byRender('chat.celebration'), scene: byRender('chat.scene') }
}

function Celebration({ stars, reason }: { stars: number; reason: string }) {
  return (
    <div className="fl-celebration" role="status">
      <span className="fl-celebration-stars">
        {Array.from({ length: Math.max(1, Math.min(3, stars)) }, (_, i) => (
          <Star key={i} className="fl-celebration-star" size={20} fill="currentColor" strokeWidth={1} style={{ animationDelay: `${i * 0.12}s` }} />
        ))}
      </span>
      <span className="fl-celebration-text">{reason}</span>
    </div>
  )
}

function renderProductRow(it: TranscriptInteraction): ReactNode {
  if (renders.celebration && it.kind === renders.celebration) {
    return <Celebration stars={Number(it.payload.stars ?? 1)} reason={String(it.payload.reason ?? '')} />
  }
  if (renders.scene && it.kind === renders.scene && sceneRenderer) {
    return <div className="fl-platform-scene">{sceneRenderer(String(it.payload.html ?? ''))}</div>
  }
  return null
}

function ChildConversation({ events, isStreaming, isRestoring, streamingText, streamingStatus, hasOlder, loadingOlder, historyError, onLoadOlder, onSubmitQuery }: ChatContentRendererProps) {
  useEffect(() => {
    activeSubmit = onSubmitQuery ?? null
    return () => { if (activeSubmit === onSubmitQuery) activeSubmit = null }
  }, [onSubmitQuery])
  // Chime when Quill's reply finishes — a true-to-false isStreaming edge,
  // never while history is still restoring (that toggles isStreaming for
  // reasons that are not a fresh reply landing).
  const wasStreamingRef = useRef(false)
  useEffect(() => {
    if (isRestoring) { wasStreamingRef.current = isStreaming; return }
    if (wasStreamingRef.current && !isStreaming && readReminderSoundPref()) playReminderChime()
    wasStreamingRef.current = isStreaming
  }, [isStreaming, isRestoring])
  const kinds = [renders.celebration, renders.scene].filter((k): k is string => Boolean(k))
  return (
    <div className="fl-platform-transcript">
      <TerminalEventTranscript
        events={events}
        terminal={null}
        loading={isRestoring}
        error={historyError}
        streamingText={streamingText}
        streamingStatus={streamingStatus}
        hasOlder={hasOlder}
        loadingOlder={loadingOlder}
        onLoadOlder={onLoadOlder}
        onRetry={onLoadOlder}
        surfaceClassName="fl-platform-surface"
        assistantLabel="Quill"
        assistantIcon={<img src="/sparkquill-mark.svg" alt="" width={16} height={16} />}
        productRows={{ kinds, render: renderProductRow }}
      />
      {isStreaming && !streamingText && (
        <div className="fl-thinking fl-platform-working"><img src="/sparkquill-loader.svg" alt="" width={30} height={30} /><span>{streamingStatus ? `Quill is: ${streamingStatus}…` : 'Working on it…'}</span></div>
      )}
      {onSubmitQuery && renders.suggestions && <ProductSuggestions events={events} kind={renders.suggestions} onSubmit={onSubmitQuery} hidden={isStreaming} />}
    </div>
  )
}

export type ChildKickoff = { id: number; dir: string; text: string }

type Props = {
  activityDir: string
  title: string
  childName: string
  theme: 'light' | 'dark'
  commands: QuickCommand[]
  /** A handoff's opening message, sent in the child's voice once this activity's chat is open. It shows like any message. */
  kickoff: ChildKickoff | null
  onKickoffSent: (id: number) => void
  onPresentation: (p: ProductPresentation) => void
  renderScene: (html: string) => ReactNode
}

const queryClient = new QueryClient()

export default function ChildPlatformChat({ activityDir, title, childName, theme, commands, kickoff, onKickoffSent, onPresentation, renderScene }: Props) {
  const [ready, setReady] = useState<{ tabId: string; sessionId: string; dir: string } | null>(null)
  const [presentationKinds, setPresentationKinds] = useState<string[]>([])
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    document.documentElement.classList.remove('light', 'dark')
    document.documentElement.classList.add(theme)
  }, [theme])

  useEffect(() => {
    setProductCommands(toProductCommandDefinitions(commands))
    return () => setProductCommands([])
  }, [commands])

  useEffect(() => { sceneRenderer = renderScene }, [renderScene])

  useEffect(() => {
    let cancelled = false
    const slug = activitySlug(activityDir)
    if (!slug) return
    const openTab = async () => {
      await api.ensureSession()
      useModeStore.getState().setModeCategory('multi-agent')
      useAppStore.getState().setAgentMode('multi-agent')
      await waitForChatStoreHydration()
      const chatStore = useChatStore.getState()
      const fresh = freshSlugs.delete(slug)
      const existing = fresh ? undefined : Object.values(chatStore.chatTabs).find((tab) => tab.metadata?.agentProfileId === CHILD_PROFILE_ID && tab.metadata?.agentProfileConversationKey === slug)
      const profile = await agentApi.getAgentProfile(CHILD_PROFILE_ID).catch(() => null) as ProfileDeclaration | null
      renders = readRenders(profile)
      setPresentationKinds((profile?.tools ?? []).map((t) => t.presentation?.kind).filter((k): k is string => typeof k === 'string' && k.length > 0))
      const [conversation, runtime] = await Promise.all([
        agentApi.resolveAgentProfileConversation(CHILD_PROFILE_ID, { conversation_key: slug }, existing?.sessionId ?? undefined),
        familyRuntime('child'),
      ])
      const createdTabId = await chatStore.createChatTab(title, {
        mode: 'multi-agent',
        agentProfileId: CHILD_PROFILE_ID,
        agentProfileVersion: CHILD_PROFILE_VERSION,
        agentProfileWorkspace: `${FAMILY_WORKSPACE}/${activityDir.replace(/^\/+|\/+$/g, '')}`,
        agentProfileChatContract: 'profile-v1',
        agentProfileEngine: runtime.engine,
        agentProfileModelID: runtime.model,
        agentProfileConnectionID: runtime.connectionId,
        agentProfileReasoningEffort: runtime.reasoningEffort,
        agentProfileConversationKey: conversation.conversation_key,
        agentProfileConversationId: conversation.conversation_id,
      }, conversation.session_id)
      if (!chatStore.getTab(createdTabId)) throw new Error('the conversation tab could not be created')
      const restoredTabId = await restoreSession(conversation.session_id, { title, source: 'sparkquill-child-open', skipConfigRestore: true, workspacePath: FAMILY_WORKSPACE })
      await hydrateTabEvents(conversation.session_id, { workspacePath: FAMILY_WORKSPACE, fallbackToChatHistory: true, preferChatHistory: true })
      return { tabId: restoredTabId, sessionId: conversation.session_id }
    }
    const prepare = async () => {
      let opened = openedTabs.get(slug)
      if (!opened) {
        opened = openTab()
        openedTabs.set(slug, opened)
      }
      const { tabId, sessionId } = await opened
      if (cancelled) return
      useChatStore.getState().switchTab(tabId)
      setReady({ tabId, sessionId, dir: activityDir })
    }
    setReady(null)
    void prepare().catch((err) => {
      openedTabs.delete(slug)
      if (!cancelled) setError(err instanceof Error ? err.message : 'Could not open the conversation.')
    })
    return () => { cancelled = true }
  }, [activityDir, title])

  // The kickoff waits for this activity's chat to be open and its renderer
  // mounted, then goes out as an ordinary message: the first turn is shown
  // like every other one.
  useEffect(() => {
    if (!kickoff || !ready || ready.dir !== kickoff.dir) return
    let tries = 0
    const timer = window.setInterval(() => {
      if (activeSubmit) {
        window.clearInterval(timer)
        activeSubmit(kickoff.text)
        onKickoffSent(kickoff.id)
      } else if (++tries > 100) {
        window.clearInterval(timer)
      }
    }, 100)
    return () => window.clearInterval(timer)
  }, [kickoff, ready, onKickoffSent])

  // Pages Quill opens for the child ride the same stream, through the
  // platform's presentation selector; what was there when the tab opened is
  // history, not something to open again.
  const sessionId = ready?.sessionId
  const presentations = usePresentationEvents(sessionId, presentationKinds)
  const handed = useRef<{ sessionId: string; count: number } | null>(null)
  useEffect(() => {
    if (!sessionId) return
    if (!handed.current || handed.current.sessionId !== sessionId) { handed.current = { sessionId, count: presentations.length }; return }
    for (const p of presentations.slice(handed.current.count)) onPresentation({ kind: p.kind, payload: p.payload })
    handed.current = { sessionId, count: presentations.length }
  }, [sessionId, presentations, onPresentation])

  return (
    <QueryClientProvider client={queryClient}>
      <div className="fl-platform-chat fl-platform-chat-child">
        {error ? (
          <p className="fl-note">Couldn’t open the conversation: {error}</p>
        ) : ready ? (
          <ChatArea
            tabId={ready.tabId}
            onNewChat={() => {}}
            landingContent={null}
            contentRenderer={ChildConversation}
            inputVariant="product"
            hideRuntimeStatus
            composerPlaceholder={`Type your answer or ask for help, ${childName || 'friend'}…`}
          />
        ) : (
          <p className="fl-note">Opening your activity…</p>
        )}
        <ToastHost />
      </div>
    </QueryClientProvider>
  )
}
