import { useEffect, useState } from 'react'
import api from '../../services/api'
import { hasDesktopConnections } from '../../stores/useConnectedWorkStore'
import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'
import { Button } from '../../components/ui/Button'

export type MacCrewBinding = { connection_id?: string; peer_server?: string; peer_project_id: string; editing: 'local' | 'server'; pending_action?: 'update' | 'server' | 'local' | 'download' }

/** The Mac app's own copy of where this Crew lives (owner, 3 Nov): the server's pair says it; this only reads it. */
export function useMacCrew(projectId?: string, pollMs = 5000) {
  const [binding, setBinding] = useState<MacCrewBinding | null>()
  const [reload, setReload] = useState(0)
  const enabled = Boolean(projectId) && hasDesktopConnections() && getRawActiveWorkspaceId() === 'local'
  useEffect(() => {
    setBinding(undefined)
    if (!enabled || !projectId) return
    const abort = new AbortController()
    const load = () => void api.get<{ binding: MacCrewBinding | null }>(`/api/crew-sync/${encodeURIComponent(projectId)}`, { signal: abort.signal, skipSessionContext: true })
      .then(({ data }) => { if (!abort.signal.aborted) setBinding(data.binding) }).catch(() => {})
    load()
    const timer = window.setInterval(load, pollMs)
    return () => { window.clearInterval(timer); abort.abort() }
  }, [enabled, projectId, pollMs, reload])
  const online = binding?.editing === 'server'
  return { enabled, binding, loaded: binding !== undefined, livesOnMac: enabled && binding !== undefined && !online, online, refresh: () => setReload(value => value + 1) }
}

/** Functions, webhooks and dashboard links work only online (owner, 3 Nov). They can be built here and go live after the move. */
export function OnlineOnlyNote({ projectId }: { projectId?: string }) {
  const { livesOnMac } = useMacCrew(projectId, 15000)
  if (!livesOnMac) return null
  return <div role="note" className="flex items-center gap-2 rounded-md border border-border bg-muted/40 p-2 text-xs">
    <span className="flex-1">Works once this Crew is online.</span>
    <Button size="sm" variant="outline" onClick={() => window.dispatchEvent(new CustomEvent('agentworks:open-where-it-lives', { detail: { projectId } }))}>Move online</Button>
  </div>
}
