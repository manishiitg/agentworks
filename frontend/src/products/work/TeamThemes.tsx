import { useCallback, useEffect, useState } from 'react'
import { Palette } from 'lucide-react'
import { shortAgo } from '../../components/chat/AgentIcon'
import api from '../../services/api'
import { useAppearanceStore } from '../../stores/useAppearanceStore'
import { teamFailure, teamRequest, type TeamTarget } from './teamApi'

/** Themes the Team shares (#3223, owner 19 Nov): optional both ways. Sharing puts a theme's look in the Team folder as
 * team/themes/<name>.json; Use copies one into the person's own saved themes and never changes the look they have on.
 * The server decides what a shared theme may hold (looks_only: colours, fonts and layout; no custom code or images),
 * on the way out and again on the way in; this only moves the file. */

const folder = 'team/themes/'
type SharedTheme = { path: string; hash: string; author?: string; modified_at?: string }
const fileOf = (name: string) => `${folder}${name.replace(/[\\/:*?"<>|]+/g, '-')}.json`
const nameOf = (path: string) => path.slice(folder.length).replace(/\.json$/, '')
const route = (path: string, hash = '') => `/file?path=${encodeURIComponent(path)}&base=${encodeURIComponent(hash)}&transfer=${encodeURIComponent(crypto.randomUUID())}`

export function TeamThemes({ target }: { target: TeamTarget }) {
  const saved = useAppearanceStore(state => state.saved)
  const [shared, setShared] = useState<SharedTheme[]>([])
  const [pick, setPick] = useState('')
  const [busy, setBusy] = useState(false)
  const [line, setLine] = useState('')
  const load = useCallback(async () => {
    try {
      const data = await teamRequest<{ files?: SharedTheme[] }>(target, '/files')
      setShared((data.files ?? []).filter(file => file.path.startsWith(folder) && file.path.endsWith('.json')))
    } catch { setShared([]) }
  }, [target.project, target.connection, target.folderId]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    void load()
    window.addEventListener('agentworks:team-updated', load)
    return () => window.removeEventListener('agentworks:team-updated', load)
  }, [load])
  const run = async (work: () => Promise<string>) => {
    setBusy(true)
    try { setLine(await work()) } catch (e) { setLine(teamFailure(e).message) } finally { setBusy(false); void load() }
  }
  const choice = pick || saved[0]?.name || ''
  const share = () => run(async () => {
    const { data } = await api.put<{ theme: unknown }>('/api/appearance', { operation: 'export', name: choice, looks_only: true }, { skipSessionContext: true })
    const path = fileOf(choice)
    await teamRequest(target, route(path, shared.find(file => file.path === path)?.hash), 'PUT', JSON.stringify(data.theme, null, 2), true)
    return `Shared “${choice}” with the Team. Only the look is shared: custom code and images stay yours.`
  })
  const use = (file: SharedTheme) => run(async () => {
    let theme: { name?: string }
    try { theme = JSON.parse(await teamRequest<string>(target, `/file?path=${encodeURIComponent(file.path)}`, 'GET', undefined, true)) } catch { return 'That shared file is not a theme.' }
    const mine = useAppearanceStore.getState().saved.some(own => own.name.toLowerCase() === (theme.name || '').toLowerCase())
    await api.put('/api/appearance', { operation: 'import', looks_only: true, theme, ...(mine ? { name: `${theme.name} (Team)` } : {}) }, { skipSessionContext: true })
    await useAppearanceStore.getState().load(useAppearanceStore.getState().scope, { force: true })
    return `Added “${mine ? `${theme.name} (Team)` : theme.name}” to your themes. Your current look is unchanged; choose it from Appearance when you want it.`
  })
  const stop = (file: SharedTheme) => run(async () => {
    await teamRequest(target, route(file.path, file.hash), 'DELETE')
    return `Stopped sharing “${nameOf(file.path)}”. Copies teammates already added stay with them.`
  })
  return <section aria-label="Shared themes" className="space-y-1.5">
    <h4 className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">Themes <span className="font-normal normal-case tracking-normal">· share a look; teammates add it only if they want it</span></h4>
    <ul className="space-y-1.5">{shared.map(file => <li key={file.path} className="flex items-center gap-3 rounded-xl border border-border bg-muted/20 px-3 py-2">
      <Palette className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
      <span className="min-w-0 flex-1"><span className="block truncate font-medium">{nameOf(file.path)}</span><span className="block text-[11px] text-muted-foreground">{[file.author && `Shared by ${file.author}`, shortAgo(file.modified_at)].filter(Boolean).join(' · ')}</span></span>
      <button type="button" disabled={busy} onClick={() => void use(file)} className="rounded-lg border border-border px-2.5 py-1 text-xs hover:bg-muted disabled:opacity-50" aria-label={`Add ${nameOf(file.path)} to my themes`}>Use</button>
      <button type="button" disabled={busy} onClick={() => void stop(file)} className="rounded-lg px-2 py-1 text-xs text-muted-foreground hover:bg-muted disabled:opacity-50" aria-label={`Stop sharing ${nameOf(file.path)}`}>Stop sharing</button>
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
