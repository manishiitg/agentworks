import { useEffect, useRef, useState } from 'react'
import { ArrowUp, Folder, Laptop, MessageSquare, Plug, Sparkles } from 'lucide-react'
import { agentApi } from '../../services/api'
import { ChatComposerSendButton } from '../../components/chat/ChatComposer'

/**
 * An empty project's start page (#4439), laid out like a Claude desktop chat: the message box sits where a chat's box
 * sits, with where it runs, the project's folder and the first chat's model under it, and a small card of ways to bring
 * existing work in. The first message opens the project's first chat. New project itself is a dialog (owner, 28 Nov).
 */
export function StartPage({ place, projectId, projectFolder, onSend, onImportChat, onImportMcp, onDiscoverSkills }: {
  /** Where it runs, as the server says: This Mac, or the server's name. */
  place: string
  /** The project, for the model its first chat starts on. */
  projectId?: string
  /** The project's own folder name, when it was made from a folder. */
  projectFolder?: string
  onSend: (text: string) => void
  onImportChat?: () => void
  onImportMcp?: () => void
  onDiscoverSkills?: () => void
}) {
  const [text, setText] = useState('')
  const [model, setModel] = useState<{ display_label?: string; reasoning_effort?: string }>()
  const input = useRef<HTMLTextAreaElement>(null)
  useEffect(() => { input.current?.focus() }, [])
  // The server says which CLI and model the first chat starts on.
  useEffect(() => {
    if (!projectId) return
    let active = true
    void agentApi.getAgentProfileChatModelState('work', projectId).then(data => { if (active) setModel(data.new_chat_model) }).catch(() => {})
    return () => { active = false }
  }, [projectId])
  const send = () => { if (text.trim()) onSend(text.trim()) }
  const pill = 'inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-sm text-muted-foreground'
  const item = 'flex w-full items-center gap-2.5 rounded-lg px-3 py-2 text-left text-sm text-foreground hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
  const sub = 'block text-xs text-muted-foreground'
  const imports = [
    onImportChat && { key: 'chat', icon: MessageSquare, label: 'Import a chat', line: projectFolder ? `From ${projectFolder}, in Claude Code, Codex or Cursor` : 'From Claude Code, Codex or Cursor on this Mac', run: onImportChat },
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
        <textarea ref={input} value={text} onChange={event => setText(event.target.value)} rows={1}
          onKeyDown={event => { if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) { event.preventDefault(); send() } }}
          placeholder="Describe a task or ask a question" aria-label="Your first message" data-testid="work-first-chat-input"
          className="max-h-60 min-h-8 flex-1 resize-none bg-transparent py-1.5 text-[15px] text-foreground outline-none [field-sizing:content] placeholder:text-muted-foreground" />
        <ChatComposerSendButton type="submit" aria-label="Send" disabled={!text.trim()}
          className="h-8 w-8 shrink-0 rounded-full"><ArrowUp className="h-4 w-4" /></ChatComposerSendButton>
      </form>
      <div className="mt-1.5 flex flex-wrap items-center gap-1">
        <span className={pill} title={place === 'This Mac' ? 'Runs on this Mac while it is on and awake' : `Runs on ${place}`}><Laptop className="h-3.5 w-3.5" />{place}</span>
        {projectFolder && <span className={pill} title="This project's folder"><Folder className="h-3.5 w-3.5" />{projectFolder}</span>}
        {model?.display_label && <span className={`${pill} ml-auto`} title="The model a new chat starts on; change it in the chat's message box">{model.display_label}{model.reasoning_effort && <span className="capitalize">{model.reasoning_effort}</span>}</span>}
      </div>
    </div>
  </div>
}
