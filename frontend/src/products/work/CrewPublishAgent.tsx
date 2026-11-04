import { useEffect, useState } from 'react'
import { desktopRequest } from '../../services/desktopConnections'
import { Button } from '../../components/ui/Button'

type Agent = { tab: string; name: string; purpose: string; icon?: string }
type Selection = { tab: string; paths: string[]; account_id?: string }
type Placement = { location: 'publishing' | 'server'; since?: string; transfer_id: string }
type Preview = {
  ready: boolean; message?: string; agents?: Agent[]; selection: Selection; transfer_id?: string
  accounts: { id: string; name: string; identity?: string }[]
  files: number; bytes: number; native_bytes: number; callers: string[]; warnings?: string[]
}
type Reply = { publish?: Preview; placements?: Record<string, Placement> }
const size = (bytes: number) => bytes < 1024 * 1024 ? `${Math.ceil(bytes / 1024)} KB` : `${(bytes / (1024 * 1024)).toFixed(1)} MB`

export function CrewPublishAgent({ projectId, connection, serverLink, serverName }: { projectId: string; connection: string; serverLink: string; serverName: string }) {
  const [agents, setAgents] = useState<Agent[]>([])
  const [placements, setPlacements] = useState<Record<string, Placement>>({})
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
      if (!cancelled) { setAgents(reply.publish?.agents || []); setPlacements(reply.placements || {}) }
    }).catch(cause => { if (!cancelled) setError(cause instanceof Error ? cause.message : 'Could not load agents.') })
    return () => { cancelled = true }
  }, [endpoint])
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
  return <div className="space-y-3 rounded-xl border border-sky-500/25 p-3">
    <p className="font-semibold">One agent</p>
    <p className="text-[13px] text-muted-foreground">Move one agent to {serverName}. Its current native chat, memory, skills and callers go with it. Your other agents stay on this Mac.</p>
    {agents.map(agent => {
      const place = placements[agent.tab]
      return <div key={agent.tab} className="flex items-center justify-between gap-3 rounded-lg border border-border p-3">
        <div className="min-w-0"><p className="font-medium">{agent.icon} {agent.name}</p><p className="text-xs text-muted-foreground break-words">{agent.purpose || 'Set a purpose in this agent’s chat first.'}</p><p className="mt-1 text-xs text-muted-foreground">{place?.location === 'server' ? `On the server${place.since ? ` since ${new Date(place.since).toLocaleDateString()}` : ''}` : place ? 'Publishing to the server' : 'On this Mac'}</p></div>
        {place?.location === 'server' ? <Button size="sm" variant="outline" className="shrink-0 rounded-full" onClick={() => window.open(serverLink, '_blank', 'noopener,noreferrer')}>Open</Button> : <Button size="sm" variant="outline" className="shrink-0 rounded-full" disabled={busy} onClick={() => { setSelected(agent.tab); setPreview(undefined); setAccount(''); setError('') }}>{place ? 'Finish publish' : 'Publish'}</Button>}
      </div>
    })}
    {selected && <div className="space-y-3 rounded-lg bg-muted/40 p-3">
      <p className="font-medium">Publish {agents.find(agent => agent.tab === selected)?.name}</p>
      {!placements[selected] && <>
        <label className="flex items-start gap-2 text-[13px]"><input type="checkbox" checked={wholeFolder} disabled={busy} onChange={event => { setWholeFolder(event.target.checked); setPreview(undefined) }} />Include all working files in this Crew</label>
        {!wholeFolder && <label className="block text-[13px]">Working folders or files to include <span className="text-muted-foreground">(optional)</span><textarea className="mt-1 w-full rounded-lg border border-border bg-background p-2" rows={3} value={paths} disabled={busy} placeholder={'One path per line, e.g.\nreports\nsrc'} onChange={event => { setPaths(event.target.value); setPreview(undefined) }} /><span className="text-xs text-muted-foreground">Leave empty to move only its chat, memory and skills. Paths are inside this Crew’s folder; installed dependencies and private platform files stay behind.</span></label>}
      </>}
      {preview && <>
        {preview.accounts?.length > 1 && <label className="block text-[13px]">Your account on {serverName}<select className="mt-1 w-full rounded-lg border border-border bg-background p-2" value={account} disabled={busy} onChange={event => { setAccount(event.target.value); setPreview({ ...preview, ready: false, transfer_id: undefined }) }}><option value="">Choose your account</option>{preview.accounts.map(item => <option key={item.id} value={item.id}>{item.identity || item.name}</option>)}</select></label>}
        {preview.message && <p className="text-[13px]">{preview.message}</p>}
        {preview.accounts?.length === 0 && <a className="text-primary underline text-[13px]" href={`${new URL(serverLink).origin}/settings/providers`} target="_blank" rel="noreferrer">Open Providers on {serverName}</a>}
        {preview.ready && <div className="space-y-2 text-[13px]">
          <p>{preview.files} files ({size(preview.bytes)}) and the native chat ({size(preview.native_bytes)}).</p>
          <p>Uses your account: {preview.accounts?.find(item => item.id === preview.selection.account_id)?.identity || preview.accounts?.find(item => item.id === preview.selection.account_id)?.name}.</p>
          <p>{preview.callers?.length ? `Moves ${preview.callers.length} callers:` : 'No schedules, watchers or webhooks belong to this agent.'}</p>
          {!!preview.callers?.length && <ul className="list-disc space-y-1 pl-5">{preview.callers.map((item, i) => <li key={i}>{item}</li>)}</ul>}
          {preview.warnings?.map(warning => <p key={warning} className="text-amber-700 dark:text-amber-400">{warning}</p>)}
          <p>It will run on the server from now on. Your Mac files are kept. Bringing this agent back is not available.</p>
        </div>}
      </>}
      <div className="flex flex-wrap gap-2">
        <Button size="sm" className="rounded-full" disabled={busy} onClick={() => void run(Boolean(placements[selected]) || Boolean(preview?.ready && preview.transfer_id))}>{busy ? 'Working…' : placements[selected] ? 'Retry publish' : preview?.ready && preview.transfer_id ? 'Publish agent' : 'Review what moves'}</Button>
        <Button size="sm" variant="ghost" disabled={busy} onClick={() => { setSelected(''); setPreview(undefined) }}>Close</Button>
      </div>
    </div>}
    {error && <p role="alert" className="text-xs text-amber-700 dark:text-amber-400">{error}</p>}
  </div>
}
