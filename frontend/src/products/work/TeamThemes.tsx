import { useCallback, useEffect, useState } from 'react'
import { Palette } from 'lucide-react'
import { shortAgo } from '../../components/chat/AgentIcon'
import api from '../../services/api'
import { useAppearanceStore } from '../../stores/useAppearanceStore'
import { teamFailure, teamRequest, type TeamTarget } from './teamApi'

/** Themes the Team shares (#3223, owner 19 Nov): optional both ways. Sharing puts a whole theme in the Team's shared
 * folder as <folder>/themes/<id>.json (the id, never the typed name, so two people can share the same name); Use copies
 * one into the person's own saved themes and never changes the look they have on. The server decides what a shared
 * theme file is the theme exactly as the appearance export gives it and the appearance import takes it: this only moves
 * it, and the server checks it like any theme the person imports. */

type SharedFile = { path: string; hash: string; author?: string; mine?: boolean; modified_at?: string }
type SharedTheme = SharedFile & { name: string; text: string }
const route = (path: string, hash = '') => `/file?path=${encodeURIComponent(path)}&base=${encodeURIComponent(hash)}&transfer=${encodeURIComponent(crypto.randomUUID())}`

/** `folder` is the Team's first shared folder; no shared folder means nothing to show here. */
export function TeamThemes({ target, folder }: { target: TeamTarget; folder?: string }) {
  const saved = useAppearanceStore(state => state.saved)
  const [shared, setShared] = useState<SharedTheme[]>([])
  const [pick, setPick] = useState('')
  const [busy, setBusy] = useState(false)
  const [line, setLine] = useState('')
  const dir = folder === undefined ? '' : `${folder === '.' ? '' : `${folder}/`}themes/`
  const load = useCallback(async () => {
    if (folder === undefined) return
    try {
      const data = await teamRequest<{ files?: SharedFile[] }>(target, '/files')
      const files = (data.files ?? []).filter(file => file.path.startsWith(dir) && file.path.endsWith('.json')).slice(0, 40)
      const read = await Promise.all(files.map(async file => {
        try {
          const text = await teamRequest<string>(target, `/file?path=${encodeURIComponent(file.path)}`, 'GET', undefined, true)
          const name = (JSON.parse(text) as { name?: unknown }).name
          return typeof name === 'string' && name.trim() ? { ...file, name: name.trim(), text } : null
        } catch { return null }
      }))
      setShared(read.filter((theme): theme is SharedTheme => !!theme))
    } catch { setShared([]) }
  }, [target.project, target.connection, target.folderId, dir]) // eslint-disable-line react-hooks/exhaustive-deps
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
  const choice = pick || saved[0]?.name || ''
  const share = () => run(async () => {
    const { data } = await api.put<{ theme: unknown }>('/api/appearance', { operation: 'export', name: choice }, { skipSessionContext: true })
    // Sharing the same theme again updates your own file; someone else's theme with the same name stays theirs.
    const own = shared.find(theme => theme.mine && theme.name.toLowerCase() === choice.toLowerCase())
    await teamRequest(target, route(own?.path ?? `${dir}${crypto.randomUUID()}.json`, own?.hash), 'PUT', JSON.stringify(data.theme, null, 2), true)
    return `Shared “${choice}” with the Team. It is your whole theme.`
  })
  const use = (theme: SharedTheme) => run(async () => {
    let object: unknown
    try { object = JSON.parse(theme.text) } catch { return 'That shared file is not a theme.' }
    const mine = useAppearanceStore.getState().saved.some(own => own.name.toLowerCase() === theme.name.toLowerCase())
    const name = mine ? `${theme.name} (Team)` : theme.name
    try { await api.put('/api/appearance', { operation: 'import', theme: object, ...(mine ? { name } : {}) }, { skipSessionContext: true }) } catch (e) {
      // The server says why in plain words. A background image or clip missing on this Mac is not an error: the theme falls back to its colours and CSS.
      const why = (e as { response?: { data?: unknown } })?.response?.data
      return typeof why === 'string' && why.trim() ? why.trim() : 'Could not add that theme. Try again.'
    }
    await useAppearanceStore.getState().load(useAppearanceStore.getState().scope, { force: true })
    return `Added “${name}” to your themes. Your current look is unchanged; choose it from Appearance when you want it.`
  })
  const stop = (theme: SharedTheme) => run(async () => {
    await teamRequest(target, route(theme.path, theme.hash), 'DELETE')
    return `Stopped sharing “${theme.name}”. Copies teammates already added stay with them.`
  })
  return <section aria-label="Shared themes" className="space-y-1.5">
    <h4 className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">Themes <span className="font-normal normal-case tracking-normal">· share a look; teammates add it only if they want it</span></h4>
    <ul className="space-y-1.5">{shared.map(theme => <li key={theme.path} className="flex items-center gap-3 rounded-xl border border-border bg-muted/20 px-3 py-2">
      <Palette className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
      <span className="min-w-0 flex-1"><span className="block truncate font-medium">{theme.name}</span><span className="block text-[11px] text-muted-foreground">{[theme.mine ? 'Shared by you' : theme.author && `Shared by ${theme.author}`, shortAgo(theme.modified_at)].filter(Boolean).join(' · ')}</span></span>
      <button type="button" disabled={busy} onClick={() => void use(theme)} className="rounded-lg border border-border px-2.5 py-1 text-xs hover:bg-muted disabled:opacity-50" aria-label={`Add ${theme.name} to my themes`}>Use</button>
      {theme.mine && <button type="button" disabled={busy} onClick={() => void stop(theme)} className="rounded-lg px-2 py-1 text-xs text-muted-foreground hover:bg-muted disabled:opacity-50" aria-label={`Stop sharing ${theme.name}`}>Stop sharing</button>}
    </li>)}</ul>
    {!shared.length && <p className="text-xs text-muted-foreground">No shared themes yet.</p>}
    {saved.length > 0 && <div className="flex flex-wrap items-center gap-2 text-xs">
      <label className="flex items-center gap-2"><span className="text-muted-foreground">Share one of your themes</span>
        <select aria-label="Theme to share" value={choice} disabled={busy} onChange={event => setPick(event.target.value)} className="max-w-48 rounded border border-input bg-background px-2 py-1">{saved.map(theme => <option key={theme.name} value={theme.name}>{theme.name}</option>)}</select>
      </label>
      <button type="button" disabled={busy || !choice} onClick={() => void share()} className="rounded-lg border border-border px-2.5 py-1 hover:bg-muted disabled:opacity-50">Share with the Team</button>
    </div>}
    {line && <p role="status" className="text-xs text-muted-foreground">{line}</p>}
  </section>
}
