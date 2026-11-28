import { lazy, Suspense, useEffect, useState } from 'react'
import { Bot as BotIcon, X } from 'lucide-react'
import { useAuthStore } from '../../../stores/useAuthStore'
import { openProjectAskConversation } from '../../../utils/projectAskConversation'
import { WorkingTipsContext } from '../../../components/chat/workingTipsContext'
import { botError, callBot, listBots, type Bot, type BotFunction } from './botsApi'
import { useBotsPageStore } from './botsPageStore'

const ChatArea = lazy(() => import('../../../components/ChatArea'))

/**
 * Bots (#4545): agents of projects on the server that their makers shared for you to use without joining the project.
 * Each one opens your own private chat with it; its buttons are the project functions its maker picked. The server
 * lists only the bots shared with you and decides every call.
 */
export default function BotsPage() {
  const close = useBotsPageStore(state => state.setOpen)
  const [bots, setBots] = useState<Bot[] | null>(null)
  const [error, setError] = useState('')
  const [selected, setSelected] = useState<string | null>(null)
  const [retry, setRetry] = useState(0)
  useEffect(() => {
    let cancelled = false
    setError('')
    void listBots().then(list => { if (!cancelled) { setBots(list); setSelected(current => current && list.some(bot => bot.id === current) ? current : list.find(bot => !bot.mine)?.id ?? list[0]?.id ?? null) } })
      .catch(reason => { if (!cancelled) { setBots([]); setError(botError(reason, 'Could not load bots.')) } })
    return () => { cancelled = true }
  }, [retry])
  const bot = bots?.find(item => item.id === selected)
  return <div className="flex h-full min-h-0 flex-col bg-background">
    <header className="flex h-12 shrink-0 items-center gap-2 border-b border-border px-4">
      <BotIcon aria-hidden="true" className="h-4 w-4" />
      <h1 className="text-sm font-semibold">Bots</h1>
      <span className="text-xs text-muted-foreground">Agents shared for you to use. Each one is your own private chat.</span>
      <button type="button" aria-label="Close Bots" onClick={() => close(false)} className="ml-auto grid h-7 w-7 place-items-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground"><X aria-hidden="true" className="h-4 w-4" /></button>
    </header>
    <div className="flex min-h-0 flex-1 max-md:flex-col">
      <nav aria-label="Bots shared with you" className="w-80 shrink-0 overflow-y-auto border-r border-border p-2 max-md:max-h-56 max-md:w-full max-md:border-b max-md:border-r-0">
        {bots === null && <p role="status" className="p-3 text-sm text-muted-foreground">Loading bots…</p>}
        {error && <div className="p-3 text-sm"><p role="alert">{error}</p><button type="button" className="mt-2 text-primary underline" onClick={() => setRetry(value => value + 1)}>Try again</button></div>}
        {bots?.length === 0 && !error && <div className="p-3 text-sm text-muted-foreground">
          <p className="font-medium text-foreground">No bots yet</p>
          <p className="mt-1">A bot is an agent someone shared for you to use, without joining their project. To share one of yours, open the agent's menu in a project on the server and turn on Bot.</p>
        </div>}
        {bots?.map(item => <button key={item.id} type="button" aria-current={item.id === selected ? 'true' : undefined} onClick={() => setSelected(item.id)}
          className={`flex w-full items-start gap-3 rounded-lg p-2.5 text-left hover:bg-muted ${item.id === selected ? 'bg-muted' : ''}`}>
          <span aria-hidden="true" className="grid h-9 w-9 shrink-0 place-items-center rounded-lg bg-primary/10 text-lg">{item.icon || '🤖'}</span>
          <span className="min-w-0 flex-1">
            <span className="flex items-center gap-1.5"><span className="truncate text-sm font-medium">{item.name}</span>{item.mine && <span className="shrink-0 rounded-full bg-primary/15 px-1.5 text-[10px] font-medium text-primary">Yours</span>}</span>
            {item.purpose && <span className="line-clamp-2 block text-xs text-muted-foreground">{item.purpose}</span>}
            <span className="block truncate text-[11px] text-muted-foreground">{item.project_title} · {item.mine ? 'by you' : `by ${item.maker_name}`}</span>
          </span>
        </button>)}
      </nav>
      <main className="flex min-h-0 min-w-0 flex-1 flex-col">
        {bot ? <BotView key={bot.id} bot={bot} /> : bots && bots.length > 0 && <p className="p-6 text-sm text-muted-foreground">Choose a bot.</p>}
      </main>
    </div>
  </div>
}

function BotView({ bot }: { bot: Bot }) {
  const buttons = bot.functions.filter(fn => fn.name !== 'ask')
  return <>
    <div className="flex shrink-0 items-start gap-3 border-b border-border px-4 py-3">
      <span aria-hidden="true" className="grid h-10 w-10 shrink-0 place-items-center rounded-lg bg-primary/10 text-xl">{bot.icon || '🤖'}</span>
      <div className="min-w-0 flex-1">
        <h2 className="truncate text-sm font-semibold">{bot.name}</h2>
        <p className="text-xs text-muted-foreground">{bot.purpose || 'Ask it anything.'}</p>
        <p className="text-[11px] text-muted-foreground">From {bot.project_title}, {bot.mine ? 'your project' : `shared by ${bot.maker_name}`}. Your chat is yours alone; it runs with the project's setup and its cost is yours.</p>
        {buttons.length > 0 && <div className="mt-2 flex flex-wrap gap-1.5">{buttons.map(fn => <BotButton key={fn.name} bot={bot.id} fn={fn} />)}</div>}
      </div>
    </div>
    <BotChat bot={bot} />
  </>
}

/** One of the bot's buttons: a project function, with a small form when it takes input. */
function BotButton({ bot, fn }: { bot: string; fn: BotFunction }) {
  const fields = Object.entries(fn.input_schema?.properties ?? {})
  const [open, setOpen] = useState(false)
  const [values, setValues] = useState<Record<string, string>>({})
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState('')
  const run = async () => {
    setBusy(true); setResult('')
    try {
      const args: Record<string, unknown> = {}
      for (const [name, field] of fields) {
        const value = values[name]?.trim()
        if (!value) continue
        args[name] = field.type === 'number' || field.type === 'integer' ? Number(value) : field.type === 'boolean' ? value === 'true' : value
      }
      const out = await callBot(bot, fn.name, args)
      const text = out.result === undefined ? '' : typeof out.result === 'string' ? out.result : JSON.stringify(out.result, null, 2)
      setResult(out.error || text || (out.status === 'completed' ? 'Done.' : 'Started. It is still running; the result will be in the project\'s function calls.'))
    } catch (reason) {
      setResult(botError(reason, 'Could not run it.'))
    } finally { setBusy(false) }
  }
  return <span className="relative">
    <button type="button" title={fn.description} disabled={busy} aria-expanded={fields.length ? open : undefined}
      onClick={() => fields.length ? setOpen(!open) : void run()}
      className="rounded-md border border-border px-2.5 py-1 text-xs hover:bg-muted disabled:opacity-60">{busy ? `${fn.name}…` : fn.name}</button>
    {(open || result) && <div role="dialog" aria-label={fn.name} className="absolute left-0 top-8 z-50 w-80 rounded-lg border border-border bg-popover p-3 text-xs shadow-lg">
      {fn.description && <p className="mb-2 text-muted-foreground">{fn.description}</p>}
      {open && fields.map(([name, field]) => <label key={name} className="mb-2 block">
        <span className="block font-medium">{name}{fn.input_schema?.required?.includes(name) ? ' *' : ''}</span>
        <input value={values[name] ?? ''} onChange={event => setValues({ ...values, [name]: event.target.value })} placeholder={field.description} className="mt-0.5 w-full rounded-md border border-input bg-background px-2 py-1" />
      </label>)}
      {result && <pre className="mb-2 max-h-48 overflow-auto whitespace-pre-wrap rounded-md bg-muted p-2">{result}</pre>}
      <div className="flex justify-end gap-2">
        <button type="button" onClick={() => { setOpen(false); setResult('') }} className="rounded-md px-2 py-1 hover:bg-muted">Close</button>
        {open && <button type="button" disabled={busy} onClick={() => void run()} className="rounded-md bg-primary px-2 py-1 text-primary-foreground disabled:opacity-60">{busy ? 'Running…' : 'Run'}</button>}
      </div>
    </div>}
  </span>
}

/** The person's own chat with the bot: the server resolves it in their own chats, in Run mode. */
function BotChat({ bot }: { bot: Bot }) {
  const viewer = useAuthStore(state => state.user?.id)
  const [tabId, setTabId] = useState<string | null>(null)
  const [error, setError] = useState('')
  const [retry, setRetry] = useState(0)
  useEffect(() => {
    let cancelled = false
    setTabId(null); setError('')
    if (viewer) void openProjectAskConversation({ workspacePath: bot.workspace, projectId: bot.project_id }, { activate: false, agentKey: bot.id })
      .then(id => { if (!cancelled) setTabId(id) })
      .catch(reason => { if (!cancelled) setError(reason instanceof Error ? reason.message : botError(reason, 'Could not open the chat.')) })
    return () => { cancelled = true }
  }, [bot.id, bot.workspace, bot.project_id, viewer, retry])
  return <section aria-label={`Chat with ${bot.name}`} className="flex min-h-0 flex-1 flex-col">
    {tabId ? <WorkingTipsContext.Provider value={false}><Suspense fallback={<p role="status" className="p-4 text-sm">Opening chat…</p>}><ChatArea tabId={tabId} compact hideHeader previousChatsWorkspacePath={bot.workspace} landingContent={<div />} composerPlaceholder={`Ask ${bot.name}…`} /></Suspense></WorkingTipsContext.Provider>
      : <div className="p-4 text-sm text-muted-foreground"><p role="status">{error || 'Opening chat…'}</p>{error && <button type="button" onClick={() => setRetry(value => value + 1)} className="mt-2 underline">Try again</button>}</div>}
  </section>
}
