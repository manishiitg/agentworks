import { useEffect, useState } from 'react'
import { ChevronDown, Terminal } from 'lucide-react'
import { bullpenApi, type BullpenAgent } from '../../services/bullpenApi'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'

const PROVIDERS: Record<string, string> = {
  codex: 'Codex',
  claude: 'Claude Code',
  cursor: 'Cursor',
  muse: 'Muse',
  pi: 'Pi',
  opencode: 'OpenCode',
  agy: 'agy',
}

/** Connection presence from AgentBridge; this is not a running-task indicator. */
export default function ConnectedAgentsSidebar() {
  const userId = useAuthStore((state) => state.user?.id)
  const workspace = useWorkspaceConnectionStore((state) => state.activeWorkspaceId)
  const scope = `${workspace}:${userId || ''}`
  const [snapshot, setSnapshot] = useState<{
    scope: string
    agents: BullpenAgent[]
    error: boolean
  } | null>(null)
  const [collapsed, setCollapsed] = useState(false)
  const [attempt, setAttempt] = useState(0)
  useEffect(() => {
    if (!userId) return
    const identity = captureChatIdentity()
    const controller = new AbortController()
    let pending = false
    const refresh = async () => {
      if (pending || controller.signal.aborted) return
      pending = true
      try {
        const agents = await bullpenApi.agents(controller.signal)
        if (!controller.signal.aborted && isChatIdentityCurrent(identity))
          setSnapshot({
            scope,
            agents: agents
              .filter((agent) => agent.online && !agent.shared)
              .sort((a, b) => a.agent_name.localeCompare(b.agent_name)),
            error: false,
          })
      } catch {
        if (!controller.signal.aborted && isChatIdentityCurrent(identity))
          setSnapshot({ scope, agents: [], error: true })
      } finally {
        pending = false
      }
    }
    void refresh()
    const timer = window.setInterval(() => {
      if (!document.hidden) void refresh()
    }, 15000)
    const onVisible = () => {
      if (!document.hidden) void refresh()
    }
    document.addEventListener('visibilitychange', onVisible)
    window.addEventListener('focus', onVisible)
    return () => {
      controller.abort()
      window.clearInterval(timer)
      document.removeEventListener('visibilitychange', onVisible)
      window.removeEventListener('focus', onVisible)
    }
  }, [scope, userId, attempt])
  const data = snapshot?.scope === scope ? snapshot : null
  if (!userId || !data || (!data.error && !data.agents.length)) return null
  return (
    <section aria-label="Connected CLI agents" className="mt-5">
      <button
        type="button"
        aria-expanded={!collapsed}
        aria-controls="sidebar-connected-agents"
        title="Your CLI agents connected to this server through AgentBridge"
        onClick={() => setCollapsed((value) => !value)}
        className="mb-1 flex w-full items-center gap-1 rounded-md px-2 py-1.5 text-xs text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        <ChevronDown className={`h-3 w-3 ${collapsed ? '-rotate-90' : ''}`} aria-hidden="true" />
        <span>Connected agents</span>
        {data && !data.error && (
          <span className="ml-auto text-[10px] tabular-nums">{data.agents.length}</span>
        )}
      </button>
      <div id="sidebar-connected-agents" hidden={collapsed}>
        {!data ? (
          <p className="px-3 py-2 text-xs text-muted-foreground">Loading connections…</p>
        ) : data.error ? (
          <button
            type="button"
            className="px-3 py-2 text-xs text-muted-foreground hover:text-foreground"
            onClick={() => setAttempt((value) => value + 1)}
          >
            Connections unavailable · Retry
          </button>
        ) : !data.agents.length ? (
          <p className="px-3 py-2 text-xs text-muted-foreground">No agents connected</p>
        ) : (
          <ul>
            {data.agents.slice(0, 5).map((agent) => (
              <li
                key={agent.agent_id}
                title={agent.purpose || agent.agent_name}
                className="flex items-center gap-2.5 px-2.5 py-2"
              >
                <Terminal
                  className="h-3.5 w-3.5 shrink-0 text-muted-foreground"
                  aria-hidden="true"
                />
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-[13px] leading-4">{agent.agent_name}</span>
                  <span className="mt-1 block text-[10px] leading-3 text-muted-foreground">
                    {PROVIDERS[agent.provider || ''] || agent.provider || 'CLI agent'}
                  </span>
                </span>
                <span
                  role="img"
                  aria-label="Connected"
                  title="Connected to AgentBridge"
                  className="h-1.5 w-1.5 shrink-0 rounded-full bg-emerald-500"
                />
              </li>
            ))}
          </ul>
        )}
        <button
          type="button"
          onClick={() => openProductWorkspace('bullpen')}
          className="ml-7 mt-1 rounded px-1 py-1.5 text-xs text-muted-foreground hover:text-foreground"
        >
          Open AgentBridge{data && data.agents.length > 5 ? ` · ${data.agents.length} agents` : ''}
        </button>
      </div>
    </section>
  )
}
