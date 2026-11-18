import { lazy, Suspense, useEffect, useMemo, useState } from 'react'
import { X } from 'lucide-react'
import type { ExecutionInspectionResponse } from '../../services/api-types'
import { agentApi } from '../../services/api'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { inspectionChat } from '../../utils/inspectionConversation'
import ModalPortal from '../ui/ModalPortal'

const ChatTranscript = lazy(() => import('../TerminalEventTranscript').then(module => ({ default: module.TerminalEventTranscript })))

type Subagent = { running: boolean; transcript: ExecutionInspectionResponse['transcript'] }

/** Reads a subagent's conversation, again every 2 s while it runs (#3132). */
export function useSubagentChat(sessionId: string, toolCallId: string) {
  const [data, setData] = useState<Subagent | null>(null)
  const [error, setError] = useState('')
  useEffect(() => {
    let disposed = false
    let timer: ReturnType<typeof setTimeout> | undefined
    const identity = captureChatIdentity()
    setData(null); setError('')
    const load = async () => {
      let again = true
      try {
        const next = await agentApi.getSessionSubagent(sessionId, toolCallId)
        if (disposed || !isChatIdentityCurrent(identity)) return
        setData(next); setError('')
        again = next.running
      } catch (failure) {
        if (disposed || !isChatIdentityCurrent(identity)) return
        const response = (failure as { response?: { status?: number; data?: unknown } }).response
        if (response?.status === 404) {
          setError(typeof response.data === 'string' && response.data.trim() ? response.data.trim() : "This helper's conversation is not available.")
          again = false
        } else setError('The conversation is temporarily unavailable. Retrying…')
      }
      if (again && !disposed) timer = setTimeout(() => { void load() }, 2000)
    }
    void load()
    return () => { disposed = true; clearTimeout(timer) }
  }, [sessionId, toolCallId])
  return { data, error }
}

/** A subagent's conversation beside the chat: live while it runs, read only. */
export function SubagentChatPanel({ sessionId, toolCallId, title, onClose }: { sessionId: string; toolCallId: string; title: string; onClose: () => void }) {
  const { data, error } = useSubagentChat(sessionId, toolCallId)
  const chat = useMemo(() => data ? inspectionChat({ transcript: data.transcript, children: [] } as unknown as ExecutionInspectionResponse) : null, [data])
  useEffect(() => {
    const close = (event: KeyboardEvent) => { if (event.key === 'Escape') onClose() }
    window.addEventListener('keydown', close)
    return () => window.removeEventListener('keydown', close)
  }, [onClose])
  const name = data?.transcript.name || title
  return <ModalPortal>
    <aside role="dialog" aria-label={`Helper: ${name}`} data-testid="subagent-chat-panel" className="fixed inset-y-0 right-0 z-50 flex w-[min(36rem,100vw)] flex-col border-l border-border bg-background shadow-xl">
      <header className="flex shrink-0 items-center gap-2 border-b border-border px-3 py-2">
        <h2 className="min-w-0 flex-1 truncate text-sm font-medium" title={name}>{name}</h2>
        <span className="flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground">
          {data?.running && <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-primary/70" aria-hidden="true" />}
          {data ? (data.running ? 'Working' : data.transcript.status === 'failed' ? 'Failed' : 'Finished') : error ? '' : 'Loading…'}
        </span>
        <button type="button" aria-label="Close helper conversation" onClick={onClose} className="rounded p-1 text-muted-foreground hover:bg-muted"><X className="h-4 w-4" /></button>
      </header>
      <p className="shrink-0 px-3 pt-2 text-[11px] text-muted-foreground">The helper's own conversation. Read only.</p>
      {error && <p role="status" className="px-3 py-2 text-xs text-muted-foreground">{error}</p>}
      {chat && <div className="min-h-0 flex-1"><Suspense fallback={<p className="p-3 text-xs">Loading conversation…</p>}>
        <ChatTranscript events={chat.events} terminal={chat.terminal} scrollKey={`subagent:${sessionId}:${toolCallId}`} surfaceClassName="bg-background" toolLayout="rows" />
      </Suspense></div>}
    </aside>
  </ModalPortal>
}
