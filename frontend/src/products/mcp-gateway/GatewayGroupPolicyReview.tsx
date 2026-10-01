import { useEffect, useRef, useState } from 'react'
import { CheckCircle2, ShieldAlert, ShieldCheck } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { Textarea } from '../../components/ui/Textarea'
import { ConsoleError } from './gatewayConsoleShared'
import { SettingsCard, SettingsEmpty } from '../../components/ui/SettingsCard'
import { listAccessHistory, listAccessPackages, publishAccessPackage, revokeAccessPackage, simulateAccessPackage,
  type GatewayAccessPackage, type GatewayPolicyEvent } from './gatewayAdminApi'

export function GatewayGroupPolicyReview({ base, groupId, revision, chatBusy, onChanged }: {
  base: string; groupId: string; revision?: string; chatBusy: boolean; onChanged: () => void
}) {
  const [busy, setBusy] = useState(false)
  const [packages, setPackages] = useState<GatewayAccessPackage[]>([])
  const [history, setHistory] = useState<GatewayPolicyEvent[]>([])
  const [selectedKey, setSelectedKey] = useState('')
  const [error, setError] = useState('')
  const [sample, setSample] = useState('{}')
  const [sampleTool, setSampleTool] = useState('')
  const [simulation, setSimulation] = useState<string | null>(null)
  const simulationRequest = useRef(0)
  const selected = packages.find(p => `${p.id}:${p.status}` === selectedKey)
  function applySnapshot(saved: { packages: GatewayAccessPackage[] }, changes: { events: GatewayPolicyEvent[] }) {
    const groupPolicies = saved.packages.filter(policy => policy.group_id === groupId)
    const ids = new Set(groupPolicies.map(policy => policy.id))
    setPackages(groupPolicies)
    setHistory((changes.events ?? []).filter(event => ids.has(event.package_id)))
    setSelectedKey(previous => {
      const choice = groupPolicies.find(policy => `${policy.id}:${policy.status}` === previous)
        ?? groupPolicies.find(policy => policy.id === previous.split(':')[0])
        ?? groupPolicies.find(policy => policy.status === 'draft') ?? groupPolicies[0]
      return choice ? `${choice.id}:${choice.status}` : ''
    })
    setError('')
  }
  async function refresh() {
    const [saved, changes] = await Promise.all([listAccessPackages(base), listAccessHistory(base)])
    applySnapshot(saved, changes)
  }
  useEffect(() => {
    let cancelled = false
    Promise.all([listAccessPackages(base), listAccessHistory(base)]).then(([saved, changes]) => {
      if (!cancelled) applySnapshot(saved, changes)
    }).catch(err => { if (!cancelled) setError(err instanceof Error ? err.message : 'Could not load group permissions') })
    return () => { cancelled = true; simulationRequest.current += 1 }
  }, [base, groupId, revision])
  useEffect(() => { setSampleTool('') }, [selectedKey, selected?.version])
  useEffect(() => { simulationRequest.current += 1; setSimulation(null) }, [selectedKey, selected?.version, sampleTool, sample])
  async function publish() {
    if (!selected || selected.status !== 'draft') return
    setError('')
    setBusy(true)
    try {
      await publishAccessPackage(base, selected.id, selected.version)
      await refresh()
      onChanged()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Publish failed')
    } finally {
      setBusy(false)
    }
  }

  async function revoke() {
    if (!selected || selected.status !== 'published') return
    setError('')
    setBusy(true)
    try {
      await revokeAccessPackage(base, selected.id)
      await refresh()
      onChanged()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Revoke failed')
    } finally {
      setBusy(false)
    }
  }

  async function simulate() {
    if (!selected || selected.status !== 'draft' || !sampleTool) return
    setError('')
    const request = ++simulationRequest.current
    try {
      const parsed: unknown = JSON.parse(sample)
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error('Arguments must be a JSON object')
      const result = await simulateAccessPackage(base, selected.id, sampleTool, parsed as Record<string, unknown>)
      if (request === simulationRequest.current) setSimulation(result.allowed ? 'Allowed by this draft' : 'Denied by this draft')
    } catch (err) {
      if (request === simulationRequest.current) setError(err instanceof Error ? err.message : 'Simulation failed')
    }
  }

  if (packages.length === 0 && !error) return null
  return <>
    {error && <ConsoleError message={error} onRetry={() => void refresh().then(() => setError('')).catch(err => setError(err instanceof Error ? err.message : 'Refresh failed'))} />}
<SettingsCard className="rounded-none border-0 border-t pt-4 px-0 pb-0" icon={<ShieldCheck className="h-4 w-4 text-primary" />} title="Permission changes" count={`${packages.length} ${packages.length === 1 ? 'policy' : 'policies'}`} description="Review changes before publishing." ariaLabel="Group permission review">
        {packages.length === 0 ? <SettingsEmpty>Ask the assistant to configure access for this group. Review draft changes here before publishing.</SettingsEmpty> : (
          <select aria-label="Group permission policy" value={selectedKey} onChange={event => { setSelectedKey(event.target.value); setSimulation(null) }} className="mb-4 w-full rounded-md border border-input bg-background p-2 text-sm text-foreground">
            {packages.map(item => <option key={`${item.id}:${item.status}`} value={`${item.id}:${item.status}`}>{item.name} · {item.status}</option>)}
          </select>
        )}
        {selected && <div className="space-y-4 text-sm">
          <div className="flex items-center gap-2 text-foreground">{selected.status === 'published' ? <CheckCircle2 className="h-4 w-4 text-emerald-500" /> : <ShieldAlert className="h-4 w-4 text-amber-500" />}<span className="capitalize">{selected.status}</span><span className="text-muted-foreground">v{selected.version}</span></div>
          <div className="space-y-3">
            {selected.rules.map(rule => <div key={rule.public_name} className="rounded-md border border-border p-3">
              <div className="break-all font-medium text-foreground">{rule.public_name}</div>
              {rule.conditions.length === 0 ? <p className="mt-1 text-amber-600">All arguments permitted</p> : rule.conditions.map((condition, i) => <p key={i} className="mt-1 break-all font-mono text-xs text-muted-foreground">{condition.path} {condition.op} {condition.value}</p>)}
            </div>)}
          </div>
          {selected.status === 'draft' && <>
            <div className="space-y-2 border-t border-border pt-4">
              <label htmlFor="group-sim-tool" className="font-medium text-foreground">Test draft</label>
              <select id="group-sim-tool" value={sampleTool} onChange={event => setSampleTool(event.target.value)} className="w-full rounded-md border border-input bg-background p-2 text-xs text-foreground">
                <option value="">Select a tool</option>
                {selected.rules.map(rule => <option key={rule.public_name} value={rule.public_name}>{rule.public_name}</option>)}
              </select>
              <Textarea aria-label="Sample tool arguments JSON" value={sample} onChange={event => setSample(event.target.value)} rows={3} className="w-full rounded-md border border-input bg-background p-2 font-mono text-xs text-foreground" />
              <Button size="sm" variant="outline" type="button" onClick={() => void simulate()} disabled={!sampleTool || busy || chatBusy}>Simulate</Button>
              {simulation && <p role="status" className="text-muted-foreground">{simulation}</p>}
            </div>
            <Button size="sm" type="button" onClick={() => void publish()} disabled={busy || chatBusy} className="w-full">Publish permissions</Button>
          </>}
          {selected.status === 'published' && <Button size="sm" type="button" variant="outline" onClick={() => void revoke()} disabled={busy || chatBusy} className="w-full border-destructive text-destructive">Revoke permissions</Button>}
        </div>}
        {history.length > 0 && <div className="mt-6 border-t border-border pt-4">
          <h3 className="mb-2 text-sm font-semibold text-foreground">Recent changes</h3>
          <ol className="space-y-2 text-xs text-muted-foreground">
            {history.slice(-5).reverse().map((event, index) => <li key={`${event.package_id}-${event.version}-${index}`} className="break-all">{event.action.replace('_', ' ')} · {event.package_id} v{event.version} · {event.actor}</li>)}
          </ol>
        </div>}
        </SettingsCard>
  </>
}
