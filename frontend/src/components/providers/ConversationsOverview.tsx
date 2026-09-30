import { useEffect, useMemo, useState } from 'react'
import { Loader2, RefreshCw, ShieldCheck } from 'lucide-react'
import { agentApi } from '../../services/api'
import type { CodeAdminChat, CodeAdminWorkspace } from '../../services/api-types'

type Conversation = Awaited<ReturnType<typeof agentApi.adminGetCodeChat>>
const workspaceKey = (workspace: CodeAdminWorkspace) => `${workspace.owner_id}/${workspace.id}`
const chatKey = (chat: CodeAdminChat) => `${chat.user_id}/${chat.session_id}`
const controlClass = 'rounded-lg border border-border bg-background px-3 py-2 text-sm text-foreground'

function errorText(cause: unknown): string {
  const response = (cause as { response?: { status?: number; data?: { error?: string } } })?.response
  if (response?.status === 403) return 'Conversation review requires an admin or Code reviewer account.'
  return response?.data?.error || (cause instanceof Error ? cause.message : 'Could not load conversations.')
}

function updatedAt(value?: string): string {
  if (!value || Number.isNaN(Date.parse(value))) return 'No update time'
  return new Date(value).toLocaleString()
}

function Loading() {
  return <p role="status" className="flex items-center gap-2 p-3 text-sm text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" />Loading conversations…</p>
}

function Transcript({ workspace, chat }: { workspace: CodeAdminWorkspace; chat: CodeAdminChat }) {
  const [data, setData] = useState<Conversation | null>(null)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    let cancelled = false
    void agentApi.adminGetCodeChat(workspace.owner_id, workspace.id, chat.session_id, chat.user_id)
      .then(result => { if (!cancelled) setData(result) })
      .catch(cause => { if (!cancelled) setError(errorText(cause)) })
    return () => { cancelled = true }
  }, [workspace.owner_id, workspace.id, chat.session_id, chat.user_id])
  return <section aria-label="Conversation transcript" className="min-w-0 p-4">
    <h3 className="break-words font-semibold text-foreground">{chat.title || 'Untitled chat'}</h3>
    <p className="mb-4 text-xs text-muted-foreground">{chat.username || chat.user_id} · {updatedAt(chat.updated_at)}</p>
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    {!data && !error && <Loading />}
    {data?.conversation_history?.length === 0 && <p className="text-sm text-muted-foreground">This conversation has no recorded messages.</p>}
    {data && !data.conversation_history && <p className="text-sm text-muted-foreground">No transcript is available for this conversation.</p>}
    {data?.conversation_history?.map((message, index) => {
      const role = String(message.Role ?? message.role ?? 'message').toLowerCase()
      const label = role === 'human' || role === 'user' ? 'User' : role === 'ai' || role === 'assistant' ? 'Assistant' : role
      return <article key={index} className="mb-3 rounded-lg border border-border bg-muted/20 p-3">
        <p className="mb-2 text-xs font-semibold capitalize text-muted-foreground">{label}</p>
        {(message.Parts ?? message.parts ?? []).map((part, partIndex) => {
          const text = part.Text ?? part.text
          return typeof text === 'string' && text.trim()
            ? <p key={partIndex} className="whitespace-pre-wrap break-words text-sm text-foreground">{text}</p>
            : <details key={partIndex} className="mt-2 text-xs text-muted-foreground"><summary className="cursor-pointer">Tool or message details</summary><pre className="mt-2 max-h-80 overflow-auto whitespace-pre-wrap break-all">{JSON.stringify(part, null, 2)}</pre></details>
        })}
      </article>
    })}
  </section>
}

function WorkspaceChats({ workspace }: { workspace: CodeAdminWorkspace }) {
  const [chats, setChats] = useState<CodeAdminChat[] | null>(null)
  const [selected, setSelected] = useState<CodeAdminChat | null>(null)
  const [user, setUser] = useState('')
  const [search, setSearch] = useState('')
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    let cancelled = false
    void agentApi.adminListCodeChats(workspace.owner_id, workspace.id)
      .then(result => { if (!cancelled) setChats(result.chats) })
      .catch(cause => { if (!cancelled) setError(errorText(cause)) })
    return () => { cancelled = true }
  }, [workspace.owner_id, workspace.id])
  const users = [...new Map((chats || []).map(chat => [chat.user_id, chat.username || chat.user_id])).entries()]
  const filtered = (chats || []).filter(chat => (!user || chat.user_id === user) &&
    `${chat.title || ''} ${chat.username || ''} ${chat.session_id}`.toLowerCase().includes(search.trim().toLowerCase()))
  return <div>
    <div className="flex flex-wrap items-center gap-2 border-b border-border p-3">
      <p className="mr-auto text-sm font-medium text-foreground">{workspace.title}</p>
      <select aria-label="Conversation user" className={controlClass} value={user} onChange={event => { setUser(event.target.value); setSelected(null) }}>
        <option value="">All conversation users</option>
        {users.map(([id, name]) => <option key={id} value={id}>{name}</option>)}
      </select>
      <input aria-label="Search conversations" placeholder="Search conversations" className={controlClass} value={search} onChange={event => { setSearch(event.target.value); setSelected(null) }} />
    </div>
    {error && <p role="alert" className="p-3 text-sm text-destructive">{error}</p>}
    {!chats && !error && <Loading />}
    {chats && <div className="grid min-w-0 lg:grid-cols-[260px_minmax(0,1fr)]">
      <div aria-label="Conversation list" className="max-h-[65vh] overflow-y-auto border-b border-border p-2 lg:border-b-0 lg:border-r">
        {filtered.length === 0 && <p className="p-2 text-sm text-muted-foreground">No conversations match these filters.</p>}
        {filtered.map(chat => <button key={chatKey(chat)} type="button" aria-pressed={selected !== null && chatKey(selected) === chatKey(chat)} onClick={() => setSelected(chat)} className={`mb-1 block w-full rounded-lg p-3 text-left hover:bg-muted ${selected && chatKey(selected) === chatKey(chat) ? 'bg-primary/10' : ''}`}>
          <span className="block truncate text-sm font-medium text-foreground">{chat.title || 'Untitled chat'}</span>
          <span className="block truncate text-xs text-muted-foreground">{chat.username || chat.user_id} · {chat.message_count} messages</span>
          <span className="block text-xs text-muted-foreground">{updatedAt(chat.updated_at)}</span>
        </button>)}
      </div>
      {selected ? <Transcript key={chatKey(selected)} workspace={workspace} chat={selected} /> : <p className="p-4 text-sm text-muted-foreground">Select a conversation to read its messages.</p>}
    </div>}
  </div>
}

export default function ConversationsOverview() {
  const [workspaces, setWorkspaces] = useState<CodeAdminWorkspace[] | null>(null)
  const [selectedKey, setSelectedKey] = useState('')
  const [owner, setOwner] = useState('')
  const [search, setSearch] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [reload, setReload] = useState(0)
  useEffect(() => {
    let cancelled = false
    setWorkspaces(null)
    setSelectedKey('')
    setError(null)
    void agentApi.adminListCodeWorkspaces()
      .then(result => { if (!cancelled) setWorkspaces(result.workspaces) })
      .catch(cause => { if (!cancelled) setError(errorText(cause)) })
    return () => { cancelled = true }
  }, [reload])
  const owners = useMemo(() => [...new Map((workspaces || []).map(workspace => [workspace.owner_id, workspace.owner_username || workspace.owner_id])).entries()].sort((a, b) => a[1].localeCompare(b[1])), [workspaces])
  const filtered = (workspaces || []).filter(workspace => (!owner || workspace.owner_id === owner) &&
    `${workspace.title} ${workspace.owner_username || ''} ${workspace.id}`.toLowerCase().includes(search.trim().toLowerCase()))
  const selected = filtered.find(workspace => workspaceKey(workspace) === selectedKey)
  return <div className="mx-auto max-w-6xl">
    <div className="mb-5 flex items-start justify-between gap-3">
      <div><h2 className="text-xl font-semibold text-foreground">Conversations</h2><p className="mt-1 text-sm text-muted-foreground">Review users’ Code chats by project and person.</p><p className="mt-2 flex items-center gap-1.5 text-xs text-muted-foreground"><ShieldCheck className="h-4 w-4" />Read-only. Each review is recorded in the audit log.</p></div>
      <button type="button" aria-label="Refresh conversations" className={controlClass} onClick={() => { setSelectedKey(''); setWorkspaces(null); setReload(value => value + 1) }}><RefreshCw className="h-4 w-4" /></button>
    </div>
    <div className="mb-4 flex flex-wrap gap-2">
      <select aria-label="Project owner" className={controlClass} value={owner} onChange={event => { setOwner(event.target.value); setSelectedKey('') }}><option value="">All project owners</option>{owners.map(([id, name]) => <option key={id} value={id}>{name}</option>)}</select>
      <input aria-label="Search projects" placeholder="Search projects" className={`${controlClass} min-w-0 flex-1`} value={search} onChange={event => { setSearch(event.target.value); setSelectedKey('') }} />
    </div>
    {error && <p role="alert" className="mb-3 text-sm text-destructive">{error}</p>}
    {!workspaces && !error && <Loading />}
    {workspaces && <div className="grid min-w-0 overflow-hidden rounded-xl border border-border md:grid-cols-[210px_minmax(0,1fr)]">
      <aside aria-label="Code projects" className="max-h-[75vh] overflow-y-auto border-b border-border p-2 md:border-b-0 md:border-r">
        {filtered.length === 0 && <p className="p-2 text-sm text-muted-foreground">No Code projects match these filters.</p>}
        {filtered.map(workspace => <button key={workspaceKey(workspace)} type="button" aria-pressed={selectedKey === workspaceKey(workspace)} onClick={() => setSelectedKey(workspaceKey(workspace))} className={`mb-1 block w-full rounded-lg p-3 text-left hover:bg-muted ${selectedKey === workspaceKey(workspace) ? 'bg-primary/10' : ''}`}><span className="block truncate text-sm font-medium text-foreground">{workspace.title}</span><span className="block truncate text-xs text-muted-foreground">{workspace.owner_username || workspace.owner_id}</span></button>)}
      </aside>
      {selected ? <WorkspaceChats key={`${reload}/${workspaceKey(selected)}`} workspace={selected} /> : <p className="p-4 text-sm text-muted-foreground">Select a Code project to review its conversations.</p>}
    </div>}
  </div>
}
