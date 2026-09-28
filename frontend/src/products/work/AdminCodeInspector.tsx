import { useEffect, useMemo, useState } from 'react'
import { AlertCircle, FileText, Loader2, MessageSquare, Plug, ScrollText, ShieldCheck, X } from 'lucide-react'
import { agentApi } from '../../services/api'
import type { CodeAdminAuditEntry, CodeAdminChat, CodeAdminWorkspace, SharedProjectFileEntry } from '../../services/api-types'

type Tab = 'files' | 'chats' | 'mcp'
type PersonalServerRow = { user_id: string; username?: string; name: string; url: string; transport: string; oauth: boolean; connected: boolean }
type ChatMessage = { role: string; parts: Array<Record<string, unknown>> }

function errorText(cause: unknown, fallback: string): string {
  const message = (cause as { response?: { data?: { error?: string } } })?.response?.data?.error
  return message || (cause instanceof Error ? cause.message : fallback)
}

function partText(part: Record<string, unknown>): string {
  const text = part.Text ?? part.text
  if (typeof text === 'string' && text.trim()) return text
  // Tool calls and their results (including terminal output) as compact JSON.
  const raw = JSON.stringify(part)
  return raw.length > 4000 ? `${raw.slice(0, 4000)}…` : raw
}

// Read-only inspection of every user's Code workspaces for server admins and
// Code reviewers. Each list, file and chat opened here is recorded in the
// audit log.
export function AdminCodeInspector({ onClose }: { onClose: () => void }) {
  const [workspaces, setWorkspaces] = useState<CodeAdminWorkspace[] | null>(null)
  const [selected, setSelected] = useState<CodeAdminWorkspace | null>(null)
  const [tab, setTab] = useState<Tab>('files')
  const [files, setFiles] = useState<SharedProjectFileEntry[]>([])
  const [file, setFile] = useState<{ path: string; content: string; binary?: boolean; truncated?: boolean } | null>(null)
  const [chats, setChats] = useState<CodeAdminChat[]>([])
  const [mcpServers, setMcpServers] = useState<PersonalServerRow[]>([])
  const [chat, setChat] = useState<{ chat: CodeAdminChat; messages: ChatMessage[] } | null>(null)
  const [audit, setAudit] = useState<CodeAdminAuditEntry[] | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    void agentApi.adminListCodeWorkspaces()
      .then(response => { if (!cancelled) setWorkspaces(response.workspaces) })
      .catch(cause => { if (!cancelled) setError(errorText(cause, 'Could not list Code workspaces.')) })
    return () => { cancelled = true }
  }, [])

  useEffect(() => {
    if (!selected) return
    let cancelled = false
    setLoading(true)
    setError(null)
    setFile(null)
    setChat(null)
    const load = tab === 'files'
      ? agentApi.adminListCodeFiles(selected.owner_id, selected.id).then(response => { if (!cancelled) setFiles(response.files) })
      : tab === 'chats'
        ? agentApi.adminListCodeChats(selected.owner_id, selected.id).then(response => { if (!cancelled) setChats(response.chats) })
        : agentApi.adminListCodeMCP(selected.owner_id, selected.id).then(response => { if (!cancelled) setMcpServers(response.servers) })
    void load
      .catch(cause => { if (!cancelled) setError(errorText(cause, 'Could not load this workspace.')) })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [selected, tab])

  const owners = useMemo(() => {
    const byOwner = new Map<string, CodeAdminWorkspace[]>()
    for (const workspace of workspaces || []) {
      const key = workspace.owner_username || workspace.owner_id
      byOwner.set(key, [...(byOwner.get(key) || []), workspace])
    }
    return [...byOwner.entries()]
  }, [workspaces])

  const openFile = (path: string) => {
    if (!selected) return
    setError(null)
    void agentApi.adminGetCodeFile(selected.owner_id, selected.id, path)
      .then(setFile)
      .catch(cause => setError(errorText(cause, 'Could not open the file.')))
  }

  const openChat = (entry: CodeAdminChat) => {
    if (!selected) return
    setError(null)
    void agentApi.adminGetCodeChat(selected.owner_id, selected.id, entry.session_id, entry.user_id)
      .then(response => {
        const messages = (response.conversation_history || []).map(message => ({
          role: String(message.Role ?? message.role ?? ''),
          parts: (message.Parts ?? message.parts ?? []) as Array<Record<string, unknown>>,
        }))
        setChat({ chat: entry, messages })
      })
      .catch(cause => setError(errorText(cause, 'Could not open the chat.')))
  }

  const openAudit = () => {
    setError(null)
    void agentApi.adminCodeAudit()
      .then(response => setAudit([...response.entries].reverse()))
      .catch(cause => setError(errorText(cause, 'Could not read the audit log.')))
  }

  return (
    <div className="fixed inset-0 z-50 flex flex-col bg-background" role="dialog" aria-modal="true" aria-label="Inspect Code workspaces">
      <div className="flex items-center gap-3 border-b border-border px-4 py-2">
        <ShieldCheck className="h-4 w-4 text-primary" />
        <div className="min-w-0 flex-1">
          <p className="text-sm font-semibold text-foreground">Inspect Code workspaces</p>
          <p className="truncate text-xs text-muted-foreground">Read-only. Every workspace, file and chat you open here is recorded in the audit log.</p>
        </div>
        <button type="button" onClick={openAudit} className="inline-flex items-center gap-1 rounded-md border border-border px-2 py-1 text-xs hover:bg-muted">
          <ScrollText className="h-3.5 w-3.5" /> Audit log
        </button>
        <button type="button" onClick={onClose} aria-label="Close" className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground">
          <X className="h-4 w-4" />
        </button>
      </div>
      {error ? <p className="flex items-center gap-1.5 border-b border-border px-4 py-2 text-sm text-destructive"><AlertCircle className="h-4 w-4" />{error}</p> : null}
      <div className="grid min-h-0 flex-1 grid-cols-1 md:grid-cols-[260px_280px_1fr]">
        <aside className="min-h-0 overflow-y-auto border-r border-border p-2">
          {workspaces === null && !error ? <p className="p-2 text-sm text-muted-foreground"><Loader2 className="mr-2 inline h-4 w-4 animate-spin" />Loading…</p> : null}
          {workspaces?.length === 0 ? <p className="p-2 text-sm text-muted-foreground">No Code workspaces on this server yet.</p> : null}
          {owners.map(([owner, items]) => (
            <div key={owner} className="mb-3">
              <p className="px-2 pb-1 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">{owner}</p>
              {items.map(item => (
                <button
                  key={`${item.owner_id}/${item.id}`}
                  type="button"
                  onClick={() => { setAudit(null); setSelected(item) }}
                  className={`w-full rounded-md px-2 py-1.5 text-left text-sm ${selected?.id === item.id && selected.owner_id === item.owner_id ? 'bg-primary/10 text-foreground' : 'text-muted-foreground hover:bg-muted'}`}
                >
                  <span className="block truncate">{item.title}</span>
                  {item.shares.length > 0 ? <span className="block truncate text-[11px]">shared with {item.shares.map(share => share.username || share.user_id).join(', ')}</span> : null}
                </button>
              ))}
            </div>
          ))}
        </aside>
        {audit ? (
          <section className="min-h-0 overflow-auto p-3 md:col-span-2">
            <p className="mb-2 text-sm font-semibold text-foreground">Reviews this month</p>
            <table className="w-full text-left text-xs">
              <thead className="text-muted-foreground"><tr><th className="py-1">When</th><th>Who</th><th>What</th><th>Owner / workspace</th><th>Target</th></tr></thead>
              <tbody>
                {audit.map((entry, index) => (
                  <tr key={index} className="border-t border-border">
                    <td className="py-1 pr-2">{new Date(entry.at).toLocaleString()}</td>
                    <td className="pr-2">{entry.admin_username || entry.admin_id}{entry.role === 'reviewer' ? ' (reviewer)' : ''}</td>
                    <td className="pr-2">{entry.action}</td>
                    <td className="pr-2">{entry.owner_id ? `${entry.owner_id} / ${entry.project_id}` : ''}</td>
                    <td className="break-all">{entry.target}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </section>
        ) : selected ? (<>
          <section className="min-h-0 overflow-y-auto border-r border-border">
            <div className="flex gap-1 border-b border-border p-2">
              <button type="button" onClick={() => setTab('files')} className={`inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs ${tab === 'files' ? 'bg-muted text-foreground' : 'text-muted-foreground'}`}><FileText className="h-3.5 w-3.5" />Files</button>
              <button type="button" onClick={() => setTab('chats')} className={`inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs ${tab === 'chats' ? 'bg-muted text-foreground' : 'text-muted-foreground'}`}><MessageSquare className="h-3.5 w-3.5" />Chats</button>
              <button type="button" onClick={() => setTab('mcp')} className={`inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs ${tab === 'mcp' ? 'bg-muted text-foreground' : 'text-muted-foreground'}`}><Plug className="h-3.5 w-3.5" />MCP</button>
            </div>
            {loading ? <p className="p-3 text-sm text-muted-foreground"><Loader2 className="mr-2 inline h-4 w-4 animate-spin" />Loading…</p> : null}
            {!loading && tab === 'files' ? files.map(entry => (
              <button key={entry.path} type="button" onClick={() => openFile(entry.path)} className={`block w-full truncate px-3 py-1 text-left font-mono text-xs ${file?.path === entry.path ? 'bg-primary/10 text-foreground' : 'text-muted-foreground hover:bg-muted'}`}>{entry.path}</button>
            )) : null}
            {!loading && tab === 'chats' ? (chats.length === 0 ? <p className="p-3 text-sm text-muted-foreground">No chats yet.</p> : chats.map(entry => (
              <button key={`${entry.user_id}/${entry.session_id}`} type="button" onClick={() => openChat(entry)} className={`block w-full px-3 py-1.5 text-left text-xs ${chat?.chat.session_id === entry.session_id ? 'bg-primary/10 text-foreground' : 'text-muted-foreground hover:bg-muted'}`}>
                <span className="block truncate text-foreground">{entry.title || entry.session_id}</span>
                <span className="block truncate">{entry.username || entry.user_id} · {entry.message_count} messages{entry.updated_at ? ` · ${new Date(entry.updated_at).toLocaleString()}` : ''}</span>
              </button>
            ))) : null}
            {!loading && tab === 'mcp' ? (mcpServers.length === 0 ? <p className="p-3 text-sm text-muted-foreground">Nobody has switched on their own MCP servers in this Code.</p> : mcpServers.map(row => (
              <div key={`${row.user_id}/${row.name}`} className="border-b border-border px-3 py-1.5 text-xs">
                <span className="block truncate text-foreground">{row.name} <span className="text-muted-foreground">· {row.username || row.user_id}</span></span>
                <span className="block truncate font-mono text-muted-foreground">{row.url}</span>
                <span className="block text-muted-foreground">{row.transport}{row.oauth ? (row.connected ? ' · signed in' : ' · not signed in') : ''}</span>
              </div>
            ))) : null}
          </section>
          <section className="min-h-0 overflow-auto p-3">
            {tab === 'files' && file ? (
              file.binary ? <p className="text-sm text-muted-foreground">{file.path} is a binary file.</p>
                : <pre className="whitespace-pre-wrap break-words font-mono text-xs text-foreground">{file.content}{file.truncated ? '\n… (truncated)' : ''}</pre>
            ) : null}
            {tab === 'chats' && chat ? chat.messages.map((message, index) => (
              <div key={index} className="mb-3 rounded-md border border-border p-2">
                <p className="mb-1 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">{message.role}</p>
                {message.parts.map((part, partIndex) => <pre key={partIndex} className="whitespace-pre-wrap break-words font-mono text-xs text-foreground">{partText(part)}</pre>)}
              </div>
            )) : null}
            {tab === 'mcp' ? <p className="text-sm text-muted-foreground">Each person's own servers switched on in this Code. Their logins and secrets are never shown.</p> : null}
            {(tab === 'files' && !file) || (tab === 'chats' && !chat) ? <p className="text-sm text-muted-foreground">Pick a {tab === 'files' ? 'file' : 'chat'} to read it.</p> : null}
          </section>
        </>) : (
          <section className="grid place-items-center p-6 text-sm text-muted-foreground md:col-span-2">Pick a workspace to inspect it.</section>
        )}
      </div>
    </div>
  )
}
