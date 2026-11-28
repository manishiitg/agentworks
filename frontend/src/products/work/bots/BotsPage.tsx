import { lazy, Suspense, useEffect, useState, type ReactNode } from 'react'
import { ArrowLeft, Bot as BotIcon, Lock, X } from 'lucide-react'
import { useAuthStore } from '../../../stores/useAuthStore'
import { openProjectAskConversation } from '../../../utils/projectAskConversation'
import { openProjectInPlace } from '../../../utils/openProjectInPlace'
import { getRawActiveWorkspaceId } from '../../../utils/desktopConnection'
import { remoteProjectConnection } from '../../../utils/remoteSessions'
import { WorkingTipsContext } from '../../../components/chat/workingTipsContext'
import { BOTS_CHANGED_EVENT, botError, listBots, setProjectBot, type Bot, type BotProject } from './botsApi'
import { useBotsPageStore, type BotChatFrom } from './botsPageStore'
import { BotAvatar } from './BotAvatar'

const ChatArea = lazy(() => import('../../../components/ChatArea'))

/** The bots shared with you, each with your projects you can add it to, read again whenever bots change. */
function useBots() {
  const [state, setState] = useState<{ bots: Bot[] | null; error: string }>({ bots: null, error: '' })
  const [retry, setRetry] = useState(0)
  useEffect(() => {
    let cancelled = false
    const load = () => {
      void listBots().then(bots => { if (!cancelled) setState({ bots, error: '' }) })
        .catch(reason => { if (!cancelled) setState({ bots: [], error: botError(reason, 'Could not load bots.') }) })
    }
    load()
    window.addEventListener(BOTS_CHANGED_EVENT, load)
    return () => { cancelled = true; window.removeEventListener(BOTS_CHANGED_EVENT, load) }
  }, [retry])
  return { ...state, retry: () => setRetry(value => value + 1) }
}

/** Open one of your projects in place: a server project on its server (from a Mac, through that server's sign-in),
 * a project on this Mac here. */
function openBotProject(projectId: string, server?: string) {
  const connection = remoteProjectConnection(projectId) ?? server ?? getRawActiveWorkspaceId()
  openProjectInPlace(connection, { surface: 'work', id: projectId })
}

/** Open a bot's chat as used in a project it was added to; Back returns to that project. */
function openInProject(bot: Bot, project: BotProject) {
  useBotsPageStore.getState().openChat(bot.id, { projectId: project.project_id, title: project.title, server: project.mac ? undefined : bot.server?.id })
}

/** On a Mac, bots grouped by the server they are on, in the order the servers come; on the website, one group. */
function byServer(bots: Bot[]): { name: string; bots: Bot[] }[] {
  const groups: { name: string; bots: Bot[] }[] = []
  for (const bot of bots) {
    const name = bot.server?.name ?? ''
    const group = groups.find(item => item.name === name)
    if (group) group.bots.push(bot); else groups.push({ name, bots: [bot] })
  }
  return groups
}

/**
 * Bots (#4545): the company's directory of shared agents. Each one is an agent of a project on the server that its
 * maker shared, with the functions the maker picked. You use a bot inside one of your projects (add it there; it shows
 * in that project's agent list) or from your own AI apps over MCP. A bot opened from a project is your own private chat.
 * The server lists only the bots shared with you and decides every call.
 */
export default function BotsPage() {
  const chat = useBotsPageStore(state => state.chat)
  const from = useBotsPageStore(state => state.from)
  return chat ? <BotChatPage key={chat} botId={chat} from={from} /> : <BotsDirectory />
}

function BotsDirectory() {
  const close = useBotsPageStore(state => state.setOpen)
  const { bots, error, retry } = useBots()
  const groups = bots ? byServer(bots) : []
  const onMac = groups.some(group => group.name)
  return <div className="flex h-full min-h-0 flex-col bg-background">
    <header className="flex h-12 shrink-0 items-center gap-2 border-b border-border px-4">
      <BotIcon aria-hidden="true" className="h-4 w-4" />
      <h1 className="text-sm font-semibold">Bots</h1>
      {bots && bots.length > 0 && <span className="rounded-full bg-muted px-1.5 text-[11px] text-muted-foreground">{bots.length}</span>}
      <span className="truncate text-xs text-muted-foreground">Agents shared with you</span>
      <button type="button" aria-label="Close Bots" onClick={() => close(false)} className="ml-auto grid h-7 w-7 shrink-0 place-items-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground"><X aria-hidden="true" className="h-4 w-4" /></button>
    </header>
    <div className="min-h-0 flex-1 overflow-y-auto p-4">
      {bots === null && <p role="status" className="text-sm text-muted-foreground">Loading bots…</p>}
      {error && <div className="text-sm"><p role="alert">{error}</p><button type="button" className="mt-2 text-primary underline" onClick={retry}>Try again</button></div>}
      {bots?.length === 0 && !error && <div className="max-w-lg rounded-xl border border-border p-4 text-sm">
        <p aria-hidden="true" className="text-2xl">🤖</p>
        <p className="mt-1 font-semibold">No bots yet</p>
        <p className="mt-1 text-muted-foreground">Bots are AI teammates you can give real work to. Each one is a colleague's agent with its whole project behind it: files, memory, skills, connected apps and workflows. Message it like a teammate in your own private chat, or add it to your projects so your agents can hand it work directly.</p>
        <p className="mt-2 text-xs italic text-muted-foreground">A QA bot reproduces a bug, files the ticket and hands the fix to the debugging bot. A release bot runs the smoke tests and tells you what broke and why.</p>
        <p className="mt-2 text-xs text-muted-foreground"><span className="font-medium text-foreground">To get one:</span> ask a colleague to share their agent with you.</p>
        <p className="mt-1 text-xs text-muted-foreground"><span className="font-medium text-foreground">To share yours:</span> turn on Bot in your agent's menu (projects on the server).</p>
      </div>}
      {bots && bots.length > 0 && <>
        <p className="mb-3 text-sm text-muted-foreground">Add a bot to one of your projects to use it there, or reach it from your own AI apps.{onMac ? ' Add to a project lists your projects on this Mac and on the bot\'s server.' : ''}</p>
        {groups.map(group => <section key={group.name} aria-label={group.name || undefined} className="mb-5">
          {group.name && <h2 className="mb-2 flex items-center gap-1.5 text-xs font-semibold">{group.name}<span className="rounded-full bg-muted px-1.5 text-[11px] font-normal text-muted-foreground">{group.bots.length}</span></h2>}
          <div className="grid gap-3 [grid-template-columns:repeat(auto-fill,minmax(260px,1fr))]">
            {group.bots.map(bot => <DirectoryCard key={bot.id} bot={bot} targets={(bot.add_targets ?? []).filter(target => target.project_id !== bot.project_id)} />)}
          </div>
        </section>)}
      </>}
    </div>
  </div>
}

/** One bot in the directory: what it does, its functions, and how to use it. No chat here. */
function DirectoryCard({ bot, targets }: { bot: Bot; targets: BotProject[] }) {
  const [mcp, setMcp] = useState(false)
  const inProjects = bot.in_projects ?? []
  const buttons = bot.functions.filter(fn => fn.name !== 'ask')
  return <div className="flex flex-col rounded-xl border border-border p-3.5">
    <div className="flex items-start gap-2.5">
      <BotAvatar bot={bot} connection={bot.server?.id} className="h-9 w-9 text-lg" />
      <div className="min-w-0 flex-1">
        <p className="flex items-center gap-1.5"><span className="truncate text-sm font-semibold">{bot.name}</span>{bot.mine && <span className="shrink-0 rounded-full bg-primary/15 px-1.5 text-[10px] font-medium text-primary">Yours</span>}</p>
        <p className="truncate text-[11px] text-muted-foreground">{bot.project_title} · {bot.mine ? 'by you' : `by ${bot.maker_name}`}</p>
        {bot.purpose && <p className="mt-1 text-xs">{bot.purpose}</p>}
      </div>
    </div>
    {buttons.length > 0 && <>
      <p className="mt-2.5 text-[11px] text-muted-foreground">Your agents can call</p>
      {/* Each function with what it does, so people and their agents know when to call it. */}
      <ul className="mt-1 space-y-1.5">
        {buttons.map(fn => <li key={fn.name} className="min-w-0">
          <code className="rounded-full bg-muted px-2 py-0.5 text-[11px]">{fn.name}</code>
          {fn.workflow && <span className="ml-1 text-[10px] text-muted-foreground">Workflow · {fn.workflow}</span>}
          {fn.description && <p className="mt-0.5 line-clamp-2 px-1 text-[11px] text-muted-foreground" title={fn.description}>{fn.description}</p>}
        </li>)}
      </ul>
    </>}
    <div className="mt-auto pt-3">
      {bot.mine ? <>
        <p className="text-[11px] text-muted-foreground">Yours, from {bot.project_title}. Change it from the agent's menu there. People you shared it with see it here.</p>
        <div className="mt-2 flex flex-wrap items-center gap-1.5">
          <button type="button" onClick={() => openBotProject(bot.project_id, bot.server?.id)} className="rounded-md border border-border px-2.5 py-1 text-xs hover:bg-muted">Open in {bot.project_title}</button>
          <McpButton open={mcp} toggle={() => setMcp(!mcp)} />
        </div>
      </> : <AddOrOpen bot={bot} targets={targets} inProjects={inProjects} mcp={<McpButton open={mcp} toggle={() => setMcp(!mcp)} />} />}
      {mcp && <McpHelp bot={bot} />}
    </div>
  </div>
}

function McpButton({ open, toggle }: { open: boolean; toggle: () => void }) {
  return <button type="button" aria-expanded={open} onClick={toggle} title="Use it from Claude, Cursor or another AI app" className="rounded-md px-2 py-1 text-xs text-muted-foreground hover:bg-muted hover:text-foreground">Use over MCP</button>
}

function McpHelp({ bot }: { bot: Bot }) {
  return <div className="mt-2 rounded-lg bg-muted p-2.5 text-[11px] text-muted-foreground">
    <p>Connect Claude, Cursor or another AI app to AgentWorks once, then ask it in plain words:</p>
    <p className="my-1.5 rounded-md bg-background px-2 py-1 text-foreground">"Ask {bot.name} {bot.purpose ? 'to help with this' : 'a question'}"</p>
    <p>It uses the <code>bots</code> tool: list bots, ask one, or run one of its functions by name. Your access and costs are the same as in the app.</p>
  </div>
}

/** Someone else's bot: the projects you added it to (open it there, or remove it), and Add to a project. */
function AddOrOpen({ bot, targets, inProjects, mcp }: { bot: Bot; targets: BotProject[]; inProjects: BotProject[]; mcp: ReactNode }) {
  const [pick, setPick] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const choices = targets.filter(target => !inProjects.some(item => item.project_id === target.project_id))
  const change = async (project: BotProject, add: boolean) => {
    setBusy(true); setError('')
    try {
      await setProjectBot(project.project_id, bot.id, add, bot.server)
      setPick('')
      if (add) openInProject(bot, project)
    } catch (reason) { setError(botError(reason, 'Could not change it.')) } finally { setBusy(false) }
  }
  const picked = choices.find(target => target.project_id === pick)
  return <>
    {inProjects.length > 0 ? <div className="space-y-1">
      {inProjects.map(project => <div key={project.project_id} className="flex items-center gap-2 text-xs">
        <span className="truncate rounded-full bg-emerald-500/10 px-2 py-0.5 text-emerald-700 dark:text-emerald-300">✓ In {project.title}</span>
        <button type="button" disabled={busy} onClick={() => void change(project, false)} className="shrink-0 text-[11px] text-muted-foreground hover:text-destructive">Remove</button>
      </div>)}
      <p className="text-[11px] text-muted-foreground">Open it in {inProjects.length === 1 ? `${inProjects[0].title}'s` : 'the project\'s'} agent list to use it.</p>
    </div> : <p className="text-[11px] text-muted-foreground">{targets.length ? 'To use it, add it to one of your projects. It shows in that project\'s agent list.' : 'To use it, add it to one of your projects. You have none yet: make one, then come back.'}</p>}
    <div className="mt-2 flex flex-wrap items-center gap-1.5">
      {inProjects.length > 0 && <button type="button" onClick={() => openInProject(bot, inProjects[0])} className="rounded-md border border-border px-2.5 py-1 text-xs hover:bg-muted">Open in {inProjects[0].title}</button>}
      {choices.length > 0 && <span className="flex min-w-0 gap-1">
        <select aria-label={`Add ${bot.name} to a project`} value={pick} onChange={event => setPick(event.target.value)} className="min-w-0 max-w-[10rem] rounded-md border border-input bg-background px-1 py-1 text-xs">
          <option value="">{inProjects.length ? 'Add to another…' : 'Add to a project…'}</option>
          {choices.map(target => <option key={`${target.mac ? 'mac' : 'server'}:${target.project_id}`} value={target.project_id}>{target.title}{target.mac ? ' · this Mac' : ''}</option>)}
        </select>
        <button type="button" disabled={!picked || busy} onClick={() => picked && void change(picked, true)} className="rounded-md bg-primary px-2.5 py-1 text-xs text-primary-foreground disabled:opacity-50">{busy ? 'Adding…' : 'Add'}</button>
      </span>}
      {mcp}
    </div>
    {error && <p role="alert" className="mt-1 text-[11px] text-destructive">{error}</p>}
  </>
}

/** Your own private chat with a bot, opened from a project it was added to. Its functions are buttons above the box. */
function BotChatPage({ botId, from }: { botId: string; from: BotChatFrom | null }) {
  const close = useBotsPageStore(state => state.setOpen)
  const { bots, error, retry } = useBots()
  const bot = bots?.find(item => item.id === botId)
  const [removing, setRemoving] = useState('')
  const remove = async () => {
    if (!from) return
    setRemoving('…')
    try { await setProjectBot(from.projectId, botId, false); close(false) } catch (reason) { setRemoving(botError(reason, 'Could not remove it.')) }
  }
  const back = from?.title ? `Back to ${from.title}` : 'Back to the project'
  return <div className="flex h-full min-h-0 flex-col bg-background">
    <header className="flex min-h-12 shrink-0 items-center gap-2.5 border-b border-border px-3 py-1.5">
      <button type="button" aria-label={back} title={back} onClick={() => { if (from) openBotProject(from.projectId, from.server); close(false) }} className="grid h-7 w-7 shrink-0 place-items-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground"><ArrowLeft aria-hidden="true" className="h-4 w-4" /></button>
      {bot ? <BotAvatar bot={bot} connection={bot.server?.id ?? bot.connection} /> : <BotAvatar bot={{ name: 'Bot' }} />}
      <div className="min-w-0 flex-1">
        <h1 className="truncate text-sm font-semibold">{bot?.name ?? 'Bot'}</h1>
        {bot && <p className="truncate text-[11px] text-muted-foreground">Bot from {bot.project_title} · {bot.mine ? 'by you' : `by ${bot.maker_name}`}</p>}
      </div>
      <span title="Only you see this chat" className="inline-flex shrink-0 items-center gap-1 rounded-full bg-muted px-2 py-0.5 text-[11px] text-muted-foreground"><Lock aria-hidden="true" className="h-3 w-3" />Your chat</span>
    </header>
    {bots === null ? <p role="status" className="p-4 text-sm text-muted-foreground">Opening…</p>
      : error ? <div className="p-4 text-sm"><p role="alert">{error}</p><button type="button" className="mt-2 text-primary underline" onClick={retry}>Try again</button></div>
      : !bot ? <div role="alert" className="m-4 rounded-lg bg-destructive/10 p-3 text-sm text-destructive">
        This bot is no longer shared with you, so it can't answer. Ask its maker to share it again{from ? <>, or <button type="button" className="underline" onClick={() => void remove()}>remove it from this project</button></> : ''}.
        {removing && removing !== '…' && <p className="mt-1">{removing}</p>}
      </div>
      : <BotChat bot={bot} />}
  </div>
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
    if (viewer) void openProjectAskConversation({ workspacePath: bot.workspace, projectId: bot.project_id }, { activate: false, agentKey: bot.id, connection: bot.server?.id })
      .then(id => { if (!cancelled) setTabId(id) })
      .catch(reason => { if (!cancelled) setError(reason instanceof Error ? reason.message : botError(reason, 'Could not open the chat.')) })
    return () => { cancelled = true }
  }, [bot.id, bot.workspace, bot.project_id, bot.server?.id, viewer, retry])
  const intro = <div className="mx-auto max-w-xl p-6 text-sm text-muted-foreground">
    <p>{bot.purpose || 'Ask it anything.'}</p>
    <p className="mt-1 text-xs">Only you see this chat. It runs with {bot.project_title}'s setup and its cost is yours; you can't see that project's files.</p>
  </div>
  return <section aria-label={`Chat with ${bot.name}`} className="flex min-h-0 flex-1 flex-col">
    {tabId ? <WorkingTipsContext.Provider value={false}><Suspense fallback={<p role="status" className="p-4 text-sm">Opening chat…</p>}><ChatArea tabId={tabId} compact hideHeader previousChatsWorkspacePath={bot.workspace} landingContent={intro} composerPlaceholder={`Ask ${bot.name}…`} /></Suspense></WorkingTipsContext.Provider>
      : <div className="p-4 text-sm text-muted-foreground"><p role="status">{error || 'Opening chat…'}</p>{error && <button type="button" onClick={() => setRetry(value => value + 1)} className="mt-2 underline">Try again</button>}</div>}
  </section>
}
