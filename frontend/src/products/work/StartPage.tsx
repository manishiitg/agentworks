import { useEffect, useRef, useState } from 'react'
import { ArrowUp, ChevronDown, Folder, Laptop, Loader2, Plus } from 'lucide-react'
import api from '../../services/api'

/** Where a new project goes: a new folder (undefined) or an existing folder on this Mac. */
export type StartFolder = string | undefined

const folderName = (path: string) => path.replace(/\/+$/, '').split('/').pop() || path

/**
 * The start page (#4439): one message box, like a Claude desktop chat. New project opens it with no dialog; the first
 * message creates the project (named by the server from the message, or after its folder) and its first chat. A
 * project with no chats shows the same box with its own folder.
 */
export function StartPage({ mode, place, projectFolder, canChooseFolder, submitting = false, error, onSend, onImportChat, onImportMcp, onDiscoverSkills }: {
  mode: 'new' | 'project'
  /** Where it runs, as the server says: This Mac, or the server's name. */
  place: string
  /** A project's own folder name (mode project). */
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
  const [menu, setMenu] = useState<'plus' | 'folder' | null>(null)
  const [recent, setRecent] = useState<string[] | null>(null)
  const [typing, setTyping] = useState(false)
  const [typed, setTyped] = useState('')
  const root = useRef<HTMLDivElement>(null)
  const input = useRef<HTMLTextAreaElement>(null)
  useEffect(() => { input.current?.focus() }, [])
  useEffect(() => {
    if (!menu) return
    const close = (event: MouseEvent) => { if (!root.current?.contains(event.target as Node)) setMenu(null) }
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') setMenu(null) }
    document.addEventListener('mousedown', close)
    document.addEventListener('keydown', escape)
    return () => { document.removeEventListener('mousedown', close); document.removeEventListener('keydown', escape) }
  }, [menu])
  // Recent folders are the folders of this Mac's recent Claude and Codex chats.
  useEffect(() => {
    if (menu !== 'folder' || recent !== null) return
    const abort = new AbortController()
    void api.get('/api/agent-profiles/work/local-project', { params: { all_folders: true, sessions: true }, signal: abort.signal })
      .then(({ data }) => { if (!abort.signal.aborted) setRecent([...new Set<string>((data.sessions || []).map((chat: { folder?: string }) => chat.folder).filter(Boolean))].slice(0, 6)) })
      .catch(() => { if (!abort.signal.aborted) setRecent([]) })
    return () => abort.abort()
  }, [menu, recent])
  const send = () => { if (text.trim() && !submitting) onSend(text.trim(), folder) }
  const choose = (next: StartFolder) => { setFolder(next); setMenu(null); setTyping(false); input.current?.focus() }
  const browse = async () => {
    const pick = window.electronAPI?.pickWorkflowFolder
    if (!pick) { setTyping(true); return }
    try { const picked = await pick(); if (picked) choose(picked) } catch { setTyping(true) }
  }
  const pill = 'inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-sm text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
  const item = 'block w-full rounded-lg px-3 py-2 text-left text-sm hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
  const menuBox = 'absolute top-full z-30 mt-1.5 w-72 rounded-xl border border-border bg-popover p-1.5 text-popover-foreground shadow-xl'
  const imports = !!(onImportChat || onImportMcp || onDiscoverSkills)
  return <div ref={root} className="w-full max-w-2xl text-left" data-testid="work-start-page">
    <form onSubmit={event => { event.preventDefault(); send() }} className="relative rounded-2xl border border-border bg-background px-2 py-2 shadow-sm focus-within:ring-2 focus-within:ring-ring">
      <div className="flex items-start gap-1.5">
        {imports && <button type="button" aria-label="Import a chat, MCP servers or skills" aria-expanded={menu === 'plus'} onClick={() => setMenu(menu === 'plus' ? null : 'plus')} className="grid h-8 w-8 shrink-0 place-items-center rounded-full text-muted-foreground hover:bg-muted hover:text-foreground"><Plus className="h-4 w-4" /></button>}
        <textarea ref={input} value={text} onChange={event => setText(event.target.value)} rows={1} disabled={submitting}
          onKeyDown={event => { if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) { event.preventDefault(); send() } }}
          placeholder="Describe a task or ask a question" aria-label="Your first message" data-testid="work-first-chat-input"
          className="max-h-60 min-h-8 flex-1 resize-none bg-transparent py-1.5 text-[15px] text-foreground outline-none [field-sizing:content] placeholder:text-muted-foreground" />
        <button type="submit" aria-label="Send" disabled={!text.trim() || submitting} className="grid h-8 w-8 shrink-0 place-items-center rounded-full bg-primary text-primary-foreground disabled:opacity-40">{submitting ? <Loader2 className="h-4 w-4 animate-spin" /> : <ArrowUp className="h-4 w-4" />}</button>
      </div>
      {menu === 'plus' && <div role="menu" className={`${menuBox} left-0`}>
        {onImportChat && <button type="button" role="menuitem" className={item} onClick={() => { setMenu(null); onImportChat() }}>Import a chat…<span className="block text-xs text-muted-foreground">From Claude Code, Codex or Cursor on this Mac</span></button>}
        {onImportMcp && <button type="button" role="menuitem" className={item} onClick={() => { setMenu(null); onImportMcp() }}>Import MCP servers…</button>}
        {onDiscoverSkills && <button type="button" role="menuitem" className={item} onClick={() => { setMenu(null); onDiscoverSkills() }}>Discover &amp; import skills…</button>}
      </div>}
    </form>
    <div className="relative mt-2 flex flex-wrap items-center gap-1">
      <span className={pill} title={place === 'This Mac' ? 'Runs on this Mac while it is on and awake' : `Runs on ${place}`}><Laptop className="h-3.5 w-3.5" />{place}</span>
      {mode === 'project' ? projectFolder && <span className={pill} title="This project's folder"><Folder className="h-3.5 w-3.5" />{projectFolder}</span>
        : canChooseFolder && <button type="button" aria-expanded={menu === 'folder'} onClick={() => setMenu(menu === 'folder' ? null : 'folder')} className={pill} data-testid="work-start-folder" title={folder || 'A new, empty folder for this project'}><Folder className="h-3.5 w-3.5" />{folder ? folderName(folder) : 'New folder'}<ChevronDown className="h-3 w-3" /></button>}
      {menu === 'folder' && <div role="menu" className={`${menuBox} left-0`}>
        <button type="button" role="menuitem" className={item} onClick={() => choose(undefined)}>New folder<span className="block text-xs text-muted-foreground">An empty folder for this project</span></button>
        <div className="mx-2 my-1 border-t border-border" />
        <p className="px-3 pb-1 pt-1 text-xs text-muted-foreground">Recent folders</p>
        {recent === null ? <p className="px-3 py-2 text-xs text-muted-foreground"><Loader2 className="mr-1.5 inline h-3 w-3 animate-spin" />Looking…</p>
          : recent.length === 0 ? <p className="px-3 py-2 text-xs text-muted-foreground">None yet</p>
          : recent.map(path => <button key={path} type="button" role="menuitem" className={item} onClick={() => choose(path)} title={path}>{folderName(path)}<span className="block truncate text-xs text-muted-foreground">{path}</span></button>)}
        <div className="mx-2 my-1 border-t border-border" />
        {typing ? <form className="flex gap-1.5 p-1" onSubmit={event => { event.preventDefault(); if (typed.trim()) choose(typed.trim()) }}>
          <input autoFocus value={typed} onChange={event => setTyped(event.target.value)} aria-label="Folder path" placeholder="Paste a folder path" className="min-w-0 flex-1 rounded-md border border-border bg-background px-2 py-1.5 text-sm outline-none focus:ring-2 focus:ring-ring" />
          <button type="submit" className="rounded-md bg-primary px-2.5 text-sm text-primary-foreground">Use</button>
        </form> : <button type="button" role="menuitem" className={item} onClick={() => void browse()}>Choose a folder…</button>}
      </div>}
    </div>
    {error && <p role="alert" className="mt-3 text-sm text-destructive">{error}</p>}
  </div>
}
