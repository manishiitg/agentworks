import { useCallback, useEffect, useMemo, useState } from 'react'
import axios from 'axios'
import { WebhookEndpoint } from '../scheduler/scheduleRuns/WebhookEndpoint'
import { conditionTriggersApi, type ConditionTrigger, type ConditionHistory, type WatcherCaller } from '../../api/conditionTriggers'
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
  const [savedChats, setSavedChats] = useState<SideChat[]>([])
  const [callers, setCallers] = useState<WatcherCaller[]>([])
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const refresh = useCallback(async () => {
    try { const result = await conditionTriggersApi.list(scope.projectId, scope.profileId); setTriggers(result.triggers); setCallers(result.callers ?? []); setSavedChats(result.chats ?? []); setError(''); setLoaded(true) }
    catch (e) { setError(messageOf(e)) }
  }, [scope.projectId, scope.profileId])
  useEffect(() => { setTriggers([]); setCallers([]); setSavedChats([]); setHistory({}); setLoaded(false); void refresh() }, [refresh, refreshToken])
  // The Code chats a watcher can send to: the open side chats of this project (the main chat and "a new chat" are always offered).
  const chatTabs = useChatStore(state => state.chatTabs)
  const sideChats = useMemo<SideChat[]>(() => {
    const names = new Map(savedChats.filter(chat => chat.id !== MAIN_CHAT).map(chat => [chat.id, chat]))
    for (const tab of Object.values(chatTabs).filter(tab => isWorkSideChatTab(tab, scope.projectId))) {
      const id = String(tab.metadata?.agentProfileConversationKey).slice(workSideChatKey(scope.projectId, '').length)
      names.set(id, { id, name: tab.name })
    }
    return [...names.values()]
  }, [chatTabs, savedChats, scope.projectId])
  const sourceName = scope.profileId === 'code' ? 'Code project' : scope.profileId === 'work' ? 'Crew' : scope.profileId === 'relays' ? 'Flow' : 'workflow'
  const targetHint = scope.profileId === 'code' ? 'the agent in the creating chat, or an existing function or route' : scope.profileId === 'work' ? 'a published function, defaulting to this Crew' : scope.profileId === 'relays' ? 'a published Flow entry, defaulting to this Flow' : 'a saved route or internal trigger, defaulting to this workflow'
  const setupMessage = `Set up a watcher attached to this ${sourceName}. Determine the condition I want to wait for, whether the source can send authenticated events or needs scheduled checks, and select ${targetHint}. Use manage_condition_triggers to list current watchers and discover actual entry points. Choose One time for a specific wait that should disable after the first accepted action, or Keep watching for ongoing monitoring. Default the source to the current product. ${scope.profileId === 'code' ? 'Save the detector under code/triggers and default agent delivery to the chat creating it.' : 'Pass script_content so the tool saves the detector in my own watcher folder; select only secrets I can use from Vault.'} Use check_mode=event for immediate webhook wakeups or both for events with scheduled backup. Select an existing secret by name for event authentication, return the event URL, and explain how to connect the source without printing secret values. The Python detector still queries the source; webhook payload is only a wake signal. Preserve cursor and duplicate handling.`
  const sendToChat = onAsk ?? ((message: string) => sendWorkspacePaneMessageToChat({ profileId: scope.profileId, conversationKey: scope.projectId, message }).then(() => undefined))
  // A failed hand-off to the chat must show, not vanish: the buttons call this without awaiting.
  const ask = async (message: string) => {
    try { await sendToChat(message); setError('') }
    catch (e) { setError(messageOf(e)) }
  }
  const manage = async (action: string, id: string, changes?: Record<string, unknown>) => {
    setBusy(true)
    try { await conditionTriggersApi.manage(scope.projectId, action, id, changes, triggers.find(t => t.id === id)?.revision, scope.profileId); await refresh(); setHistory({}) }
    catch (e) { setError(messageOf(e)) }
    finally { setBusy(false) }
  }
  const inspect = async (id: string) => {
    try { const h = await conditionTriggersApi.history(scope.projectId, id, scope.profileId); setHistory(old => ({ ...old, [id]: h })) }
    catch (e) { setError(messageOf(e)) }
  }
  return <div className="h-full min-w-0 overflow-y-auto p-4" data-testid="condition-triggers">
    <div className="mb-4 flex flex-wrap items-start justify-between gap-3">
      <p className="max-w-xl text-xs leading-relaxed text-muted-foreground">Check a condition with Python when an event arrives or on a schedule, then call {targetHint}. Choose One time to stop after the first accepted action. Checks with no matches use no AI.</p>
      {canManage && <button className={buttonClass} onClick={() => void ask(setupMessage)}>Add in chat</button>}
    </div>
    {error && <p role="alert" className="mb-3 rounded border border-destructive/30 p-3 text-sm text-destructive">{error}</p>}
    {loaded && triggers.length === 0 && <p className="rounded-lg border border-dashed border-border p-6 text-center text-sm text-muted-foreground">No watchers yet. Add one in chat to monitor a source and start work when it matches.</p>}
    {callers.length > 0 && <details className="mb-4 rounded border border-border p-3 text-xs"><summary className="cursor-pointer">Watchers calling this product</summary><ul className="mt-2 space-y-2">{callers.map(caller => <li key={`${caller.owner_id}:${caller.id}`} className="break-all">{caller.name} · {caller.owner_id} · {targetLabel(caller.target)} · {caller.enabled ? 'Enabled' : 'Paused'}{caller.last_dispatch_at ? ` · Last dispatch ${new Date(caller.last_dispatch_at).toLocaleString()}` : caller.last_status ? ` · ${caller.last_status}` : ''}</li>)}</ul></details>}
    <div className="space-y-3">{triggers.map(t => <section key={t.id} className="min-w-0 space-y-2 rounded-lg border border-border p-3">
      <div className="flex items-center justify-between gap-2"><h3 className="truncate text-sm font-medium">{t.name}</h3><span className="text-xs text-muted-foreground">{t.completed_at ? 'Completed' : t.once_status === 'failed' || t.once_status === 'needs_reconciliation' || t.last_status === 'error' ? 'Needs attention' : t.enabled ? 'Enabled' : 'Paused'}</span></div>
      <p className="break-all text-xs text-muted-foreground">{t.script} · {t.check_mode === 'event' ? 'When an event arrives' : t.check_mode === 'both' ? 'Events + backup schedule' : 'On a schedule'}{t.cron_expression ? ` · ${t.cron_expression} · ${t.timezone}` : ''}</p>
      {t.event_path && <details className="text-xs text-muted-foreground"><summary className="cursor-pointer">Event connection</summary>
        <p className="mt-2">Events wake the saved check. It reads the source to find matches.</p>
        <p>{t.event_auth_mode === 'github' ? 'GitHub signature' : 'Authenticated events'} · Secret: {t.event_secret_name}</p>
        <WebhookEndpoint id={t.id} name={t.name} path={t.event_path} />
      </details>}
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
          <button className={buttonClass} disabled={busy} onClick={() => void ask(`Configure event wakeups for watcher ${t.id}. Read its current definition with manage_condition_triggers. Check whether my source can send authenticated webhooks; use check_mode event or both with a backup schedule. Select an existing granted secret by name for event_secret_name and include it in secrets. Do not show secret values. Explain how to connect the returned event_path to the source. Preserve the detector, target and one-time choice unless I ask to change them.`)}>{t.event_path ? 'Edit event connection' : 'Connect events in chat'}</button>
          <button className={buttonClass} disabled={busy} onClick={() => void ask(`Edit watcher ${t.id}. Read its saved definition and read_script using manage_condition_triggers, explain its check and target, then apply the changes I request. Preserve its cursor and history.`)}>Edit in chat</button>
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
