import { useEffect, useState } from 'react'
import { Cable, Terminal } from 'lucide-react'
import { bullpenApi, type BullpenAgent } from '../../services/bullpenApi'
import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'
import { WorkspaceContextRow as Row } from './WorkspaceContextCard'

const BRIDGE_PROVIDERS: Record<string, string> = { codex: 'Codex', claude: 'Claude Code', cursor: 'Cursor', muse: 'Muse', pi: 'Pi', opencode: 'OpenCode', agy: 'agy' }

/** AgentBridge, with the agents of yours that are connected right now (Claude Code, Codex and the like on your computer). */
export function WorkspaceAgentBridgeRows() {
  const [agents, setAgents] = useState<BullpenAgent[] | null>(null)
  useEffect(() => {
    let alive = true
    const load = () => { void bullpenApi.agents().then(list => { if (alive) setAgents(list.filter(agent => agent.online && !agent.shared).sort((a, b) => a.agent_name.localeCompare(b.agent_name))) }).catch(() => { if (alive) setAgents(null) }) }
    load()
    const timer = window.setInterval(() => { if (!document.hidden) load() }, 15_000)
    return () => { alive = false; window.clearInterval(timer) }
  }, [])
  if (!agents) return null
  const open = () => openProductWorkspace('bullpen')
  return <>
    <Row icon={Cable} detail={agents.length ? `${agents.length} online` : 'None online'} onClick={open}>AgentBridge</Row>
    {agents.slice(0, 5).map(agent => <Row key={agent.agent_id} icon={Terminal} hint={agent.purpose || agent.agent_name} detail={BRIDGE_PROVIDERS[agent.provider || ''] || agent.provider || 'CLI agent'} onClick={open}>{agent.agent_name}</Row>)}
  </>
}
