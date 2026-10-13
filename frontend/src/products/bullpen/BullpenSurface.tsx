import { currentProductRoute } from '../../utils/productRoutes'
import { useProductUrlView } from '../../hooks/useProductUrlView'
import { AgentUsage, BullpenUsageOverview } from './BullpenUsage'
import { lazy, Suspense, useEffect, useState } from 'react'
import { BookOpen, Check, Copy, MessagesSquare, RefreshCw, Search, Terminal, UsersRound } from 'lucide-react'
import { ModePresetBar } from '../../components/ModePresetBar'
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
import { bullpenApi, bullpenError, type BullpenAgent, type BullpenNotice, type BullpenGroup } from '../../services/bullpenApi'

const AdminPages = lazy(() => import('../../components/AdminPages'))
const views = [
  { id: 'agents' as const, label: 'Agents', icon: UsersRound },
  { id: 'setup' as const, label: 'Connect an agent', icon: Terminal },
]
const providerNames: Record<string, string> = { codex: 'Codex', claude: 'Claude Code', muse: 'Muse', pi: 'Pi', cursor: 'Cursor', agy: 'agy' }

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

function AgentDirectory({ onConnect, isAdmin }: { onConnect: () => void; isAdmin: boolean }) {
  const [agents, setAgents] = useState<BullpenAgent[]>([])
  const [loading, setLoading] = useState(true)
  const [usageLoading, setUsageLoading] = useState(true)
  const [error, setError] = useState('')
  const [query, setQuery] = useState('')
  const [attempt, setAttempt] = useState(0)
  const [updated, setUpdated] = useState<Date | null>(null)
  const [notices, setNotices] = useState<BullpenNotice[]>([])
  const [groups, setGroups] = useState<BullpenGroup[]>([])
  useEffect(() => {
    const controller = new AbortController()
    bullpenApi.notices(controller.signal).then(items => { if (!controller.signal.aborted) setNotices(items) }).catch(() => undefined)
    bullpenApi.groups(controller.signal).then(data => { if (!controller.signal.aborted) setGroups(data) }).catch(() => undefined)
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
  const visible = agents.filter(agent => [agent.agent_name, agent.purpose, agent.provider].some(value => value?.toLowerCase().includes(search)))
    .sort((a, b) => Number(b.online) - Number(a.online) || a.agent_name.localeCompare(b.agent_name))
  return <section aria-label="Your agents" className="space-y-5">
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div><h2 className="text-lg font-semibold">Your agents</h2><p className="mt-1 text-sm text-muted-foreground">Names registered to your account on this server.</p></div>
      <div className="flex gap-2"><Button variant="outline" size="sm" disabled={loading} onClick={() => setAttempt(value => value + 1)}><RefreshCw className="mr-2 h-3.5 w-3.5" />Refresh agents</Button><Button size="sm" onClick={onConnect}>Connect an agent</Button></div>
    </div>
    <HowBullpenWorks collapsible />
    {notices.map(item => <div key={item.notice_id} role="status" className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-border bg-muted/40 p-3 text-sm"><span className="min-w-0 break-words">{item.text}</span><Button variant="outline" size="sm" onClick={() => void bullpenApi.dismissNotice(item.notice_id).then(() => setNotices(list => list.filter(entry => entry.notice_id !== item.notice_id))).catch(() => undefined)}>Dismiss</Button></div>)}
    <div className="relative"><Search aria-hidden="true" className="absolute left-3 top-3 h-4 w-4 text-muted-foreground" /><input aria-label="Find an agent" value={query} onChange={event => setQuery(event.target.value)} placeholder="Search names, providers and purpose" className="w-full rounded-lg border border-border bg-background py-2.5 pl-10 pr-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring" /></div>
    {error ? <p role="alert" className="rounded-lg border border-destructive/30 p-4 text-sm text-destructive">{error}</p> : loading && !agents.length ? <p role="status" className="py-12 text-center text-sm text-muted-foreground">Loading your agents…</p> : !agents.length ? <div className="rounded-xl border border-dashed border-border p-8 text-center"><MessagesSquare className="mx-auto mb-3 h-8 w-8 text-muted-foreground" /><h3 className="font-medium">Bring your first agent into Bullpen</h3><p className="mx-auto mt-2 max-w-md text-sm text-muted-foreground">Connect a coding chat so your other agents can find it, exchange messages and reply.</p><Button className="mt-5" onClick={onConnect}>Set up messaging</Button></div> : !visible.length ? <p className="py-8 text-center text-sm text-muted-foreground">No agents match your search.</p> : <div className="grid gap-3 md:grid-cols-2">
      {visible.map(agent => <article key={agent.agent_id} className="min-w-0 space-y-3 rounded-xl border border-border p-4">
        <div className="flex items-start justify-between gap-3"><div className="min-w-0"><h3 className="line-clamp-2 break-words font-semibold" title={agent.purpose || agent.agent_name}>{agent.purpose || agent.agent_name}</h3><p className="mt-1 truncate font-mono text-xs text-muted-foreground" title={agent.agent_name}>{agent.agent_name} · {providerNames[agent.provider || ''] || agent.provider || 'Registered mailbox'}</p></div><span className="flex shrink-0 flex-col items-end gap-1"><span className="rounded-full bg-muted px-2 py-1 text-xs">{agent.online ? 'Connected' : 'Offline'}</span>{(agent.groups ?? []).length > 0 && <span className="rounded-full border border-border px-2 py-1 text-xs" title={(agent.groups ?? []).join(', ')}>In {(agent.groups ?? []).length === 1 ? 'group ' + (agent.groups ?? [])[0] : (agent.groups ?? []).length + ' groups'}</span>}</span></div>
        {agent.purpose && agent.purpose.length > 120 && <details className="text-sm"><summary className="cursor-pointer text-primary">Full purpose</summary><p className="mt-2 break-words text-muted-foreground">{agent.purpose}</p></details>}
        <AgentAddress target={agent.target} />
        {agent.provider === 'cursor' && <details className="text-xs text-muted-foreground"><summary className="cursor-pointer" title="Cursor picks up new messages when you send your next prompt or when its current turn ends. An idle Cursor chat is not woken; messages wait safely.">Gets messages when its chat is used</summary><p className="mt-1">Cursor picks up new messages when you send your next prompt or when its current turn ends. An idle Cursor chat is not woken; messages wait safely.</p></details>}
        <AgentUsage usage={agent.usage} unavailable={agent.usage_unavailable} provider={agent.provider} />
      </article>)}
    </div>}
    {!error && (usageLoading ? <p role="status" className="text-sm text-muted-foreground">Loading optional usage…</p> : <BullpenUsageOverview agents={agents} onChange={() => setAttempt(value => value + 1)} />)}
    <div className="space-y-2">
      <h3 className="font-medium">Groups on this server</h3>
      {!groups.length ? <p className="text-sm text-muted-foreground">No groups yet. Ask an agent “share yourself in the docs group, for questions about the docs” to start one.</p> : <ul className="divide-y divide-border rounded-xl border border-border">
        {groups.map(group => <li key={group.name} className="p-3 text-sm"><strong className="break-all">group:{group.name}</strong> · {group.member_count} member{group.member_count === 1 ? '' : 's'}{(group.joined_with ?? []).length > 0 && ' · you: ' + (group.joined_with ?? []).join(', ')}<p className="mt-1 break-words text-muted-foreground">{group.purpose}</p>
          {(group.members ?? []).length > 0 && <p className="mt-1 break-words text-xs text-muted-foreground">{(group.members ?? []).map(member => member.agent_name + (member.online ? '' : ' (offline)')).join(' · ')}</p>}</li>)}
      </ul>}
    </div>
    <p className="text-xs leading-5 text-muted-foreground">Connected means the server has an active delivery stream. Whether a message starts a turn depends on the agent’s delivery adapter. Optional usage reporting is off unless you turn it on with agentworks usage on. Stored, admitted and processed are separate receipts.{updated && ' Last refreshed ' + updated.toLocaleTimeString() + '.'}</p>
    <div className="rounded-xl bg-muted/30 p-4 text-sm"><h3 className="font-medium">Start a conversation</h3><p className="mt-2 leading-6 text-muted-foreground">Copy an agent address, then ask your coding agent to send it a message through AgentWorks. To let other people’s agents message an agent, ask it to share itself into a group (and to leave the group to undo it). Replies stay in the same conversation. This page shows your directory; it does not expose private chat transcripts.</p></div>
    {isAdmin && <AdminPanel />}
  </section>
}

export function BullpenSurface() {
  const [view, setView] = usePersistentTab('bullpen:view', 'agents', ['agents', 'setup'] as const, currentProductRoute()?.surface === 'bullpen' ? (currentProductRoute()?.view === 'setup' ? 'setup' : 'agents') : undefined)
  useProductUrlView('bullpen', { view: view === 'setup' ? 'setup' : undefined })
  const adminPage = useAppStore(state => state.adminPage)
  const showProviders = useLLMStore(state => state.showLLMModal)
  const user = useAuthStore(state => state.user)
  useRegisterPanelSwitcher('bullpen', views, id => setView(id as 'agents' | 'setup'))
  return <div className="flex h-dvh min-h-0 min-w-0 bg-background text-foreground" data-testid="bullpen-surface">
    <ModePresetBar reduced walkthroughPaused />
    <main className="min-h-0 min-w-0 flex-1 overflow-y-auto">
      {showProviders ? <LlmModalHost /> : adminPage ? <Suspense fallback={<p className="p-6">Opening settings…</p>}><AdminPages /></Suspense> : <div className="w-full p-4 sm:p-6">
        <header className="mb-6 border-b border-border pb-5"><div className="flex items-center gap-3"><MessagesSquare className="h-6 w-6 text-primary" /><h1 className="text-2xl font-semibold tracking-tight">Bullpen</h1></div><p className="mt-2 text-sm text-muted-foreground">Your connected agents, in one place.</p>
          <nav aria-label="Bullpen views" className="mt-5 flex flex-wrap gap-2">{views.map(({ id, label, icon: Icon }) => <Button key={id} variant={view === id ? 'default' : 'ghost'} size="sm" aria-pressed={view === id} onClick={() => setView(id)}><Icon className="mr-2 h-4 w-4" />{label}</Button>)}<a href="https://github.com/manishiitg/agentworks/blob/main/docs/bullpen.md" target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-2 rounded-md px-3 py-2 text-sm font-medium text-muted-foreground hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><BookOpen className="h-4 w-4" aria-hidden="true" />Bullpen help<span className="sr-only"> (opens in a new tab)</span></a></nav>
        </header>
        {view === 'setup' ? <AgentMessagingSetupPanel onOpenMcp={() => useAppStore.getState().setAdminPage('mcp')} /> : user ? <AgentDirectory key={user.id} isAdmin={user.role === 'admin'} onConnect={() => setView('setup')} /> : <p>Sign in to see your agents.</p>}
      </div>}
    </main>
  </div>
}
