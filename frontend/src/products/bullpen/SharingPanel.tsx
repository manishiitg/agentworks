import { useEffect, useState } from 'react'
import { Ban, RefreshCw, Share2 } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { bullpenApi, bullpenError, sharingError, type BullpenAgent, type BullpenSharing } from '../../services/bullpenApi'

const stateLabel = { active: 'Active', revoked: 'Revoked', expired: 'Expired' } as const

function when(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '' : date.toLocaleString()
}

export function SharingPanel() {
  const [data, setData] = useState<BullpenSharing | null>(null)
  const [agents, setAgents] = useState<BullpenAgent[]>([])
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)
  const [attempt, setAttempt] = useState(0)
  const [agentName, setAgentName] = useState('')
  const [person, setPerson] = useState('')
  const [days, setDays] = useState(30)
  const [blockName, setBlockName] = useState('')

  useEffect(() => {
    const controller = new AbortController()
    setError('')
    Promise.all([bullpenApi.sharing(controller.signal), bullpenApi.agents(controller.signal)]).then(([sharing, own]) => {
      if (controller.signal.aborted) return
      setData(sharing)
      setAgents(own)
      setAgentName(current => current || own[0]?.agent_name || '')
    }).catch(reason => { if (!controller.signal.aborted) setError(bullpenError(reason)) })
    return () => controller.abort()
  }, [attempt])

  async function change(action: () => Promise<void>, done: string) {
    setBusy(true); setNotice(''); setError('')
    try { await action(); setNotice(done); setAttempt(value => value + 1) }
    catch (reason) { setError(sharingError(reason)) }
    finally { setBusy(false) }
  }

  return <section aria-label="Sharing" className="space-y-6">
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div><h2 className="text-lg font-semibold">Sharing</h2><p className="mt-1 text-sm text-muted-foreground">Let one person’s agents send messages to one of your agents. Sharing is chat only: it never lets them run anything for you. You can revoke or block at any time.</p></div>
      <Button variant="outline" size="sm" disabled={busy} onClick={() => setAttempt(value => value + 1)}><RefreshCw className="mr-2 h-3.5 w-3.5" />Refresh</Button>
    </div>
    {error && <p role="alert" className="rounded-lg border border-destructive/30 p-4 text-sm text-destructive">{error}</p>}
    {notice && <p role="status" className="text-sm text-muted-foreground">{notice}</p>}

    <form className="grid gap-3 rounded-xl border border-border p-4 sm:grid-cols-4" onSubmit={event => { event.preventDefault(); void change(() => bullpenApi.share(agentName, person.trim(), days * 24), 'Shared ' + agentName + ' with ' + person.trim() + '.').then(() => setPerson('')) }}>
      <label className="text-sm">Your agent
        <select value={agentName} onChange={event => setAgentName(event.target.value)} className="mt-1 w-full rounded-md border border-border bg-background px-2 py-2 text-sm">
          {agents.map(agent => <option key={agent.agent_id} value={agent.agent_name}>{agent.agent_name}</option>)}
        </select>
      </label>
      <label className="text-sm">Person’s username
        <input value={person} onChange={event => setPerson(event.target.value)} required className="mt-1 w-full rounded-md border border-border bg-background px-2 py-2 text-sm" />
      </label>
      <label className="text-sm">Days
        <input type="number" min={1} max={365} value={days} onChange={event => setDays(Math.max(1, Math.min(365, Number(event.target.value) || 1)))} className="mt-1 w-full rounded-md border border-border bg-background px-2 py-2 text-sm" />
      </label>
      <div className="flex items-end"><Button type="submit" disabled={busy || !agentName || !person.trim()}><Share2 className="mr-2 h-4 w-4" />Share</Button></div>
    </form>

    <div className="space-y-2">
      <h3 className="font-medium">Shared by you</h3>
      {!data?.shares.length ? <p className="text-sm text-muted-foreground">You have not shared any agent.</p> : <ul className="divide-y divide-border rounded-xl border border-border">
        {data.shares.map(share => <li key={share.share_id} className="flex flex-wrap items-center justify-between gap-3 p-3 text-sm">
          <span className="min-w-0 break-all"><strong>{share.agent_name}</strong> with <strong>{share.person}</strong> · {stateLabel[share.state]}{share.state === 'active' && ' until ' + when(share.expires_at)}</span>
          {share.state === 'active' && <Button variant="outline" size="sm" disabled={busy} aria-label={'Revoke ' + share.agent_name + ' for ' + share.person} onClick={() => void change(() => bullpenApi.revoke(share.share_id), 'Revoked. Their pending messages will not be delivered.')}>Revoke</Button>}
        </li>)}
      </ul>}
    </div>

    <div className="space-y-2">
      <h3 className="font-medium">Who messaged your agents</h3>
      {!data?.incoming.length ? <p className="text-sm text-muted-foreground">No one has messaged your agents through sharing.</p> : <ul className="divide-y divide-border rounded-xl border border-border">
        {data.incoming.map(item => <li key={item.person + item.sender_agent + item.agent_name} className="flex flex-wrap items-center justify-between gap-3 p-3 text-sm">
          <span className="min-w-0 break-all"><strong>{item.person}/{item.sender_agent}</strong> → {item.agent_name} · {item.messages} message{item.messages === 1 ? '' : 's'}, last {when(item.last_at)}</span>
          <Button variant="outline" size="sm" disabled={busy} aria-label={'Block ' + item.person} onClick={() => void change(() => bullpenApi.block(item.person), 'Blocked ' + item.person + ' and revoked everything shared with them.')}><Ban className="mr-2 h-3.5 w-3.5" />Block</Button>
        </li>)}
      </ul>}
    </div>

    <div className="space-y-2">
      <h3 className="font-medium">Blocked people</h3>
      <form className="flex flex-wrap gap-2" onSubmit={event => { event.preventDefault(); void change(() => bullpenApi.block(blockName.trim()), 'Blocked ' + blockName.trim() + '.').then(() => setBlockName('')) }}>
        <input aria-label="Username to block" placeholder="Username" value={blockName} onChange={event => setBlockName(event.target.value)} className="rounded-md border border-border bg-background px-2 py-2 text-sm" />
        <Button type="submit" variant="outline" size="sm" disabled={busy || !blockName.trim()}>Block</Button>
      </form>
      {data?.blocks.length ? <ul className="divide-y divide-border rounded-xl border border-border">
        {data.blocks.map(block => <li key={block.person} className="flex items-center justify-between gap-3 p-3 text-sm"><span>{block.person}</span><Button variant="outline" size="sm" disabled={busy} onClick={() => void change(() => bullpenApi.block(block.person, true), 'Unblocked ' + block.person + '.')}>Unblock</Button></li>)}
      </ul> : <p className="text-sm text-muted-foreground">No one is blocked.</p>}
    </div>

    <div className="space-y-2">
      <h3 className="font-medium">Shared with you</h3>
      {!data?.shared_with_me.length ? <p className="text-sm text-muted-foreground">No one has shared an agent with you.</p> : <ul className="divide-y divide-border rounded-xl border border-border">
        {data.shared_with_me.map(agent => <li key={agent.target} className="p-3 text-sm"><strong className="break-all">{agent.agent_name}</strong> · {agent.online ? 'Connected' : 'Offline'}<p className="mt-1 break-words text-muted-foreground">{agent.purpose || 'No purpose set.'}</p><p className="mt-1 font-mono text-xs">{agent.target}</p></li>)}
      </ul>}
    </div>
  </section>
}
