import { useCallback, useEffect, useMemo, useState } from 'react'
import { Copy, Download, Mail } from 'lucide-react'
import api from '../../services/api'
import { Button } from '../ui/Button'
import { useCapabilitiesStore } from '../../stores/useCapabilitiesStore'
import { Switch } from '../ui/Switch'
import { PageRow, RowButton } from '../automation/AutomationKit'

export function useAgentInboxEnabled() { return useCapabilitiesStore(state => state.capabilities?.agent_inboxes === true) }
export interface InboxMail { sequence: number; id: string; thread_id: string; from: string; recipients: string[]; subject: string; body: string; received_at: number; authenticated: boolean; labels: string[]; attachments?: { id: string; name: string; size: number }[] }
interface InboxState { enabled: boolean; can_configure?: boolean;inbox: { id: string; address: string }; receiving: boolean; error?: string; mail: { messages: InboxMail[]; next_cursor: number; has_more: boolean } }
const errorText = (cause: unknown) => { const data = (cause as { response?: { data?: unknown } })?.response?.data; return typeof data === 'string' ? data : 'Could not load this inbox. Please try again.' }
export function AgentInboxPanel({ workspacePath, refreshToken = 0, embedded = false }: { workspacePath: string; refreshToken?: number; embedded?: boolean }) {
  const [state, setState] = useState<InboxState | null>(null)
  const [mail, setMail] = useState<InboxMail[]>([])
  const [thread, setThread] = useState<string | null>(null)
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const enabled = useAgentInboxEnabled()
  const load = useCallback(async (after = 0) => {
    setBusy(true); setError('')
    try { const result = (await api.get<InboxState>('/api/agent-email/inbox', { params: { workspace_path: workspacePath, before:after, recent:true } })).data; setState(result); setMail(old => after ? [...old, ...result.mail.messages.filter(item => !old.some(saved => saved.id === item.id))] : result.mail.messages) }
    catch (cause) { setError(errorText(cause)) }
    finally { setBusy(false) }
  }, [workspacePath])
  useEffect(() => { setState(null); setMail([]); setThread(null); if (enabled) void load() }, [enabled, load, refreshToken])
  // The owner's switch: the server turns the Crew's incoming email on or off, then the page reads it again.
  const setIncoming = async (on: boolean) => {
    setBusy(true); setError(''); setNotice('')
    try { await api.post('/api/agent-email/inbox', { workspace_path: workspacePath, enabled: on }); await load() }
    catch (cause) { setError(errorText(cause)); setBusy(false) }
  }
  const threads = useMemo(() => {
    const grouped = new Map<string, InboxMail[]>()
    for (const message of mail) grouped.set(message.thread_id, [...(grouped.get(message.thread_id) ?? []), message])
    for (const messages of grouped.values()) messages.sort((a,b)=>a.received_at-b.received_at || a.sequence-b.sequence)
    return [...grouped.entries()].sort((a, b) => b[1][b[1].length - 1].received_at - a[1][a[1].length - 1].received_at)
  }, [mail])
  const download = async (message: InboxMail, index: number) => {
    try { const result = await api.get('/api/agent-email/inbox', { params: { workspace_path: workspacePath, message_id: message.id, attachment: index }, responseType: 'blob' }); const url = URL.createObjectURL(result.data); const link = document.createElement('a'); link.href = url; link.download = message.attachments![index].name; link.click(); URL.revokeObjectURL(url) }
    catch (cause) { setError(errorText(cause)) }
  }
  if (!enabled) return null
  if (embedded) {
    const address = state?.inbox.address
    const week = mail.filter(message => message.received_at > Date.now() - 7 * 86400000).length
    const status = !state ? 'Loading…' : !state.enabled ? 'Incoming email is off for this Crew.' : state.receiving ? `${week} ${week === 1 ? 'message' : 'messages'} this week · each starts a chat` : 'Checking the Crew owner’s Gmail…'
    return <section aria-label="Agent inbox">
      {state && (state.can_configure
        ? <PageRow lead={<Switch aria-label={state.enabled ? 'Turn incoming email off' : 'Turn incoming email on'} checked={state.enabled} disabled={busy} onCheckedChange={on => void setIncoming(on)} />} title="Incoming email" lines={[state.enabled ? 'On' : 'Incoming email is off for this Crew.']} />
        : !state.enabled && <p className="px-4 pt-3 text-xs text-muted-foreground">Incoming email is off for this Crew.</p>)}
      {error && <p role="alert" className="px-4 pt-3 text-xs text-destructive">{error}</p>}
      {notice && <p role="status" className="px-4 pt-3 text-xs">{notice}</p>}
      {state && state.enabled && state.error && <p className="px-4 pt-3 text-xs text-destructive">{state.error}</p>}
      {!busy && !error && state && state.enabled && !address && mail.length === 0 && <p className="px-4 py-6 text-center text-sm text-muted-foreground">No email yet.</p>}
      {(address || mail.length > 0) && <PageRow lead={<Mail className="h-4 w-4 text-muted-foreground" />} title={address || 'Inbox'} lines={[status]}
        action={address ? <RowButton onClick={() => void navigator.clipboard.writeText(address).then(() => setNotice('Address copied.'), () => setError('Could not copy the address. Select and copy it.'))}>Copy address</RowButton> : undefined}
        open={open} onToggle={() => setOpen(value => !value)}>
        {mail.length === 0 && !busy && <p className="text-muted-foreground">No email yet. Send a message to this address.</p>}
        <div className="space-y-2">{threads.map(([id, messages]) => { const latest = messages[messages.length - 1]; return <div key={id} className="overflow-hidden rounded-lg border border-border"><button type="button" aria-expanded={thread === id} onClick={() => setThread(thread === id ? null : id)} className="flex w-full items-start gap-3 p-3 text-left hover:bg-muted/40"><Mail className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" /><div className="min-w-0 flex-1"><p className="break-words font-medium">{latest.subject || '(No subject)'}</p><p className="mt-1 break-all text-xs text-muted-foreground">{latest.from} · {messages.length} {messages.length === 1 ? 'message' : 'messages'}</p></div><time className="shrink-0 text-xs text-muted-foreground">{new Date(latest.received_at).toLocaleDateString()}</time></button>{thread === id && <div className="divide-y divide-border border-t border-border">{messages.map(message => <article key={message.id} className="space-y-3 p-3"><div className="text-xs text-muted-foreground"><p className="break-all">From: {message.from}</p><p>{new Date(message.received_at).toLocaleString()}{!message.authenticated && ' · Sender not verified'}</p></div><p className="whitespace-pre-wrap break-words text-sm">{message.body}</p>{message.attachments?.map((attachment, index) => <Button key={index} size="xs" variant="outline" onClick={() => void download(message, index)}><Download className="mr-1 h-3 w-3" /><span className="max-w-60 truncate">{attachment.name}</span></Button>)}</article>)}</div>}</div> })}</div>
        <div className="flex items-center gap-3 text-xs"><Button size="sm" variant="outline" disabled={busy} onClick={() => void load()}>Refresh inbox</Button>{state?.mail.has_more && <Button size="sm" variant="outline" disabled={busy} onClick={() => void load(state.mail.next_cursor)}>Load more</Button>}{busy && <span role="status" className="text-muted-foreground">Fetching emails…</span>}</div>
      </PageRow>}
    </section>
  }
  return <section aria-label="Agent inbox" className="space-y-4 text-sm">
    <div className="min-w-0"><h3 className="flex items-center gap-2 font-semibold"><Mail className="h-4 w-4" />Inbox</h3>{state?.inbox.address && <p className="mt-2 break-all font-mono text-xs select-all">{state.inbox.address}</p>}<p className="mt-2 text-xs text-muted-foreground">Mail stays here. Optional Watchers filter it and send matching emails to chat or a workflow route.</p></div>
    {state && <div className="flex flex-wrap items-center gap-2 text-xs"><span>{!state.enabled ? 'Incoming email is off for this Crew. Ask in chat to enable it.' : state.receiving ? 'Ready to receive email' : 'Checking the Crew owner’s Gmail…'}</span>{state.inbox.address && <Button variant="ghost" size="xs" onClick={() => void navigator.clipboard.writeText(state.inbox.address).then(() => setNotice('Address copied.'), () => setError('Could not copy the address. Select and copy it.'))}><Copy className="mr-1 h-3 w-3" />Copy address</Button>}{state.enabled && state.error && <span className="text-destructive">{state.error}</span>}</div>}
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}{notice && <p role="status" className="text-xs">{notice}</p>}
    {!busy && !error && mail.length === 0 && <div className="rounded-lg border border-dashed border-border p-6 text-center text-muted-foreground">{state?.enabled ? "No email yet. Send a message to this agent’s address." : "No stored email. Ask in chat to enable incoming email for this Crew."}</div>}
    <div className="space-y-2">{threads.map(([id, messages]) => { const latest = messages[messages.length - 1]; return <div key={id} className="overflow-hidden rounded-lg border border-border"><button type="button" aria-expanded={thread === id} onClick={() => setThread(thread === id ? null : id)} className="flex w-full items-start gap-3 p-3 text-left hover:bg-muted/40"><Mail className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" /><div className="min-w-0 flex-1"><p className="break-words font-medium">{latest.subject || '(No subject)'}</p><p className="mt-1 break-all text-xs text-muted-foreground">{latest.from} · {messages.length} {messages.length === 1 ? 'message' : 'messages'}</p></div><time className="shrink-0 text-xs text-muted-foreground">{new Date(latest.received_at).toLocaleDateString()}</time></button>{thread === id && <div className="divide-y divide-border border-t border-border">{messages.map(message => <article key={message.id} className="space-y-3 p-3"><div className="text-xs text-muted-foreground"><p className="break-all">From: {message.from}</p><p>{new Date(message.received_at).toLocaleString()}{!message.authenticated && ' · Sender not verified'}</p></div><p className="whitespace-pre-wrap break-words text-sm">{message.body}</p>{message.attachments?.map((attachment, index) => <Button key={index} size="xs" variant="outline" onClick={() => void download(message, index)}><Download className="mr-1 h-3 w-3" /><span className="max-w-60 truncate">{attachment.name}</span></Button>)}</article>)}</div>}</div> })}</div>
    <div className="flex items-center gap-3 text-xs"><Button size="sm" variant="outline" disabled={busy} onClick={() => void load()}>Refresh inbox</Button>{state?.mail.has_more && <Button size="sm" variant="outline" disabled={busy} onClick={() => void load(state.mail.next_cursor)}>Load more</Button>}{busy && <span role="status" className="text-muted-foreground">Fetching emails…</span>}</div>
  </section>
}
