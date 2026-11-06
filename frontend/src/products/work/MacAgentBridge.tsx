import { useState } from 'react'
import { desktopRequest } from '../../services/desktopConnections'
import { Switch } from '../../components/ui/Switch'
import { useTeamTab } from './teamApi'

/** Public on AgentBridge, on the Mac online tab only (owner, 6 Nov; decisions log "AgentBridge (Mac online tab)"). The
 * rows are the owner's own agents from the server's Team tab reply (crew_team_tab.go agentbridge), the same in the Mac
 * app and on the website. Only the owner's Mac app switches them (crew-sync public / only_my_agents); the server decides
 * the rules (a purpose first) and its reply shows after the click. Elsewhere the switch shows the setting, and the
 * server's line says where to change it. */
export function MacAgentBridge({ projectId }: { projectId: string }) {
  const state = useTeamTab(projectId)
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  const view = state.view
  const rows = (view?.agents || []).filter(agent => agent.agentbridge)
  if (!rows.length) return null
  const { connection, localProject: local } = state.target || {}
  const setPublic = async (tab: string, on: boolean) => {
    if (!connection || !local) return
    setBusy(tab); setError('')
    try {
      await desktopRequest(`/${encodeURIComponent(connection)}/crew-sync/${encodeURIComponent(local)}`, 'POST', { action: on ? 'public' : 'only_my_agents', key: tab })
      window.dispatchEvent(new CustomEvent('agentworks:team-updated'))
    } catch (cause) { setError(cause instanceof Error ? cause.message : 'It was not saved. Try again.') } finally { setBusy('') }
  }
  const macOnly = rows.some(agent => agent.agentbridge?.mac_only) && !(connection && local)
  return <div aria-label="AgentBridge" className="mt-4">
    <p className="py-2.5"><span className="font-semibold">AgentBridge</span> <span className="text-[12.5px] text-muted-foreground">· agents that can message your agents</span></p>
    {rows.map(agent => {
      const bridge = agent.agentbridge!
      const canSwitch = bridge.can_change && (!bridge.mac_only || Boolean(connection && local))
      return <div key={agent.key} className="border-t border-border py-2.5 pl-3">
        <div className="flex items-center gap-2.5">
          <span title={agent.purpose || undefined} className="min-w-0 flex-1 truncate">{agent.name}</span>
          <span className="text-[12.5px] text-muted-foreground">Public</span>
          <Switch aria-label={`${agent.name}: Public on AgentBridge`} checked={bridge.public} disabled={!canSwitch || busy !== ''} onCheckedChange={on => void setPublic(agent.tab, on)} />
        </div>
        {bridge.public && (bridge.line || agent.address) && <p className="mt-0.5 text-xs text-muted-foreground">{bridge.line}{agent.address && <> <code className="rounded bg-muted px-1 py-0.5">agent:{agent.address}</code></>}</p>}
      </div>
    })}
    {macOnly && view?.agents_mac_only && <p className="border-t border-border pt-2 pl-3 text-xs text-muted-foreground">{view.agents_mac_only}</p>}
    {error && <p role="alert" className="pl-3 text-xs text-amber-600">{error}</p>}
  </div>
}
