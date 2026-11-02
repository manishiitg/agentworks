import { useEffect, useState } from 'react'
import { ArrowRight, MessagesSquare, Terminal } from 'lucide-react'
import { bullpenApi, type BullpenAgent } from '../services/bullpenApi'
import { captureChatIdentity, isChatIdentityCurrent } from '../utils/chatIdentity'
import { openAppProduct } from '../utils/appNavigation'

const icons: Record<string, string> = { claude: 'claude.ico', codex: 'codex.png', cursor: 'cursor.svg', muse: 'muse.ico', pi: 'pi.svg', agy: 'antigravity.svg' }

/** Uses the existing authorized directory; other people's shared agents aren't your connections. */
export function HomeConnectedAgents() {
  const [agents, setAgents] = useState<BullpenAgent[] | null>(null)
  useEffect(() => {
    const controller = new AbortController()
    const identity = captureChatIdentity()
    const load = () => {
      if (document.hidden) return
      void bullpenApi.agents(controller.signal).then(items => {
        if (!controller.signal.aborted && isChatIdentityCurrent(identity)) setAgents(items.filter(agent => agent.online && !agent.shared))
      }).catch(() => { /* Keep the last known list; the directory shows connection errors. */ })
    }
    load()
    window.addEventListener('focus', load)
    const timer = window.setInterval(load, 15_000)
    return () => { controller.abort(); window.clearInterval(timer); window.removeEventListener('focus', load) }
  }, [])
  const empty = agents?.length === 0
  return <button type="button" aria-label={empty ? 'Connect your AI app' : agents ? `Connected chats · ${agents.length}` : 'Connected chats'} onClick={() => openAppProduct({ surface: 'bullpen', view: empty ? 'setup' : undefined })}
    className="flex w-full items-center gap-3 rounded-xl border border-border px-4 py-3 text-left text-sm hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
    <span aria-hidden="true" className="flex shrink-0 items-center -space-x-2">
      {agents?.length ? agents.slice(0, 4).map(agent => <span key={agent.agent_id} title={agent.label || agent.agent_name} className="grid h-7 w-7 place-items-center rounded-full bg-background ring-2 ring-background">
        {icons[agent.provider || ''] ? <img src={`/provider-icons/${icons[agent.provider || '']}`} alt="" className="h-4 w-4 object-contain" /> : <Terminal className="h-4 w-4 text-muted-foreground" />}
      </span>) : <MessagesSquare className="h-4 w-4 text-muted-foreground" />}
    </span>
    <span className="min-w-0 flex-1 font-medium">{empty ? 'Connect your AI app' : 'Connected chats'}{agents && !empty && <span className="ml-2 font-normal text-muted-foreground">· {agents.length}</span>}</span>
    <ArrowRight className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
  </button>
}
