import { useEffect, useRef, useState } from 'react'
import { ArrowUp, ChevronDown, Folder, Laptop, Loader2, MessageSquare, Plug, Sparkles } from 'lucide-react'
import api, { agentApi } from '../../services/api'
import { ChatComposerSendButton } from '../../components/chat/ChatComposer'

/** Where a new project goes: a new folder (undefined) or an existing folder on this Mac. */
export type StartFolder = string | undefined

const folderName = (path: string) => path.replace(/\/+$/, '').split('/').pop() || path

/**
 * The start page (#4439), laid out like a Claude desktop chat: the message box sits where a chat's box sits, with where
 * it runs, the folder and the model under it, and a small card of ways to bring existing work in. New project opens it
 * with no dialog; the first message creates the project (named by the server) and its first chat. A project with no
 * chats shows the same page with its own folder and model.
 */
export function StartPage({ mode, place, projectId, projectFolder, canChooseFolder, submitting = false, error, onSend, onImportChat, onImportMcp, onDiscoverSkills }: {
  mode: 'new' | 'project'
  /** Where it runs, as the server says: This Mac, or the server's name. */
  place: string
  /** Mode project: the project, for the model its first chat starts on. */
  projectId?: string
  /** Mode project: the project's own folder name, when it was made from a folder. */
  projectFolder?: string
  canChooseFolder: boolean
  submitting?: boolean
  error?: string | null
  onSend: (text: string, folder: StartFolder) => void
  onImportChat?: () => void
  onImportMcp?: () => void
  onDiscoverSkills?: () => void
}) {
  const [text, setText] = useState('')
  const [folder, setFolder] = useState<StartFolder>()
  const [menu, setMenu] = useState(false)
  const [recent, setRecent] = useState<string[] | null>(null)
  const [typing, setTyping] = useState(false)
  const [typed, setTyped] = useState('')
  const [model, setModel] = useState<{ display_label?: string; reasoning_effort?: string }>()
  const under = useRef<HTMLDivElement>(null)
  const input = useRef<HTMLTextAreaElement>(null)
  useEffect(() => { input.current?.focus() }, [])
  // The server says which CLI and model the first chat starts on.
  useEffect(() => {
    let active = true
    if (mode === 'project' && projectId) void agentApi.getAgentProfileChatModelState('work', projectId).then(data => { if (active) setModel(data.new_chat_model) }).catch(() => {})
    // New project: the model a new project starts on.
    else if (mode === 'new') void api.get('/api/agent-profiles/work/chat-models').then(({ data }) => { if (active) setModel(data?.new_chat_model) }).catch(() => {})
    return () => { active = false }
  }, [mode, projectId])
  useEffect(() => {
    if (!menu) return
    const close = (event: MouseEvent) => { if (!under.current?.contains(event.target as Node)) setMenu(false) }
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') setMenu(false) }
    document.addEventListener('mousedown', close)
    document.addEventListener('keydown', escape)
    return () => { document.removeEventListener('mousedown', close); document.removeEventListener('keydown', escape) }
  }, [menu])
  // Recent folders are the folders of this Mac's recent Claude and Codex chats.
  useEffect(() => {
    if (!menu || recent !== null) return
    const abort = new AbortController()
    void api.get('/api/agent-profiles/work/local-project', { params: { all_folders: true, sessions: true }, signal: abort.signal })
      .then(({ data }) => { if (!abort.signal.aborted) setRecent([...new Set<string>((data.sessions || []).map((chat: { folder?: string }) => chat.folder).filter(Boolean))].slice(0, 6)) })
      .catch(() => { if (!abort.signal.aborted) setRecent([]) })
    return () => abort.abort()
  }, [menu, recent])
  const send = () => { if (text.trim() && !submitting) onSend(text.trim(), folder) }
  const choose = (next: StartFolder) => { setFolder(next); setMenu(false); setTyping(false); input.current?.focus() }
  const browse = async () => {
    const pick = window.electronAPI?.pickWorkflowFolder
    if (!pick) { setTyping(true); return }
    try { const picked = await pick(); if (picked) choose(picked) } catch { setTyping(true) }
  }
  const pill = 'inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-sm text-muted-foreground'
  const item = 'flex w-full items-center gap-2.5 rounded-lg px-3 py-2 text-left text-sm text-foreground hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
  const sub = 'block text-xs text-muted-foreground'
  const imports = [
    onImportChat && { key: 'chat', icon: MessageSquare, label: 'Import a chat', line: 'From Claude Code, Codex or Cursor on this Mac', run: onImportChat },
    onImportMcp && { key: 'mcp', icon: Plug, label: 'Import MCP servers', line: 'From your Claude, Codex, Cursor and agy settings', run: onImportMcp },
    onDiscoverSkills && { key: 'skills', icon: Sparkles, label: 'Discover & import skills', line: 'From your Claude and Codex folders', run: onDiscoverSkills },
  ].filter(Boolean) as { key: string; icon: typeof Plug; label: string; line: string; run: () => void }[]
  return <div className="flex h-full min-h-0 w-full flex-col text-left" data-testid="work-start-page">
    <div className="flex min-h-0 flex-1 items-center justify-center overflow-y-auto p-6">
      {imports.length > 0 && <div className="w-full max-w-sm rounded-xl border border-border bg-background p-1.5 shadow-sm" aria-label="Bring existing work">
        {imports.map(entry => <button key={entry.key} type="button" onClick={entry.run} className={item}><entry.icon className="h-4 w-4 shrink-0 text-muted-foreground" /><span>{entry.label}…<span className={sub}>{entry.line}</span></span></button>)}
      </div>}
    </div>
    <div className="mx-auto w-full max-w-3xl shrink-0 px-4 pb-4">
      <form data-chat-composer-card="" onSubmit={event => { event.preventDefault(); send() }} className="flex items-end gap-2 rounded-2xl border border-border bg-background py-2 pl-4 pr-2 shadow-sm">
        <textarea ref={input} value={text} onChange={event => setText(event.target.value)} rows={1} disabled={submitting}
          onKeyDown={event => { if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) { event.preventDefault(); send() } }}
          placeholder="Describe a task or ask a question" aria-label="Your first message" data-testid="work-first-chat-input"
          className="max-h-60 min-h-8 flex-1 resize-none bg-transparent py-1.5 text-[15px] text-foreground outline-none [field-sizing:content] placeholder:text-muted-foreground" />
        <ChatComposerSendButton type="submit" aria-label="Send" disabled={!text.trim() || submitting}
          className="h-8 w-8 shrink-0 rounded-full">{submitting ? <Loader2 className="h-4 w-4 animate-spin" /> : <ArrowUp className="h-4 w-4" />}</ChatComposerSendButton>
      </form>
      <div ref={under} className="relative mt-1.5 flex flex-wrap items-center gap-1">
        <span className={pill} title={place === 'This Mac' ? 'Runs on this Mac while it is on and awake' : `Runs on ${place}`}><Laptop className="h-3.5 w-3.5" />{place}</span>
        {mode === 'project' ? projectFolder && <span className={pill} title="This project's folder"><Folder className="h-3.5 w-3.5" />{projectFolder}</span>
          : canChooseFolder && <button type="button" aria-expanded={menu} onClick={() => setMenu(!menu)} className={`${pill} hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring`} data-testid="work-start-folder" title={folder || 'A new, empty folder for this project'}><Folder className="h-3.5 w-3.5" />{folder ? folderName(folder) : 'New folder'}<ChevronDown className="h-3 w-3" /></button>}
        {model?.display_label && <span className={`${pill} ml-auto`} title="The model a new chat starts on; change it in the chat's message box">{model.display_label}{model.reasoning_effort && <span className="capitalize">{model.reasoning_effort}</span>}</span>}
        {menu && <div role="menu" className="absolute bottom-full left-0 z-30 mb-1.5 w-72 rounded-xl border border-border bg-popover p-1.5 text-popover-foreground shadow-xl">
          <button type="button" role="menuitem" className={item} onClick={() => choose(undefined)}><span>New folder<span className={sub}>An empty folder for this project</span></span></button>
          <div className="mx-2 my-1 border-t border-border" />
          <p className="px-3 pb-1 pt-1 text-xs text-muted-foreground">Recent folders</p>
          {recent === null ? <p className="px-3 py-2 text-xs text-muted-foreground"><Loader2 className="mr-1.5 inline h-3 w-3 animate-spin" />Looking…</p>
            : recent.length === 0 ? <p className="px-3 py-2 text-xs text-muted-foreground">None yet</p>
            : recent.map(path => <button key={path} type="button" role="menuitem" className={item} onClick={() => choose(path)} title={path}><span className="min-w-0">{folderName(path)}<span className={`${sub} truncate`}>{path}</span></span></button>)}
          <div className="mx-2 my-1 border-t border-border" />
          {typing ? <form className="flex gap-1.5 p-1" onSubmit={event => { event.preventDefault(); if (typed.trim()) choose(typed.trim()) }}>
            <input autoFocus value={typed} onChange={event => setTyped(event.target.value)} aria-label="Folder path" placeholder="Paste a folder path" className="min-w-0 flex-1 rounded-md border border-border bg-background px-2 py-1.5 text-sm outline-none focus:ring-2 focus:ring-ring" />
            <button type="submit" className="rounded-md bg-primary px-2.5 text-sm text-primary-foreground">Use</button>
          </form> : <button type="button" role="menuitem" className={item} onClick={() => void browse()}>Choose a folder…</button>}
        </div>}
      </div>
      {error && <p role="alert" className="mt-2 text-sm text-destructive">{error}</p>}
    </div>
  </div>
}
