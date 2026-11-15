import { useEffect, useRef, useState } from 'react'
import { Terminal } from 'lucide-react'
import { crewTerminalTarget, teamRequest, type TeamTarget } from './teamApi'
import { TerminalLiveDialog } from './TerminalLiveDialog'
import { useAuthStore } from '../../stores/useAuthStore'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { sidebarAgentListClass, sidebarAgentRowClass } from '../../components/workspace/sidebarAgentLayout'

export type OwnTerminalAgent = { id: string; name: string; provider: string; provider_name: string; address: string; online: boolean; presence: string }

/** Same owner-only server rows on Mac/web; never insert a native session into
 * the writable app-chat store. Unmounting or a failed read drops private data. */
export function OwnTerminalAgents({ projectId, connection = getRawActiveWorkspaceId(), nested = false }: { projectId: string; connection?: string; nested?: boolean }) {
  const [state, setState] = useState<{ key: string; target: TeamTarget; agents: OwnTerminalAgent[] }>()
  const [selected, setSelected] = useState<string>()
  const refresh = useRef(() => {})
  const account = useAuthStore(state => state.user?.id)
  const key = JSON.stringify([account, connection, projectId])
  useEffect(() => {
    let active = true, reading = false
    const identity = captureChatIdentity()
    const read = async () => {
      if (reading || !active || document.hidden) return
      reading = true
      try {
        const target = await crewTerminalTarget(projectId, connection)
        const data = await teamRequest<{ agents: OwnTerminalAgent[] }>(target, '/terminal-agents')
        if (active && isChatIdentityCurrent(identity)) setState({ key, target, agents: data.agents })
      } catch { if (active) { setState(undefined); setSelected(undefined) } }
      finally { reading = false }
    }
    refresh.current = () => { void read() }
    void read()
    return () => { active = false; refresh.current = () => {} }
  }, [key, projectId, connection])
  useLiveRefetch(() => refresh.current(), { kinds: ['sessions', 'crew_panel'], project: projectId, scopeKey: key, fallbackMs: 60_000, safetyMs: 0, minIntervalMs: 15_000, refreshOnFocus: true })
  const current = state?.key === key ? state : undefined
  const agent = current?.agents.find(item => item.id === selected)
  if (!current?.agents.length) return null
  return <div className={nested ? '' : sidebarAgentListClass} role="group" aria-label="Your terminal agents">
    {current.agents.map(item => <button key={item.id} type="button" role="listitem" title={`${item.name} · ${item.provider_name} · ${item.presence}`} onClick={() => setSelected(item.id)} className={`${sidebarAgentRowClass} gap-1.5 text-muted-foreground hover:bg-muted hover:text-foreground`}>
      <Terminal aria-hidden="true" className="h-3.5 w-3.5 shrink-0" /><span className="min-w-0 flex-1 truncate">{item.name}</span>
      <img src={`/provider-icons/${item.provider === 'codex-cli' ? 'codex.png' : 'claude.ico'}`} alt={item.provider_name} title={item.provider_name} className="h-3.5 w-3.5 shrink-0 object-contain" />
      <span className="shrink-0 text-[11px]">{item.presence}</span>
    </button>)}
    {agent && <TerminalLiveDialog target={current.target} agent={agent} owner close={() => setSelected(undefined)} />}
  </div>
}
