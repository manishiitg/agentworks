import { useState } from 'react'
import { Globe } from 'lucide-react'
import { desktopRequest } from '../../services/desktopConnections'
import { Button } from '../../components/ui/Button'
import { Switch } from '../../components/ui/Switch'
import { teamError, teamRequest, useTeamTab } from './teamApi'
import { AskButton, Saving, teamCard } from './TeamTab'
import { askAgentPurpose } from './AgentSharingRow'

/** The Mac online tab's AgentBridge card (owner, 6 Nov, approved mockup): the owner's own agents from the server's
 * Team tab reply (crew_team_tab.go agentbridge), the same on the Mac app and the website. Only the switch is
 * clickable; an agent with no purpose shows Set purpose instead (the server says which). The Mac app switches on its
 * own Mac (crew-sync public / only_my_agents); the website asks the server, which passes it to the Mac and refuses
 * with a plain line while the Mac is offline (crew_team_agentbridge.go). The card always shows (owner, 6 Nov), with a
 * plain line while there are no rows, and ends with the coding-app line: coding apps reach the agents through it. */
export function MacAgentBridge({ projectId, crewName }: { projectId: string; crewName: string }) {
  const state = useTeamTab(projectId)
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  const rows = (state.view?.agents || []).filter(agent => agent.mine && agent.agentbridge)
  const setPublic = async (tab: string, on: boolean) => {
    if (!state.target) return
    const { connection, localProject: local, project } = state.target
    setBusy(tab); setError('')
    try {
      if (connection && local) await desktopRequest(`/${encodeURIComponent(connection)}/crew-sync/${encodeURIComponent(local)}`, 'POST', { action: on ? 'public' : 'only_my_agents', key: tab })
      else await teamRequest({ project, connection }, '/agentbridge', 'POST', { tab, public: on })
      window.dispatchEvent(new CustomEvent('agentworks:team-updated'))
    } catch (cause) { setError(teamError(cause)) } finally { setBusy('') }
  }
  return <section aria-label="AgentBridge" className={teamCard}>
    <div className="flex items-center gap-2"><h3 className="flex-1 font-semibold">AgentBridge</h3><AskButton projectId={projectId} card="agentbridge" /></div>
    <p className="text-[12.5px] text-muted-foreground">A public agent can be found and messaged by other people's agents on this server. It answers as you, with your rules for "when others ask".</p>
    {!rows.length && <p className="text-[12.5px] text-muted-foreground">{state.view ? 'No agents yet.' : 'Your agents will appear here once this Crew is online.'}</p>}
    <div className="divide-y divide-border">
      {rows.map(agent => {
        const bridge = agent.agentbridge!
        return <div key={agent.key} className="flex items-center gap-2.5 py-2.5">
          <Globe aria-hidden="true" className={`h-3.5 w-3.5 shrink-0 ${bridge.public ? 'text-emerald-500' : 'text-muted-foreground'}`} />
          <div className="min-w-0 flex-1">
            <p className="flex min-w-0 items-center gap-1.5 text-[13px]"><b className="truncate">{agent.name}</b>
              {bridge.state && <span className={`shrink-0 rounded-full px-2 py-px text-[11px] ${bridge.state.includes('offline') ? 'bg-muted text-muted-foreground' : 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'}`}>{bridge.state}</span>}</p>
            <p className="truncate text-[12.5px] text-muted-foreground">{agent.purpose || 'No purpose yet. Other agents need one to know what to ask it.'}</p>
          </div>
          {bridge.needs_purpose
            ? <button type="button" className="shrink-0 text-[12.5px] text-primary hover:underline" onClick={() => askAgentPurpose(state, projectId, agent)}>Set purpose</button>
            : <Saving on={busy === agent.tab}><Switch aria-label={`${agent.name}: Public on AgentBridge`} checked={bridge.public} disabled={!bridge.can_change || busy !== ''} onCheckedChange={on => void setPublic(agent.tab, on)} /></Saving>}
        </div>
      })}
      <div className="flex flex-wrap items-center gap-x-2.5 gap-y-2 py-2.5 text-sm">
        <div className="min-w-0 flex-1"><p className="font-semibold">Claude Code, Codex, Cursor</p><p className="text-[12.5px] text-muted-foreground">One line to paste in your coding app</p></div>
        <div className="flex flex-wrap items-center gap-1.5"><CopySetupLine crewName={crewName} /></div>
      </div>
    </div>
    {error && <p role="alert" className="text-xs text-amber-600">{error}</p>}
  </section>
}

/** One line your coding app (Claude Code, Codex, Cursor) pastes to reach this Crew's agents through AgentWorks. */
function CopySetupLine({ crewName }: { crewName: string }) {
  const [copied, setCopied] = useState(false)
  const [fallback, setFallback] = useState('')
  const line = `Connect to AgentWorks, then ask the ${JSON.stringify(crewName)} Crew's agents to help me.`
  const copy = async () => {
    try { await navigator.clipboard.writeText(line); setCopied(true); window.setTimeout(() => setCopied(false), 1500) }
    catch { setFallback(line) }
  }
  return <>
    <Button className="h-7 rounded-full px-3" size="sm" variant="outline" onClick={() => void copy()}>{copied ? 'Copied' : 'Copy line'}</Button>
    {fallback && <p className="w-full text-xs text-muted-foreground">Copy this into your coding app: {fallback}</p>}
  </>
}
