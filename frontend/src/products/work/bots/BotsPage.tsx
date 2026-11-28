import { lazy, Suspense, useEffect, useState } from 'react'
import { Bot as BotIcon, X } from 'lucide-react'
import { useAuthStore } from '../../../stores/useAuthStore'
import { openProjectAskConversation } from '../../../utils/projectAskConversation'
import { WorkingTipsContext } from '../../../components/chat/workingTipsContext'
import { BOTS_CHANGED_EVENT, botError, callBot, listBots, setProjectBot, type Bot, type BotFunction, type BotProject } from './botsApi'
import { useBotsPageStore } from './botsPageStore'

const ChatArea = lazy(() => import('../../../components/ChatArea'))

/**
 * Bots (#4545): agents of projects on the server that their makers shared for you to use without joining the project.
 * Each one opens your own private chat with it; its buttons are the project functions its maker picked. The server
 * lists only the bots shared with you and decides every call.
 */
export default function BotsPage() {
  const close = useBotsPageStore(state => state.setOpen)
  const asked = useBotsPageStore(state => state.selected)
  const [bots, setBots] = useState<Bot[] | null>(null)
  const [targets, setTargets] = useState<BotProject[]>([])
  const [error, setError] = useState('')
  const [selected, setSelected] = useState<string | null>(asked)
  const [retry, setRetry] = useState(0)
  useEffect(() => { if (asked) setSelected(asked) }, [asked])
  useEffect(() => {
    let cancelled = false
    const load = () => {
      setError('')
      void listBots().then(({ bots: list, addTargets }) => {
        if (cancelled) return
        setBots(list); setTargets(addTargets)
        setSelected(current => current && list.some(bot => bot.id === current) ? current : list.find(bot => !bot.mine)?.id ?? list[0]?.id ?? null)
      }).catch(reason => { if (!cancelled) { setBots([]); setError(botError(reason, 'Could not load bots.')) } })
    }
    load()
    window.addEventListener(BOTS_CHANGED_EVENT, load)
    return () => { cancelled = true; window.removeEventListener(BOTS_CHANGED_EVENT, load) }
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
        {bot ? <BotView key={bot.id} bot={bot} targets={targets} /> : bots && bots.length > 0 && <p className="p-6 text-sm text-muted-foreground">Choose a bot.</p>}
      </main>
    </div>
  </div>
}

function BotView({ bot, targets }: { bot: Bot; targets: BotProject[] }) {
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
      <AddToProject bot={bot} targets={targets.filter(target => target.project_id !== bot.project_id)} />
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

/**
 * Add the bot to one of your projects on the server (#4545): it shows in that project's agent list and right menu, and
 * its agents can ask it. You don't join the bot's project.
 */
function AddToProject({ bot, targets }: { bot: Bot; targets: BotProject[] }) {
  const [pick, setPick] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const inProjects = bot.in_projects ?? []
  const choices = targets.filter(target => !inProjects.some(item => item.project_id === target.project_id))
  const change = async (projectId: string, add: boolean) => {
    setBusy(true); setError('')
    try { await setProjectBot(projectId, bot.id, add); setPick('') } catch (reason) { setError(botError(reason, 'Could not change it.')) } finally { setBusy(false) }
  }
  if (!targets.length && !inProjects.length) return null
  return <div className="w-56 shrink-0 space-y-1 text-xs max-md:w-40">
    {inProjects.map(project => <div key={project.project_id} className="flex items-center justify-between gap-2">
      <span className="truncate rounded-full bg-emerald-500/10 px-2 py-0.5 text-emerald-700 dark:text-emerald-300">✓ In {project.title}</span>
      <button type="button" disabled={busy} onClick={() => void change(project.project_id, false)} className="shrink-0 text-muted-foreground hover:text-destructive">Remove</button>
    </div>)}
    {choices.length > 0 && <div className="flex gap-1">
      <select aria-label="Add to a project" value={pick} onChange={event => setPick(event.target.value)} className="min-w-0 flex-1 rounded-md border border-input bg-background px-1 py-1">
        <option value="">Add to a project…</option>
        {choices.map(target => <option key={target.project_id} value={target.project_id}>{target.title}</option>)}
      </select>
      <button type="button" disabled={!pick || busy} onClick={() => void change(pick, true)} className="rounded-md border border-border px-2 disabled:opacity-50">Add</button>
    </div>}
    {error && <p role="alert" className="text-destructive">{error}</p>}
  </div>
}
