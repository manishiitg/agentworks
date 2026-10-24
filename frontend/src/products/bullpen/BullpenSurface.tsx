import { currentProductRoute } from '../../utils/productRoutes'
import { useProductUrlView } from '../../hooks/useProductUrlView'
import { AgentUsage, BullpenUsageOverview } from './BullpenUsage'
import { lazy, Suspense, useEffect, useState } from 'react'
import { BookOpen, Check, Copy, MessagesSquare, RefreshCw, Search, Terminal, UsersRound, ShieldCheck } from 'lucide-react'
import { ModePresetBar } from '../../components/ModePresetBar'
import { WorkspacePageHeader } from '../../components/workspace/WorkspacePageHeader'
import { WorkspacePageContent } from '../../components/workspace/WorkspacePageContent'
import LlmModalHost from '../../components/topbar/LlmModalHost'
import { AgentMessagingSetupPanel } from './AgentMessagingSetupPanel'
import { AdminPanel } from './AdminPanel'
import { HowBullpenWorks } from './HowBullpenWorks'
import { Button } from '../../components/ui/Button'
import { usePersistentTab } from '../../hooks/usePersistentTab'
import { useRegisterPanelSwitcher } from '../../stores/usePanelSwitcherStore'
import { useAppStore } from '../../stores/useAppStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { useLLMStore } from '../../stores/useLLMStore'
import { bullpenApi, bullpenError, type BullpenAgent, type BullpenNotice } from '../../services/bullpenApi'

const AdminPages = lazy(() => import('../../components/AdminPages'))
const views = [
  { id: 'agents' as const, label: 'Agents', icon: UsersRound },
  { id: 'setup' as const, label: 'Connect an agent', icon: Terminal },
]
const providerNames: Record<string, string> = { codex: 'Codex', claude: 'Claude Code', muse: 'Muse Code', pi: 'Pi', cursor: 'Cursor', agy: 'agy' }

function AgentAddress({ target }: { target: string }) {
  const [copied, setCopied] = useState(false)
  const [failed, setFailed] = useState(false)
  async function copy() {
    try { await navigator.clipboard.writeText(target); setCopied(true); setFailed(false) }
    catch { setFailed(true) }
  }
  return <div className="space-y-1">
    <Button variant="outline" size="sm" className="max-w-full" aria-label={'Copy address ' + target} onClick={() => void copy()}>
      {copied ? <Check className="mr-2 h-3.5 w-3.5 shrink-0" /> : <Copy className="mr-2 h-3.5 w-3.5 shrink-0" />}
      <span className="truncate font-mono text-xs">{target}</span>
    </Button>
    {(copied || failed) && <p role="status" className="text-xs text-muted-foreground">{failed ? 'Select the address and copy it manually.' : 'Address copied'}</p>}
  </div>
}

function AgentDirectory({ onConnect }: { onConnect: () => void }) {
  const [agents, setAgents] = useState<BullpenAgent[]>([])
  const [loading, setLoading] = useState(true)
  const [usageLoading, setUsageLoading] = useState(true)
  const [error, setError] = useState('')
  const [query, setQuery] = useState('')
  const [attempt, setAttempt] = useState(0)
  const [updated, setUpdated] = useState<Date | null>(null)
  const [notices, setNotices] = useState<BullpenNotice[]>([])
  useEffect(() => {
    const controller = new AbortController()
    bullpenApi.notices(controller.signal).then(items => { if (!controller.signal.aborted) setNotices(items) }).catch(() => undefined)
    return () => controller.abort()
  }, [attempt])
  useEffect(() => {
    const controller = new AbortController()
    setLoading(true); setUsageLoading(true); setError('')
    bullpenApi.agents(controller.signal).then(items => {
      if (controller.signal.aborted) return
      setAgents(items); setUpdated(new Date()); setLoading(false)
      // Usage is optional: never hold the directory behind its database request.
      void bullpenApi.agentUsage(controller.signal).then(readings => {
        if (controller.signal.aborted) return
        const byId = new Map(readings.map(reading => [reading.agent_id, reading]))
        setAgents(current => current.map(agent => agent.shared ? agent : { ...agent, ...byId.get(agent.agent_id) }))
      }).catch(() => {
        if (!controller.signal.aborted) setAgents(current => current.map(agent => agent.shared ? agent : { ...agent, usage_unavailable: true }))
      }).finally(() => { if (!controller.signal.aborted) setUsageLoading(false) })
    }).catch(reason => {
      if (!controller.signal.aborted) { setAgents([]); setUsageLoading(false); setError(bullpenError(reason)) }
    }).finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [attempt])
  const search = query.trim().toLowerCase()
  const visible = agents.filter(agent => [agent.label, agent.agent_name, agent.purpose, agent.provider].some(value => value?.toLowerCase().includes(search)))
    .sort((a, b) => Number(b.online) - Number(a.online) || a.agent_name.localeCompare(b.agent_name))
  return <section aria-label="Your agents" className="space-y-5">
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div><h2 className="text-lg font-semibold">Your agents</h2><p className="mt-1 text-sm text-muted-foreground">Your connected agents and published agents you can reach.</p></div>
      <div className="flex gap-2"><Button variant="outline" size="sm" disabled={loading} onClick={() => setAttempt(value => value + 1)}><RefreshCw className="mr-2 h-3.5 w-3.5" />Refresh agents</Button><Button size="sm" onClick={onConnect}>Connect an agent</Button></div>
    </div>
    {notices.length > 1 ? <details className="rounded-lg border border-border bg-muted/40 p-3 text-sm"><summary className="cursor-pointer">{notices.length} notices from an administrator · Show</summary><div className="mt-3 space-y-2">{notices.map(item => <div key={item.notice_id} role="status" className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-border bg-muted/40 p-3 text-sm"><span className="min-w-0 break-words">{item.text}</span><Button variant="outline" size="sm" onClick={() => void bullpenApi.dismissNotice(item.notice_id).then(() => setNotices(list => list.filter(entry => entry.notice_id !== item.notice_id))).catch(() => undefined)}>Dismiss</Button></div>)}<Button variant="outline" size="sm" onClick={() => void Promise.all(notices.map(item => bullpenApi.dismissNotice(item.notice_id))).then(() => setNotices([])).catch(() => undefined)}>Dismiss all</Button></div></details> : notices.map(item => <div key={item.notice_id} role="status" className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-border bg-muted/40 p-3 text-sm"><span className="min-w-0 break-words">{item.text}</span><Button variant="outline" size="sm" onClick={() => void bullpenApi.dismissNotice(item.notice_id).then(() => setNotices(list => list.filter(entry => entry.notice_id !== item.notice_id))).catch(() => undefined)}>Dismiss</Button></div>)}
    <div className="relative"><Search aria-hidden="true" className="absolute left-3 top-3 h-4 w-4 text-muted-foreground" /><input aria-label="Find an agent" value={query} onChange={event => setQuery(event.target.value)} placeholder="Search names, providers and purpose" className="w-full rounded-lg border border-border bg-background py-2.5 pl-10 pr-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring" /></div>
    {error ? <p role="alert" className="rounded-lg border border-destructive/30 p-4 text-sm text-destructive">{error}</p> : loading && !agents.length ? <p role="status" className="py-12 text-center text-sm text-muted-foreground">Loading your agents…</p> : !agents.length ? <div className="rounded-xl border border-dashed border-border p-8 text-center"><MessagesSquare className="mx-auto mb-3 h-8 w-8 text-muted-foreground" /><h3 className="font-medium">Bring your first agent into AgentBridge</h3><p className="mx-auto mt-2 max-w-md text-sm text-muted-foreground">Connect an AI app or coding chat to exchange messages with your other agents.</p><Button className="mt-5" onClick={onConnect}>Set up messaging</Button></div> : !visible.length ? <p className="py-8 text-center text-sm text-muted-foreground">No agents match your search.</p> : <div className="grid gap-3 md:grid-cols-2">
      {visible.map(agent => <article key={agent.agent_id} className="min-w-0 space-y-3 rounded-xl border border-border p-4">
        <div className="flex items-start justify-between gap-3"><div className="min-w-0"><h3 className="line-clamp-2 break-words font-semibold" title={agent.label || agent.agent_name}>{agent.label || agent.agent_name}</h3><p className="mt-1 truncate font-mono text-xs text-muted-foreground" title={agent.agent_name}>{agent.agent_name} · {agent.type || 'Agent'}{agent.provider ? ` · ${providerNames[agent.provider] || agent.provider}` : ''}</p></div><span className="flex shrink-0 flex-col items-end gap-1"><span className="rounded-full bg-muted px-2 py-1 text-xs">{agent.presence || 'Status unavailable'}</span>{agent.update_needed && <span className="rounded-full border border-border px-2 py-1 text-xs" title="The AgentWorks CLI on this agent's computer is older than the server's. Run agentworks update there.">CLI update available</span>}{agent.published && <span className="rounded-full border border-border px-2 py-1 text-xs" title="Everyone on this server can find and message this agent.">Published to network</span>}</span></div>
        {agent.purpose && agent.purpose.length <= 120 && <p className="break-words text-sm text-muted-foreground">{agent.purpose}</p>}
        {agent.purpose && agent.purpose.length > 120 && <details className="text-sm"><summary className="cursor-pointer text-primary">Full purpose</summary><p className="mt-2 break-words text-muted-foreground">{agent.purpose}</p></details>}
        <AgentAddress target={agent.target} />
        {(agent.repository || agent.activity) && <p className="text-xs text-muted-foreground">{agent.repository && <span>{agent.repository}{agent.branch ? `@${agent.branch}` : ''} · </span>}{agent.activity || 'Unknown'}{agent.activity_at && <span title={new Date(agent.activity_at).toLocaleString()}> (last reported {new Date(agent.activity_at).toLocaleTimeString()})</span>}</p>}
        <div className="space-y-1 text-xs text-muted-foreground">
          <div>Last seen: {agent.last_seen_at ? <time dateTime={agent.last_seen_at}>{new Date(agent.last_seen_at).toLocaleString()}</time> : 'Unknown'}</div>
          {agent.last_delivered_at && <div>Last message delivered: <time dateTime={agent.last_delivered_at}>{new Date(agent.last_delivered_at).toLocaleString()}</time></div>}
        </div>
        {agent.provider === 'cursor' && <details className="text-xs text-muted-foreground"><summary className="cursor-pointer" title="Cursor picks up new messages after a tool finishes or when its current turn ends. An idle Cursor chat is not woken; messages wait safely.">Gets messages when its chat is used</summary><p className="mt-1">Cursor picks up new messages after a tool finishes or when its current turn ends. An idle Cursor chat is not woken; messages wait safely.</p></details>}
        <AgentUsage usage={agent.usage} unavailable={agent.usage_unavailable} provider={agent.provider} />
      </article>)}
    </div>}
    <HowBullpenWorks collapsible />
    {!error && <details className="rounded-xl border border-border p-4"><summary className="cursor-pointer text-sm font-medium">Optional token usage</summary><div className="mt-4">{usageLoading ? <p role="status" className="text-sm text-muted-foreground">Loading optional usage…</p> : <BullpenUsageOverview agents={agents} onChange={() => setAttempt(value => value + 1)} />}</div></details>}
    <p className="text-xs text-muted-foreground">{updated && `Last refreshed ${updated.toLocaleTimeString()}. `}Connection status does not mean a message has been read.</p>
    <details className="rounded-xl border border-border p-4 text-sm"><summary className="cursor-pointer font-medium">How to start a conversation</summary><p className="mt-2 leading-6 text-muted-foreground">Copy an agent address, then ask your connected agent to send it a message through AgentWorks. Replies stay in the same conversation. This directory does not show private chat transcripts.</p></details>
  </section>
}

export function BullpenSurface() {
  const [view, setView] = usePersistentTab('bullpen:view', 'agents', ['agents', 'setup', 'admin'] as const, currentProductRoute()?.surface === 'bullpen' ? (currentProductRoute()?.view === 'setup' ? 'setup' : currentProductRoute()?.view === 'admin' ? 'admin' : 'agents') : undefined)
  useProductUrlView('bullpen', { view: view === 'agents' ? undefined : view })
  const adminPage = useAppStore(state => state.adminPage)
  const showProviders = useLLMStore(state => state.showLLMModal)
  const user = useAuthStore(state => state.user)
  useRegisterPanelSwitcher('bullpen', user?.role === 'admin' ? [...views, { id: 'admin', label: 'Administration' }] : views, id => setView(id as 'agents' | 'setup' | 'admin'))
  return <div className="flex h-dvh min-h-0 min-w-0 bg-background text-foreground" data-testid="bullpen-surface">
    <ModePresetBar reduced walkthroughPaused />
    <main className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden">
      {showProviders ? <LlmModalHost /> : adminPage ? <Suspense fallback={<p className="p-6">Opening settings…</p>}><AdminPages /></Suspense> : <div className="flex h-full min-h-0 w-full flex-col">
        <WorkspacePageHeader title={view === 'setup' ? 'Connect an agent' : view === 'admin' ? 'Administration' : 'AgentBridge'} icon={MessagesSquare} showBack={false} onProductHome={() => setView('agents')} productMenuItems={[
          ...views.map(item => ({ ...item, active: view === item.id, onSelect: () => setView(item.id) })),
          ...(user?.role === 'admin' ? [{ id: 'admin', label: 'Administration', icon: ShieldCheck, active: view === 'admin', onSelect: () => setView('admin') }] : []),
          { id: 'help', label: 'AgentBridge help', icon: BookOpen, href: 'https://github.com/manishiitg/agentworks/blob/main/docs/bullpen.md', external: true },
        ]} />
        <div className="min-h-0 flex-1 overflow-y-auto">
          <WorkspacePageContent className={view === 'setup' ? 'mx-auto w-full max-w-5xl' : undefined}>
            {view === 'setup' ? <AgentMessagingSetupPanel onOpenMcp={() => useAppStore.getState().setAdminPage('mcp')} onBack={() => setView('agents')} /> : view === 'admin' && user?.role === 'admin' ? <AdminPanel /> : user ? <AgentDirectory key={user.id} onConnect={() => setView('setup')} /> : <p>Sign in to see your agents.</p>}
          </WorkspacePageContent>

        </div>
      </div>}
    </main>
  </div>
}
