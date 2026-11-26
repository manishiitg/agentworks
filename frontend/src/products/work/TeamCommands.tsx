import { useCallback, useEffect, useState } from 'react'
import { Terminal } from 'lucide-react'
import { shortAgo } from '../../components/chat/AgentIcon'
import { commandsApi } from '../../api/commands'
import { loadAndRegisterUserCommands } from '../../commands'
import { useChatStore } from '../../stores/useChatStore'
import { sendToOpenChat } from '../../utils/agentPurposeDraft'
import type { UserCommand } from '../../types/commands'
import { teamFailure, teamRequest, type TeamTarget } from './teamApi'
import { belongsToWorkProject } from './workTabs'

/** Slash commands the Team shares (#3311, owner 19 Nov): optional both ways. Sharing puts one command in the Team's
 * shared folder as <folder>/commands/<id>.json: its name, description and the prompt it sends, nothing else. Add copies
 * it into the person's own Crew commands, where it shows when they type "/"; a command only holds text, so adding runs
 * nothing. The prompt is shown before adding, and a name the person already has must be changed first. Later edits do
 * not change copies already added. A copy that now differs from the shared command shows a marker and "Ask my agent to
 * update" (#3458, owner 20 Nov): the agent compares the two in plain words, merges if both changed and saves only after
 * the person says yes; nothing is updated from this screen. */

type SharedFile = { path: string; hash: string; author?: string; mine?: boolean; modified_at?: string }
type CommandFile = { kind: 'command'; name: string; description: string; prompt: string }
type SharedCommand = SharedFile & { file: CommandFile }
const route = (path: string, hash = '') => `/file?path=${encodeURIComponent(path)}&base=${encodeURIComponent(hash)}&transfer=${encodeURIComponent(crypto.randomUUID())}`
/** What the server accepts as a command's folder name. */
const nameOf = (text: string) => text.trim().replace(/[^A-Za-z0-9_-]+/g, '-')

/** The prompt without the {{context}} slot the server adds to every command, so a fresh copy is not "different". */
const plain = (text: string) => text.replace(/\{\{context\}\}/g, '').trim()

/** The Crew's own commands folder is found through its open chat; without one there is nothing to read or add to. */
function useCrewWorkspace(projectId: string): string {
  return useChatStore(state => {
    const tab = Object.values(state.chatTabs).find(item => belongsToWorkProject(item, projectId) && item.metadata?.agentProfileWorkspace)
    return tab?.metadata?.agentProfileWorkspace ?? ''
  })
}

/** `folder` is the Team's first shared folder; no shared folder means nothing to show here. */
export function TeamCommands({ target, folder }: { target: TeamTarget; folder?: string }) {
  const workspace = useCrewWorkspace(target.project)
  const [shared, setShared] = useState<SharedCommand[]>([])
  const [mine, setMine] = useState<UserCommand[]>([])
  const [pick, setPick] = useState('')
  const [adding, setAdding] = useState<SharedCommand | null>(null)
  const [name, setName] = useState('')
  const [added, setAdded] = useState('')
  const [busy, setBusy] = useState(false)
  const [line, setLine] = useState('')
  const dir = folder === undefined ? '' : `${folder === '.' ? '' : `${folder}/`}commands/`
  const load = useCallback(async () => {
    if (folder === undefined) return
    try {
      const data = await teamRequest<{ files?: SharedFile[] }>(target, '/files')
      const files = (data.files ?? []).filter(file => file.path.startsWith(dir) && file.path.endsWith('.json')).slice(0, 40)
      const read = await Promise.all(files.map(async file => {
        try {
          const parsed = JSON.parse(await teamRequest<string>(target, `/file?path=${encodeURIComponent(file.path)}`, 'GET', undefined, true)) as Partial<CommandFile>
          return parsed.kind === 'command' && parsed.name?.trim() && parsed.prompt?.trim() ? { ...file, file: { kind: 'command', name: parsed.name, description: parsed.description ?? '', prompt: parsed.prompt } as CommandFile } : null
        } catch { return null }
      }))
      setShared(read.filter((item): item is SharedCommand => !!item))
    } catch { setShared([]) }
    if (workspace) { try { setMine((await commandsApi.listCommands(workspace)).commands ?? []) } catch { setMine([]) } }
  }, [target.project, target.connection, target.folderId, dir, workspace]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    void load()
    window.addEventListener('agentworks:team-updated', load)
    return () => window.removeEventListener('agentworks:team-updated', load)
  }, [load])
  if (folder === undefined) return null
  const run = async (work: () => Promise<string>) => {
    setBusy(true)
    try { setLine(await work()) } catch (e) { setLine(teamFailure(e).message) } finally { setBusy(false); void load() }
  }
  const choice = mine.find(command => command.folder_name === pick) ?? mine[0]
  const share = () => run(async () => {
    if (!choice) return ''
    const file: CommandFile = { kind: 'command', name: choice.folder_name, description: choice.frontmatter.description ?? '', prompt: choice.content }
    // Sharing the same command again updates your own file; someone else's command with the same name stays theirs.
    const own = shared.find(item => item.mine && item.file.name.toLowerCase() === choice.folder_name.toLowerCase())
    await teamRequest(target, route(own?.path ?? `${dir}${crypto.randomUUID()}.json`, own?.hash), 'PUT', JSON.stringify(file, null, 2), true)
    return `Shared /${choice.folder_name} with the Team. Teammates can add it if they want.`
  })
  const open = (item: SharedCommand) => { setAdding(item); setAdded(''); setLine(''); setName(nameOf(item.file.name)) }
  /** The person's own command of the same name, when it no longer matches the shared one. */
  const copyOf = (item: SharedCommand) => mine.find(command => command.folder_name.toLowerCase() === item.file.name.toLowerCase())
  const differs = (item: SharedCommand) => {
    const copy = copyOf(item)
    return !!copy && (plain(copy.content) !== plain(item.file.prompt) || (copy.frontmatter.description ?? '').trim() !== (item.file.description || item.file.name).trim())
  }
  const askAgent = (item: SharedCommand) => {
    const name = item.file.name
    const message = item.mine
      ? `My /${name} command differs from the version I shared with the Team. Compare the two, tell me in plain words what is different, and ask me before you update the shared version.`
      : `The Team's shared /${name} command differs from my /${name}. Compare the two (the Team files and my commands), tell me in plain words what is different, and ask me before you change anything: take theirs, keep mine, or merge both.`
    if (!sendToOpenChat(target.project, message)) useChatStore.getState().addToast('Open one of your own agent chats in this project, then try again.', 'info')
  }
  const taken = !!adding && mine.some(command => command.folder_name.toLowerCase() === name.toLowerCase())
  const add = () => adding && run(async () => {
    try {
      const content = `---\nname: ${name}\ndescription: ${JSON.stringify(adding.file.description || name)}\nmodes:\n  - multi-agent\n---\n\n${adding.file.prompt.trim()}\n`
      await commandsApi.createCommand({ name, content }, workspace)
      await loadAndRegisterUserCommands(workspace)
      setAdded(name); setAdding(null)
      return ''
    } catch (e) {
      // The server says why in plain words.
      const why = (e as { response?: { data?: unknown } })?.response?.data
      return typeof why === 'string' && why.trim() ? why.trim() : 'Could not add that command. Try again.'
    }
  })
  const stop = (item: SharedCommand) => run(async () => {
    await teamRequest(target, route(item.path, item.hash), 'DELETE')
    return `Stopped sharing /${item.file.name}. Copies teammates already added stay with them.`
  })
  return <section aria-label="Shared commands" className="space-y-1.5">
    <h4 className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">Commands <span className="font-normal normal-case tracking-normal">· share a / command; teammates add it only if they want it</span></h4>
    {shared.length > 0 && <ul className="space-y-1.5">{shared.map(item => <li key={item.path} className="rounded-xl border border-border bg-muted/20 px-3 py-2">
      <div className="flex items-center gap-3">
        <Terminal className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
        <span className="min-w-0 flex-1"><span className="block truncate font-medium">/{item.file.name}{differs(item) && <span className="ml-1.5 rounded-full border border-border px-2 text-[11px] font-normal text-muted-foreground">{item.mine ? 'Your copy changed' : 'Differs from yours'}</span>}</span><span className="block text-[11px] text-muted-foreground">{[item.file.description, item.mine ? 'Shared by you' : item.author && `Shared by ${item.author}`, shortAgo(item.modified_at)].filter(Boolean).join(' · ')}</span></span>
        {differs(item) && <button type="button" onClick={() => askAgent(item)} className="rounded-lg border border-border px-2.5 py-1 text-xs hover:bg-muted" aria-label={`Ask my agent to update ${item.file.name}`}>Ask my agent to update</button>}
        {!item.mine && <button type="button" disabled={busy || !workspace} onClick={() => open(item)} className="rounded-lg border border-border px-2.5 py-1 text-xs hover:bg-muted disabled:opacity-50" aria-label={`Add ${item.file.name} to my commands`}>Add to my commands</button>}
        {item.mine && <button type="button" disabled={busy} onClick={() => void stop(item)} className="rounded-lg px-2 py-1 text-xs text-muted-foreground hover:bg-muted disabled:opacity-50" aria-label={`Stop sharing ${item.file.name}`}>Stop sharing</button>}
      </div>
      {adding?.path === item.path && <div className="mt-2 space-y-2 border-t border-border pt-2 text-xs">
        <label className="grid gap-0.5 text-muted-foreground">What it sends<textarea aria-label="What it sends" readOnly value={item.file.prompt} className="min-h-14 w-full resize-y rounded border border-input bg-background px-2 py-1 text-foreground" /></label>
        <label className="grid gap-0.5 text-muted-foreground">Name in your / menu<input aria-label="Command name" value={name} onChange={event => setName(nameOf(event.target.value))} className="rounded border border-input bg-background px-2 py-1 text-foreground" /></label>
        {taken && <p role="status" className="text-muted-foreground">You already have /{name}. Pick another name.</p>}
        <div className="flex justify-end gap-2">
          <button type="button" disabled={busy} onClick={() => setAdding(null)} className="rounded-lg border border-border px-2.5 py-1 hover:bg-muted disabled:opacity-50">Cancel</button>
          <button type="button" disabled={busy || !name || taken} onClick={() => void add()} className="rounded-lg bg-primary px-2.5 py-1 font-semibold text-primary-foreground hover:opacity-90 disabled:opacity-50">Add to my commands</button>
        </div>
      </div>}
    </li>)}</ul>}
    {!shared.length && <p className="text-xs text-muted-foreground">No shared commands yet. Share one of yours and teammates can add it if they want.</p>}
    {mine.length > 0 && <div className="flex flex-wrap items-center gap-2 text-xs">
      <label className="flex items-center gap-2"><span className="text-muted-foreground">Share one of your commands</span>
        <select aria-label="Command to share" value={choice?.folder_name ?? ''} disabled={busy} onChange={event => setPick(event.target.value)} className="max-w-48 rounded border border-input bg-background px-2 py-1">{mine.map(command => <option key={command.folder_name} value={command.folder_name}>/{command.folder_name}</option>)}</select>
      </label>
      <button type="button" disabled={busy || !choice} onClick={() => void share()} className="rounded-lg border border-border px-2.5 py-1 hover:bg-muted disabled:opacity-50">Share with the Team</button>
    </div>}
    {added && <p role="status" className="text-xs text-emerald-600">Added /{added} to your commands. Type / in a chat to use it.</p>}
    {line && <p role="status" className="text-xs text-muted-foreground">{line}</p>}
  </section>
}
