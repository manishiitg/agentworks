import { useEffect, useRef, useState } from 'react'
import { ArrowRight, Loader2, RefreshCw, X } from 'lucide-react'
import ModalPortal from '../../components/ui/ModalPortal'
import api from '../../services/api'

export type LocalChat = { id: string; title: string; provider: string; account: string; updated_at: string; in_use?: boolean }
export const localChatProgram = (provider: string) => ({
  'claude-code': { name: 'Claude', icon: 'claude.ico' },
  'codex-cli': { name: 'Codex', icon: 'codex.png' },
  'cursor-cli': { name: 'Cursor', icon: 'cursor.svg' },
}[provider] || { name: 'Agent', icon: 'codex.png' })
type Preview = { messages: { role: string; text: string }[]; total_messages: number }
export type ImportedLocalChat = { conversation_key: string; conversation_id: string; session_id: string; name: string; provider: string; connection_id: string; engine: string; model_id: string }

type LocalChatPickerProps = { onClose: () => void } & (
  { projectId: string; onImported: (chat: ImportedLocalChat) => Promise<void>; folder?: never; onSelected?: never } |
  { folder: string; onSelected: (chat: LocalChat) => void; projectId?: never; onImported?: never }
)
export function LocalChatPicker({ projectId, folder, onClose, onImported, onSelected }: LocalChatPickerProps) {
  const [chats, setChats] = useState<LocalChat[]>([])
  const [selected, setSelected] = useState('')
  const [preview, setPreview] = useState<Preview | null>(null)
  const [loading, setLoading] = useState(true)
  const [previewLoading, setPreviewLoading] = useState(false)
  const [importing, setImporting] = useState(false)
  const [error, setError] = useState('')
  const [refresh, setRefresh] = useState(0)
  const [partial, setPartial] = useState(false)
  const dialog = useRef<HTMLDivElement>(null)
  const current = chats.find(chat => chat.id === selected)
  const params = projectId ? { project_id: projectId } : { root_path: folder }
  const message = (cause: unknown) => {
    const response = (cause as { response?: { data?: { error?: string | { message?: string } } } })?.response?.data?.error
    return typeof response === 'string' ? response : response?.message || 'Could not read your local chats. Try refreshing.'
  }

  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null
    dialog.current?.focus()
    return () => previous?.focus()
  }, [])
  useEffect(() => {
    const abort = new AbortController()
    setLoading(true); setError(''); setPreview(null)
    void api.get('/api/agent-profiles/work/local-project', { params: { ...params, sessions: true }, signal: abort.signal }).then(({ data }) => {
      if (abort.signal.aborted) return
      const found: LocalChat[] = data.sessions || []
      setChats(found); setSelected(id => found.some(chat => chat.id === id) ? id : found[0]?.id || ''); setPartial(!!data.partial)
    }).catch(cause => { if (!abort.signal.aborted) setError(message(cause)) }).finally(() => { if (!abort.signal.aborted) setLoading(false) })
    return () => abort.abort()
  }, [projectId, folder, refresh])
  useEffect(() => {
    if (!selected || loading) return
    const abort = new AbortController()
    setPreviewLoading(true); setPreview(null); setError('')
    void api.get('/api/agent-profiles/work/local-project', { params: { ...params, native_id: selected }, signal: abort.signal }).then(({ data }) => {
      if (!abort.signal.aborted) setPreview(data)
    }).catch(cause => { if (!abort.signal.aborted) setError(message(cause)) }).finally(() => { if (!abort.signal.aborted) setPreviewLoading(false) })
    return () => abort.abort()
  }, [projectId, folder, selected, loading, refresh])
  const continueChat = async () => {
    if (!selected || importing) return
    if (onSelected && current) { onSelected(current); onClose(); return }
    setImporting(true); setError('')
    try {
      const { data } = await api.post<ImportedLocalChat>('/api/agent-profiles/work/local-project', { ...params, id: selected })
      await onImported!(data)
      onClose()
    } catch (cause) { setError(message(cause)) } finally { setImporting(false) }
  }
  return <ModalPortal><div className="fixed inset-0 z-[100] flex items-center justify-center bg-black/60 p-3 sm:p-8">
    <div ref={dialog} role="dialog" aria-modal="true" aria-labelledby="local-chat-title" tabIndex={-1}
      onKeyDown={event => {
        if (event.key === 'Escape' && !importing) { event.stopPropagation(); onClose() }
        if (event.key === 'Tab') {
          const controls = [...(dialog.current?.querySelectorAll<HTMLElement>('button:not(:disabled), [tabindex="0"]') || [])]
          const first = controls[0], last = controls[controls.length - 1]
          if (event.shiftKey && (document.activeElement === first || document.activeElement === dialog.current)) { event.preventDefault(); last?.focus() }
          else if (!event.shiftKey && (document.activeElement === last || document.activeElement === dialog.current)) { event.preventDefault(); first?.focus() }
        }
      }} className="flex max-h-[90dvh] w-full max-w-5xl flex-col rounded-xl border border-border bg-background p-4 shadow-xl sm:p-6">
      <header className="mb-5 flex items-start justify-between gap-3">
        <div><h2 id="local-chat-title" className="text-xl font-semibold">Continue a local chat</h2><p className="mt-1 text-sm text-muted-foreground">Conversations from this project folder</p></div>
        <div className="flex gap-2"><button type="button" disabled={loading || importing} onClick={() => setRefresh(n => n + 1)} className="inline-flex items-center gap-2 rounded-md border border-border px-3 py-2 text-sm disabled:opacity-50"><RefreshCw className="h-4 w-4" />Refresh</button><button type="button" aria-label="Close local chats" disabled={importing} onClick={onClose} className="rounded-md p-2 hover:bg-muted"><X className="h-4 w-4" /></button></div>
      </header>
      {error && <p role="alert" className="mb-3 text-sm text-destructive">{error}</p>}
      <div className="grid min-h-0 flex-1 overflow-auto rounded-lg border border-border sm:grid-cols-[minmax(220px,1fr)_2fr]">
        <div aria-label="Local conversations" className="max-h-48 overflow-y-auto border-b border-border p-2 sm:max-h-[55dvh] sm:border-b-0 sm:border-r">
          {loading ? <p role="status" className="p-3 text-sm text-muted-foreground">Finding local chats…</p> : chats.length === 0 ? <p className="p-3 text-sm text-muted-foreground">No Claude, Codex or Cursor CLI chats found for this folder.</p> : chats.map(chat => <button key={chat.id} type="button" aria-pressed={selected === chat.id} disabled={importing} onClick={() => setSelected(chat.id)} className={`mb-1 flex w-full gap-3 rounded-lg p-3 text-left ${selected === chat.id ? 'bg-muted' : 'hover:bg-muted/50'}`}>
            <img alt="" src={`/provider-icons/${localChatProgram(chat.provider).icon}`} className="mt-0.5 h-5 w-5 shrink-0 object-contain" /><span className="min-w-0"><span className="block truncate text-sm font-medium">{chat.title}</span><span className="mt-1 block text-xs text-muted-foreground">{localChatProgram(chat.provider).name} · {new Date(chat.updated_at).toLocaleDateString()}</span></span>
          </button>)}
          {partial && <p className="p-3 text-xs text-muted-foreground">Some chats could not be checked. Refresh to try again.</p>}
        </div>
        <section aria-label="Conversation preview" className="min-h-48 overflow-y-auto p-4 sm:max-h-[55dvh] sm:p-6">
          {previewLoading ? <p role="status" className="text-sm text-muted-foreground">Loading history…</p> : preview && <><p className="mb-3 text-xs text-muted-foreground">{localChatProgram(current?.provider || '').name} · {current?.account}{preview.total_messages > preview.messages.length ? ` · Latest ${preview.messages.length} messages` : ''}</p>{preview.messages.map((item, index) => <div key={index} className="border-b border-border py-4 last:border-b-0"><p className="mb-2 text-xs text-muted-foreground">{item.role === 'human' || item.role === 'user' ? 'You' : 'Assistant'}</p><p className="whitespace-pre-wrap break-words text-sm">{item.text}</p></div>)}</>}
        </section>
      </div>
      <footer className="mt-5 flex flex-wrap items-center justify-between gap-4"><p className="text-xs text-muted-foreground">{current?.provider === 'cursor-cli' ? `${current.in_use ? 'This chat appears open in Cursor. ' : ''}Continues the same Cursor CLI conversation${onSelected ? ' after creation' : ''}. Close it in Cursor before sending here.` : `${onSelected ? 'A separate copy opens after creation.' : 'Opens a separate chat tab.'} Your original conversation stays unchanged.`}<br />The project files are shared.</p><button type="button" disabled={!preview?.messages.length || loading || previewLoading || importing} onClick={() => void continueChat()} className="inline-flex items-center gap-2 rounded-lg bg-primary px-4 py-2.5 text-sm font-medium text-primary-foreground disabled:opacity-50">{importing ? <Loader2 className="h-4 w-4 animate-spin" /> : null}{onSelected ? 'Use this chat' : 'Continue in AgentWorks'}<ArrowRight className="h-4 w-4" /></button></footer>
    </div>
  </div></ModalPortal>
}
