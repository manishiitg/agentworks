import { useState } from 'react'
import { ShieldCheck } from 'lucide-react'
import { SettingsCard } from '../../components/ui/SettingsCard'
import { Button } from '../../components/ui/Button'
import { Textarea } from '../../components/ui/Textarea'
import {
  approvePIIReview, deletePIIRule, listConnectors, listGroups, listPIIReviews, listPIIRules,
  listTools, savePIIRule, testPII, type GatewayPIIRule,
} from './gatewayAdminApi'
import { ConsoleEmpty, ConsoleError, ConsoleLoading, ConsoleStale } from './gatewayConsoleShared'
import { gatewayErrorMessage, useAttempt, useGatewayLoader } from './gatewayConsoleUtils'

const selectClass = 'h-8 rounded-md border border-input bg-background px-2 text-xs'
const emptyRule: Partial<GatewayPIIRule> = { DataType: 'email', Direction: 'both', Action: 'mask', GroupID: '', ConnectorID: '', PublicName: '' }

export function GatewayPIIPanel({ base }: { base: string }) {
  const [attempt, bump] = useAttempt()
  const { data, loading, error } = useGatewayLoader(async () => {
    const [rules, reviews, groups, connectors, tools] = await Promise.all([
      listPIIRules(base), listPIIReviews(base), listGroups(base), listConnectors(base), listTools(base),
    ])
    return { rules: rules.rules, reviews: reviews.reviews, groups: groups.groups, connectors: connectors.connectors, tools: tools.tools }
  }, attempt)
  const [rule, setRule] = useState<Partial<GatewayPIIRule>>(emptyRule)
  const [sample, setSample] = useState('')
  const [sampleDirection, setSampleDirection] = useState('input')
  const [testResult, setTestResult] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)

  async function save() {
    setBusy(true); setActionError(null)
    try { await savePIIRule(base, rule); setRule(emptyRule); bump() }
    catch (err: unknown) { setActionError(gatewayErrorMessage(err)) }
    finally { setBusy(false) }
  }

  async function remove(id: string) {
    setBusy(true); setActionError(null)
    try { await deletePIIRule(base, id); bump() }
    catch (err: unknown) { setActionError(gatewayErrorMessage(err)) }
    finally { setBusy(false) }
  }

  async function runTest() {
    setBusy(true); setActionError(null)
    try {
      const result = await testPII(base, sample, sampleDirection, {
        GroupIDs: rule.GroupID ? [rule.GroupID] : [], ConnectorID: rule.ConnectorID, PublicName: rule.PublicName,
      })
      setTestResult(`${result.decision.action}: ${result.decision.match_count} match(es)${result.decision.data_types.length ? ` (${result.decision.data_types.join(', ')})` : ''}${result.masked_preview ? `\n${result.masked_preview}` : ''}`)
    } catch (err: unknown) { setActionError(gatewayErrorMessage(err)) }
    finally { setBusy(false) }
  }

  async function approveReview(id: string) {
    setBusy(true); setActionError(null)
    try { await approvePIIReview(base, id); bump() }
    catch (err: unknown) { setActionError(gatewayErrorMessage(err)) }
    finally { setBusy(false) }
  }

  if (loading) return <ConsoleLoading label="Loading PII policy…" />
  if (!data) return <ConsoleError message={error ?? 'Failed to load PII policy.'} onRetry={bump} />

  return (
    <div className="space-y-4" data-testid="gateway-pii">
      {error && <ConsoleStale message={error} onRetry={bump} />}
      {actionError && <ConsoleError message={actionError} onRetry={bump} />}
      <SettingsCard icon={<ShieldCheck className="h-4 w-4 text-primary" />} title="PII policy"
        description="Regex and checksum detection.">
        <details className="mb-3 text-xs text-muted-foreground"><summary className="cursor-pointer">Default protection</summary><p className="mt-2">Email and US phone numbers are masked. SSNs, valid credit cards and known API keys are blocked. Text and JSON are inspected; opaque results are blocked.</p></details>
        <div className="space-y-3">
          <div className="grid gap-2 sm:grid-cols-3">
            <select aria-label="Data type" className={selectClass} value={rule.DataType} onChange={e => setRule(current => ({ ...current, DataType: e.target.value }))}>
              {['email', 'phone', 'ssn', 'credit_card', 'api_key'].map(value => <option key={value} value={value}>{value}</option>)}
            </select>
            <select aria-label="Direction" className={selectClass} value={rule.Direction} onChange={e => setRule(current => ({ ...current, Direction: e.target.value, Action: current.Action === 'require_review' && e.target.value !== 'input' ? 'block' : current.Action }))}>
              {['input', 'output', 'both'].map(value => <option key={value} value={value}>{value}</option>)}
            </select>
            <select aria-label="Action" className={selectClass} value={rule.Action} onChange={e => setRule(current => ({ ...current, Action: e.target.value }))}>
              {['allow', 'mask', 'block', ...(rule.Direction === 'input' ? ['require_review'] : [])].map(value => <option key={value} value={value}>{value}</option>)}
            </select>
            <select aria-label="Group scope" className={selectClass} value={rule.GroupID || ''} onChange={e => setRule(current => ({ ...current, GroupID: e.target.value }))}>
              <option value="">All groups</option>
              {data.groups.map(group => <option key={group.ID} value={group.ID}>{group.Name || group.ID}</option>)}
            </select>
            <select aria-label="Server scope" className={selectClass} value={rule.ConnectorID || ''} onChange={e => setRule(current => ({ ...current, ConnectorID: e.target.value, PublicName: '' }))}>
              <option value="">All servers</option>
              {data.connectors.map(connector => <option key={connector.ID} value={connector.ID}>{connector.Label || connector.Provider}</option>)}
            </select>
            <select aria-label="Tool scope" className={selectClass} value={rule.PublicName || ''} onChange={e => setRule(current => ({ ...current, PublicName: e.target.value }))}>
              <option value="">All tools</option>
              {data.tools.filter(tool => !rule.ConnectorID || tool.ConnectorID === rule.ConnectorID).map(tool => <option key={tool.PublicName} value={tool.PublicName}>{tool.PublicName}</option>)}
            </select>
          </div>
          <p className="text-xs text-muted-foreground">Require review applies to input only. Output matches are blocked to avoid repeating an upstream action.</p>
          <Button size="sm" disabled={busy} onClick={() => void save()}>{rule.ID ? 'Update rule' : 'Add rule'}</Button>
          {rule.ID && <Button size="sm" variant="ghost" onClick={() => setRule(emptyRule)}>Cancel edit</Button>}
          {data.rules.length === 0 ? <ConsoleEmpty>No custom rules. The default protection is active.</ConsoleEmpty> : (
            <div className="space-y-1">
              {data.rules.map(saved => <div key={saved.ID} className="flex flex-wrap items-center gap-2 border-t border-border py-2 text-xs">
                <span className="font-medium">{saved.DataType} · {saved.Direction} · {saved.Action}</span>
                <span className="text-muted-foreground">{saved.GroupID || 'all groups'} / {saved.ConnectorID || 'all servers'} / {saved.PublicName || 'all tools'}</span>
                <Button size="xs" variant="ghost" onClick={() => setRule(saved)}>Edit</Button>
                <Button size="xs" variant="ghost" disabled={busy} onClick={() => void remove(saved.ID)}>Delete</Button>
              </div>)}
            </div>
          )}
        </div>
      </SettingsCard>
      <SettingsCard title="Test a sample" description="Samples are not saved. Detection is best effort.">
        <div className="space-y-2">
          <Textarea aria-label="PII sample" value={sample} onChange={e => setSample(e.target.value)} rows={3} placeholder="Paste a sample value" />
          <select aria-label="Sample direction" className={selectClass} value={sampleDirection} onChange={e => setSampleDirection(e.target.value)}>
            <option value="input">Input to server</option><option value="output">Output to client</option>
          </select>
          <Button size="sm" disabled={busy || !sample} onClick={() => void runTest()}>Test policy</Button>
          {testResult && <pre className="whitespace-pre-wrap break-all rounded border border-border p-2 text-xs">{testResult}</pre>}
        </div>
      </SettingsCard>
      <SettingsCard title="Pending reviews" description="Approval allows one retry by the same caller.">
        {data.reviews.filter(review => review.Status === 'pending').length === 0 ? <ConsoleEmpty>No pending PII reviews.</ConsoleEmpty> : (
          <div className="space-y-2">
            {data.reviews.filter(review => review.Status === 'pending').map(review => <div key={review.ID} className="flex flex-wrap items-center gap-2 border-t border-border py-2 text-xs">
              <span className="font-mono">{review.ID}</span>
              <span>{review.UserID} · {review.PublicName} · {review.Direction} · {review.DataTypes?.join(', ')}</span>
              <Button size="xs" disabled={busy} onClick={() => void approveReview(review.ID)}>Approve retry</Button>
            </div>)}
          </div>
        )}
      </SettingsCard>
    </div>
  )
}
