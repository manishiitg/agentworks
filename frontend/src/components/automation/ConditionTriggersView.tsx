import { useCallback, useEffect, useMemo, useState } from 'react'
import axios from 'axios'
import { conditionTriggersApi, type ConditionTrigger, type ConditionHistory } from '../../api/conditionTriggers'
import type { ProductTriggerScope } from '../../api/productWebhooks'
import { sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'
import { useChatStore } from '../../stores/useChatStore'
import { isWorkSideChatTab, workSideChatKey } from '../../products/work/workTabs'

const buttonClass = 'rounded-md border border-border px-2 py-1 text-xs hover:bg-muted disabled:opacity-50'
function messageOf(error: unknown) { return axios.isAxiosError(error) && typeof error.response?.data === 'string' ? error.response.data : error instanceof Error ? error.message : 'Watcher request failed' }
type SideChat = { id: string; name: string }
const NEW_CHAT = 'new'
const MAIN_CHAT = 'main'
function chatChoice(t: ConditionTrigger['target']) { return !t.chat_id || t.chat_id === MAIN_CHAT ? MAIN_CHAT : t.chat_id }
function chatLabel(chatId: string | undefined, sideChats: SideChat[]) {
  if (!chatId || chatId === MAIN_CHAT) return 'main chat'
  if (chatId === NEW_CHAT) return 'a new chat for each event'
  return sideChats.find(chat => chat.id === chatId)?.name || `chat ${chatId}`
}
function targetLabel(t: ConditionTrigger['target'], sideChats: SideChat[] = []) {
  if (t.kind === 'agent') return `Code agent · ${chatLabel(t.chat_id, sideChats)}`
  if (t.kind === 'function') return `${t.name} · ${t.function}`
  if (t.kind === 'route') return `${t.name} · ${Object.values(t.route_selections || {}).join(', ') || 'full workflow'}`
  return `${t.name} · trigger ${t.trigger_id}`
}

export default function ConditionTriggersView({ scope, refreshToken = 0, onAsk, canManage = true }: { scope: ProductTriggerScope; refreshToken?: number; onAsk?: (message: string) => void | Promise<void>; canManage?: boolean }) {
  const [triggers, setTriggers] = useState<ConditionTrigger[]>([])
  const [history, setHistory] = useState<Record<string, ConditionHistory>>({})
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const refresh = useCallback(async () => {
    try { setTriggers((await conditionTriggersApi.list(scope.projectId)).triggers); setError(''); setLoaded(true) }
    catch (e) { setError(messageOf(e)) }
  }, [scope.projectId])
  useEffect(() => { setTriggers([]); setHistory({}); setLoaded(false); void refresh() }, [refresh, refreshToken])
  // The Code chats a watcher can send to: the open side chats of this project (the main chat and "a new chat" are always offered).
  const chatTabs = useChatStore(state => state.chatTabs)
  const sideChats = useMemo<SideChat[]>(() => Object.values(chatTabs)
    .filter(tab => isWorkSideChatTab(tab, scope.projectId))
    .map(tab => ({ id: String(tab.metadata?.agentProfileConversationKey).slice(workSideChatKey(scope.projectId, '').length), name: tab.name })), [chatTabs, scope.projectId])
  const sendToChat = onAsk ?? ((message: string) => sendWorkspacePaneMessageToChat({ profileId: scope.profileId, conversationKey: scope.projectId, message }).then(() => undefined))
  // A failed hand-off to the chat must show, not vanish: the buttons call this without awaiting.
  const ask = async (message: string) => {
    try { await sendToChat(message); setError('') }
    catch (e) { setError(messageOf(e)) }
  }
  const manage = async (action: string, id: string, changes?: Record<string, unknown>) => {
    setBusy(true)
    try { await conditionTriggersApi.manage(scope.projectId, action, id, changes, triggers.find(t => t.id === id)?.revision); await refresh(); setHistory({}) }
    catch (e) { setError(messageOf(e)) }
    finally { setBusy(false) }
  }
  const inspect = async (id: string) => {
    try { const h = await conditionTriggersApi.history(scope.projectId, id); setHistory(old => ({ ...old, [id]: h })) }
    catch (e) { setError(messageOf(e)) }
  }
  return <div className="h-full min-w-0 overflow-y-auto p-4" data-testid="condition-triggers">
    <div className="mb-4 flex flex-wrap items-start justify-between gap-3">
      <p className="max-w-xl text-xs leading-relaxed text-muted-foreground">Check a source on a timer using Python. When the condition matches, start this Code&apos;s agent or an existing function or workflow route. Checks with no matches use no AI.</p>
      {canManage && <button className={buttonClass} onClick={() => void ask('Set up a watcher in this Code project. Find out what source and condition I want to check, and which agent, function or route should run. Use manage_condition_triggers to discover current watchers and authorized targets, save the Python detector, select its required secrets, and configure its schedule and One time or Keep watching mode. Default to this project and the chat creating it. Explain its cursor and duplicate handling.')}>Add in chat</button>}
    </div>
    {error && <p role="alert" className="mb-3 rounded border border-destructive/30 p-3 text-sm text-destructive">{error}</p>}
    {loaded && triggers.length === 0 && <p className="rounded-lg border border-dashed border-border p-6 text-center text-sm text-muted-foreground">No watchers yet. Add one in chat to monitor a source and start work when it matches.</p>}
    <div className="space-y-3">{triggers.map(t => <section key={t.id} className="min-w-0 space-y-2 rounded-lg border border-border p-3">
      <div className="flex items-center justify-between gap-2"><h3 className="truncate text-sm font-medium">{t.name}</h3><span className="text-xs text-muted-foreground">{t.completed_at ? 'Completed' : t.once_status === 'failed' || t.once_status === 'needs_reconciliation' ? 'Needs attention' : t.enabled ? 'Enabled' : 'Paused'}</span></div>
      <p className="break-all text-xs text-muted-foreground">{t.script} · {t.cron_expression} · {t.timezone}</p>
      <p className="break-all text-xs">Starts {targetLabel(t.target, sideChats)}</p>
      <p className="text-xs text-muted-foreground">{t.once ? 'One time · disables after the first accepted action' : 'Keep watching'}</p>
      {canManage && !t.completed_at && <label className="flex items-center gap-2 text-xs text-muted-foreground">Mode
        <select aria-label={`Mode for ${t.name}`} className="rounded-md border border-border bg-background px-2 py-1 text-xs text-foreground" disabled={busy} value={t.once ? 'once' : 'recurring'} onChange={event => void manage('update', t.id, { once: event.target.value === 'once' })}>
          <option value="recurring">Keep watching</option><option value="once">One time</option>
        </select>
      </label>}
      {canManage && t.target.kind === 'agent' && <label className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">Send messages to
        <select
          aria-label={`Chat for ${t.name}`}
          className="rounded-md border border-border bg-background px-2 py-1 text-xs text-foreground"
          disabled={busy}
          value={chatChoice(t.target)}
          onChange={event => void manage('update', t.id, { target: { ...t.target, chat_id: event.target.value === MAIN_CHAT ? '' : event.target.value } })}
        >
          <option value={MAIN_CHAT}>Main chat</option>
          {sideChats.map(chat => <option key={chat.id} value={chat.id}>{chat.name}</option>)}
          {chatChoice(t.target) !== MAIN_CHAT && chatChoice(t.target) !== NEW_CHAT && !sideChats.some(chat => chat.id === t.target.chat_id) && <option value={t.target.chat_id}>{`Chat ${t.target.chat_id}`}</option>}
          <option value={NEW_CHAT}>A new chat for each event</option>
        </select>
      </label>}
      {t.last_status && <p className="text-xs text-muted-foreground">Last check: {t.last_status}{t.last_run_at ? ` · ${new Date(t.last_run_at).toLocaleString()}` : ''}</p>}
      {t.last_error && <p className="text-xs text-destructive">{t.last_error}</p>}
      <div className="flex flex-wrap gap-2">
        {canManage && <>
          <button className={buttonClass} disabled={busy || !t.enabled} onClick={() => void manage('check', t.id)}>Check now</button>
          {!t.completed_at && <button className={buttonClass} disabled={busy} onClick={() => void manage('update', t.id, { enabled: !t.enabled })}>{t.enabled ? 'Pause' : 'Enable'}</button>}
          <button className={buttonClass} disabled={busy} onClick={() => void ask(`Edit watcher ${t.id}. Read its saved definition using manage_condition_triggers, explain its check and target, then apply the changes I request. Preserve its cursor and history.`)}>Edit in chat</button>
          <button className={buttonClass} disabled={busy} onClick={() => void manage('delete', t.id)}>Remove</button>
        </>}
        <button className={buttonClass} disabled={busy} onClick={() => void inspect(t.id)}>History</button>
      </div>
      {history[t.id] && <div className="space-y-2 border-t border-border pt-2 text-xs">
        <p className="text-muted-foreground">{history[t.id]!.pending} events waiting for dispatch</p>
        <details><summary className="cursor-pointer">Recent checks</summary><ul className="mt-2 space-y-1">{history[t.id]!.checks.map(c => <li key={c.id}>{new Date(c.started_at).toLocaleString()} · {c.status} · {c.error || c.final_response}</li>)}</ul></details>
        <details open><summary className="cursor-pointer">Matching events</summary>
          {history[t.id]!.events.length === 0 && <p className="mt-2 text-muted-foreground">No matching events recorded.</p>}
          {history[t.id]!.events.map(e => <details key={e.event.id} className="mt-2 rounded border border-border p-2"><summary className="cursor-pointer break-all">{e.event.id} · {e.status.replaceAll('_', ' ')}</summary><pre className="mt-2 overflow-x-auto whitespace-pre-wrap break-all">{JSON.stringify(e.event.payload, null, 2)}</pre>{e.receipt && <p className="mt-1 break-all">Receipt: {e.receipt}</p>}{e.error && <p className="mt-1 text-destructive">{e.error}</p>}</details>)}
        </details>
      </div>}
    </section>)}</div>
  </div>
}
