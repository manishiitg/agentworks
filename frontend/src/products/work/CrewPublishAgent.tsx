import { useEffect, useState } from 'react'
import { getAuthToken, getDesktopApiBaseUrl } from '../../services/api'
import type { ListScheduledJobsResponse } from '../../services/api-types'
import { desktopRequest } from '../../services/desktopConnections'
import { Button } from '../../components/ui/Button'

type Agent = { tab: string; name: string; purpose: string; icon?: string }
type Selection = { tab: string; paths: string[]; account_id?: string }
export type AgentPlacement = { location: 'publishing' | 'server'; since?: string; transfer_id: string }
type Preview = {
  ready: boolean; message?: string; needs_login?: boolean; agents?: Agent[]; selection: Selection; transfer_id?: string
  accounts: { id: string; name: string; identity?: string }[]
  files: number; bytes: number; native_bytes: number; callers: string[]; warnings?: string[]
}
type Reply = { publish?: Preview; placements?: Record<string, AgentPlacement> }
const size = (bytes: number) => bytes < 1024 * 1024 ? `${Math.ceil(bytes / 1024)} KB` : `${(bytes / (1024 * 1024)).toFixed(1)} MB`

export function CrewPublishAgent({ projectId, connection, serverLink, serverName, reportedPlacements, onAgentCount }: { projectId: string; connection: string; serverLink: string; serverName: string; reportedPlacements?: Record<string, AgentPlacement>; onAgentCount?: (count: number) => void }) {
  const [agents, setAgents] = useState<Agent[]>([])
  const [placements, setPlacements] = useState<Record<string, AgentPlacement>>({})
  const [serverSchedules, setServerSchedules] = useState<Record<string, number>>()
  const [selected, setSelected] = useState('')
  const [paths, setPaths] = useState('')
  const [wholeFolder, setWholeFolder] = useState(false)
  const [preview, setPreview] = useState<Preview>()
  const [account, setAccount] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const endpoint = `/${encodeURIComponent(connection)}/crew-sync/${encodeURIComponent(projectId)}`
  useEffect(() => {
    let cancelled = false
    setAgents([]); setPlacements({}); setSelected(''); setPreview(undefined); setError('')
    void desktopRequest<Reply>(endpoint, 'POST', { action: 'publish_agents' }).then(reply => {
      if (!cancelled) { setAgents(reply.publish?.agents || []); onAgentCount?.((reply.publish?.agents || []).length); setPlacements(reply.placements || {}) }
    }).catch(cause => { if (!cancelled) setError(cause instanceof Error ? cause.message : 'Could not load agents.') })
    return () => { cancelled = true }
  }, [endpoint, onAgentCount])
  useEffect(() => { if (reportedPlacements) setPlacements(reportedPlacements) }, [reportedPlacements])
  const publishedState = Object.entries(placements).map(([tab, place]) => `${tab}:${place.location}:${place.transfer_id}`).sort().join('|')
  useEffect(() => {
    const controller = new AbortController()
    setServerSchedules(undefined)
    const load = async () => {
      const prefix = `${getDesktopApiBaseUrl()}/api/connections/${encodeURIComponent(connection)}`
      const read = async <T,>(path: string): Promise<T> => {
        const response = await fetch(prefix + path, { signal: controller.signal, headers: { Authorization: `Bearer ${getAuthToken() || ''}` } })
        if (!response.ok) throw new Error('Schedule status unavailable')
        return response.json() as Promise<T>
      }
      const serverProject = new URL(serverLink).pathname.split('/').pop()
      if (!serverProject) return
      const [status, schedules] = await Promise.all([
        read<{ workspace?: string }>(`/api/crew-sync/${serverProject}`),
        read<ListScheduledJobsResponse>('/api/scheduler/jobs?entity_type=product&limit=1000'),
      ])
      if (!status.workspace || schedules.total > schedules.jobs.length || controller.signal.aborted) return
      const counts: Record<string, number> = {}
      for (const job of schedules.jobs) {
        if (!job.enabled || job.workspace_path?.replace(/\/+$/, '') !== status.workspace.replace(/\/+$/, '')) continue
        const tab = job.chat_id?.split(':chat:').pop() || 'main'
        counts[tab] = (counts[tab] || 0) + 1
      }
      setServerSchedules(counts)
    }
    void load().catch(() => {})
    const focus = () => { void load().catch(() => {}) }
    window.addEventListener('focus', focus)
    return () => { controller.abort(); window.removeEventListener('focus', focus) }
  }, [connection, serverLink, publishedState])
  const selection: Selection = { tab: selected, paths: wholeFolder ? ['.'] : paths.split('\n').map(p => p.trim()).filter(Boolean), account_id: account || undefined }
  const run = async (confirm: boolean) => {
    setBusy(true); setError('')
    try {
      const pending = placements[selected]
      const reply = await desktopRequest<Reply>(endpoint, 'POST', {
        action: confirm ? 'publish_agent' : 'publish_preview',
        publish_agent: confirm && preview ? preview.selection : selection,
        ...(confirm ? { transfer_id: preview?.transfer_id || pending?.transfer_id } : {}),
      })
      setPlacements(reply.placements || {})
      setPreview(reply.publish)
      if (reply.publish?.selection?.account_id) setAccount(reply.publish.selection.account_id)
      if (reply.placements?.[selected]?.location === 'server') { setSelected(''); setPreview(undefined) }
    } catch (cause) { setError(cause instanceof Error ? cause.message : 'Publishing did not finish. Retry to continue.') }
    finally { setBusy(false) }
  }
  return <div>
    <p className="pb-1 pt-1 text-[12.5px] text-muted-foreground"><b className="font-semibold text-foreground">Or one agent</b> · the others stay on this Mac</p>
    {agents.map(agent => {
      const place = placements[agent.tab]
      const date = place?.since ? new Date(place.since) : undefined
      const since = date && Number.isFinite(date.getTime()) ? date.toLocaleDateString() === new Date().toLocaleDateString() ? date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) : date.toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : ''
      const schedules = place?.location === 'server' ? serverSchedules?.[agent.tab] : undefined
      return <div key={agent.tab} className="flex flex-wrap items-center gap-2.5 border-t border-border py-2.5 text-sm">
        <span aria-hidden="true" className="grid h-[22px] w-[22px] shrink-0 place-items-center rounded-md bg-muted text-xs">{agent.icon || '🤖'}</span>
        <span className="min-w-0 break-words" title={agent.purpose || 'Set a purpose in this agent’s chat first.'}>{agent.name}</span>
        <span className={`rounded-full px-2 py-0.5 text-xs ${place?.location === 'server' ? 'bg-sky-500/10 text-sky-700 dark:text-sky-300' : place ? 'bg-amber-500/10 text-amber-700 dark:text-amber-300' : 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'}`} title={date?.toLocaleString()}>{place?.location === 'server' ? `On the server${since ? ` · since ${since}` : ''}` : place ? 'Publishing…' : 'On this Mac'}</span>
        {schedules !== undefined && schedules > 0 && <span className="text-[12.5px] text-muted-foreground">{schedules} {schedules === 1 ? 'schedule' : 'schedules'} running there</span>}
        {place?.location === 'server' ? <Button size="sm" variant="outline" className="ml-auto h-7 shrink-0 rounded-full px-3" onClick={() => window.open(`${serverLink}?agent=${encodeURIComponent(agent.tab)}`, '_blank', 'noopener,noreferrer')}>Open</Button> : <Button size="sm" className="ml-auto h-7 shrink-0 rounded-full px-3" disabled={busy} onClick={() => { setSelected(agent.tab); setPreview(undefined); setAccount(''); setError('') }}>{place ? 'Finish publish' : 'Publish'}</Button>}
      </div>
    })}
    {selected && <div className="mt-2.5 space-y-3 rounded-xl border border-sky-500/30 bg-sky-500/5 px-3 py-2.5">
      <p className="font-semibold">Publish “{agents.find(agent => agent.tab === selected)?.name}”?</p>
      {!placements[selected] && !preview?.ready && <>
        <label className="flex items-start gap-2 text-[13px]"><input type="checkbox" checked={wholeFolder} disabled={busy} onChange={event => { setWholeFolder(event.target.checked); setPreview(undefined) }} />Include all working files in this Crew</label>
        {!wholeFolder && <label className="block text-[13px]">Working folders or files to include <span className="text-muted-foreground">(optional)</span><textarea className="mt-1 w-full rounded-lg border border-border bg-background p-2" rows={3} value={paths} disabled={busy} placeholder={'One path per line, e.g.\nreports\nsrc'} onChange={event => { setPaths(event.target.value); setPreview(undefined) }} /><span className="text-xs text-muted-foreground">Leave empty to move only its chat, memory and skills. Paths are inside this Crew’s folder; installed dependencies and private platform files stay behind.</span></label>}
      </>}
      {preview && <>
        {preview.accounts?.length > 1 && <label className="block text-[13px]">Your account on {serverName}<select className="mt-1 w-full rounded-lg border border-border bg-background p-2" value={account} disabled={busy} onChange={event => { setAccount(event.target.value); setPreview({ ...preview, ready: false, transfer_id: undefined }) }}><option value="">Choose your account</option>{preview.accounts.map(item => <option key={item.id} value={item.id}>{item.identity || item.name}</option>)}</select></label>}
        {preview.message && <p className="text-[13px]">{preview.message}</p>}
        {preview.needs_login && <a className="text-primary underline text-[13px]" href={`${new URL(serverLink).origin}/settings/ai-accounts`} target="_blank" rel="noreferrer">Open Providers on {serverName}</a>}
        {preview.ready && <div className="space-y-2 text-[13px]">
          <p>It moves to {serverName}: its chat, memory and skills, and {preview.files} selected files ({size(preview.bytes)}). The native chat is {size(preview.native_bytes)}.</p>
          <p>Uses your account: {preview.accounts?.find(item => item.id === preview.selection.account_id)?.identity || preview.accounts?.find(item => item.id === preview.selection.account_id)?.name}.</p>
          <p>{preview.callers?.length ? `Moves ${preview.callers.length} callers:` : 'No schedules, watchers or webhooks belong to this agent.'}</p>
          {!!preview.callers?.length && <ul className="list-disc space-y-1 pl-5">{preview.callers.map((item, i) => <li key={i}>{item}</li>)}</ul>}
          {preview.warnings?.map(warning => <p key={warning} className="text-amber-700 dark:text-amber-400">{warning}</p>)}
          <p>It will run on the server from now on. Your Mac files are kept. Bringing this agent back is not available.</p>
        </div>}
      </>}
      <div className="flex flex-wrap gap-2">
        <Button size="sm" className="rounded-full" disabled={busy} onClick={() => void run(Boolean(placements[selected]) || Boolean(preview?.ready && preview.transfer_id))}>{busy ? 'Working…' : placements[selected] ? 'Retry publish' : preview?.ready && preview.transfer_id ? 'Publish' : 'Review what moves'}</Button>
        <Button size="sm" variant="ghost" disabled={busy} onClick={() => { setSelected(''); setPreview(undefined) }}>Cancel</Button>
      </div>
    </div>}
    {error && <p role="alert" className="text-xs text-amber-700 dark:text-amber-400">{error}</p>}
  </div>
}
